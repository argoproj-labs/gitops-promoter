package scmserver

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // Gitea and Forgejo still send a SHA-1 signature header alongside SHA-256.
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxWebhookDeliveries = 2000

// WebhookSubscription delivers push and pull request events for repositories of a provider to a URL, as a hook
// subscribed to both event types on the provider would.
type WebhookSubscription struct {
	// Provider whose repositories are watched.
	Provider Provider `json:"provider"`
	// Repo is the full name of the watched repository ("owner/name", "org/project/name"); empty watches all
	// repositories of the provider.
	Repo string `json:"repo,omitempty"`
	// URL receives the webhook POST requests.
	URL string `json:"url"`
	// Secret signs deliveries the way the provider does (HMAC signature headers, GitLab token header, Azure DevOps
	// basic auth password).
	Secret string `json:"secret,omitempty"`
	// ID is assigned by the server.
	ID int64 `json:"id"`
}

// WebhookDelivery records one webhook delivery attempt.
type WebhookDelivery struct {
	Time     time.Time         `json:"time"`
	Headers  map[string]string `json:"headers"`
	ID       string            `json:"id"`
	Provider Provider          `json:"provider"`
	Repo     string            `json:"repo"`
	Event    string            `json:"event"`
	Ref      string            `json:"ref"`
	Before   string            `json:"before"`
	After    string            `json:"after"`
	// Action is the provider-neutral pull request action of a pull request event: opened, closed, reopened, merged.
	Action         string          `json:"action,omitempty"`
	URL            string          `json:"url"`
	Error          string          `json:"error,omitempty"`
	Payload        json.RawMessage `json:"payload"`
	SubscriptionID int64           `json:"subscriptionID"`
	// Number is the pull request number of a pull request event.
	Number     int  `json:"number,omitempty"`
	StatusCode int  `json:"statusCode"`
	Dropped    bool `json:"dropped"`
}

type webhookDispatcher struct {
	client     *http.Client
	subs       []WebhookSubscription
	deliveries []WebhookDelivery
	wg         sync.WaitGroup
	mu         sync.Mutex
	nextID     int64
}

func newWebhookDispatcher() *webhookDispatcher {
	return &webhookDispatcher{client: &http.Client{Timeout: 15 * time.Second}}
}

func (d *webhookDispatcher) add(sub WebhookSubscription) WebhookSubscription {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.nextID++
	sub.ID = d.nextID
	d.subs = append(d.subs, sub)
	return sub
}

func (d *webhookDispatcher) remove(id int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, sub := range d.subs {
		if sub.ID == id {
			d.subs = append(d.subs[:i], d.subs[i+1:]...)
			return true
		}
	}
	return false
}

func (d *webhookDispatcher) list() []WebhookSubscription {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]WebhookSubscription(nil), d.subs...)
}

func (d *webhookDispatcher) listDeliveries() []WebhookDelivery {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]WebhookDelivery(nil), d.deliveries...)
}

func (d *webhookDispatcher) reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.subs = nil
	d.deliveries = nil
}

func (d *webhookDispatcher) record(delivery WebhookDelivery) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deliveries = append(d.deliveries, delivery)
	if len(d.deliveries) > maxWebhookDeliveries {
		d.deliveries = d.deliveries[len(d.deliveries)-maxWebhookDeliveries:]
	}
}

// wait blocks until in-flight deliveries finish.
func (d *webhookDispatcher) wait() {
	d.wg.Wait()
}

// pushEvent is a provider-neutral description of a push used to build provider payloads.
type pushEvent struct {
	occurred time.Time
	commits  []commitInfo
	baseURL  string
	ref      string
	before   string
	after    string
	pusher   string
	deliver  string
	repo     repoSnapshot
	hookID   int64
}

// repoSnapshot is an immutable copy of the repository fields used outside store.mu.
type repoSnapshot struct {
	provider      Provider
	owner         string
	project       string
	name          string
	uuid          string
	projectUUID   string
	defaultBranch string
	fullName      string
	id            int64
}

func snapshotRepo(r *repo) repoSnapshot {
	return repoSnapshot{
		provider: r.provider, owner: r.owner, project: r.project, name: r.name, uuid: r.uuid,
		projectUUID: r.projectUUID, defaultBranch: r.defaultBranch, fullName: r.fullName(), id: r.id,
	}
}

// notifyPush delivers push events for changes to every matching subscription, asynchronously.
func (d *webhookDispatcher) notifyPush(ctx context.Context, s *Server, r *repo, changes []refChange, pusher string) {
	s.store.mu.Lock()
	snap := snapshotRepo(r)
	s.store.mu.Unlock()

	d.mu.Lock()
	var subs []WebhookSubscription
	for _, sub := range d.subs {
		if sub.Provider == snap.provider && (sub.Repo == "" || strings.EqualFold(sub.Repo, snap.fullName)) {
			subs = append(subs, sub)
		}
	}
	d.mu.Unlock()
	if len(subs) == 0 {
		return
	}

	behavior := s.behavior.get(snap.provider)
	for _, change := range changes {
		var commits []commitInfo
		if change.after != zeroSHA {
			var err error
			commits, err = s.git.log(ctx, r.dir, change.before, change.after, 20)
			if err != nil {
				s.log.Error("list pushed commits", "repo", snap.fullName, "ref", change.ref, "error", err)
			}
		}
		for _, sub := range subs {
			ev := pushEvent{
				repo: snap, commits: commits, baseURL: s.publicBaseURL(snap.provider), ref: change.ref,
				before: change.before, after: change.after, pusher: pusher, hookID: sub.ID,
				deliver: newGUID(), occurred: s.now(),
			}
			delivery := WebhookDelivery{
				ID: ev.deliver, SubscriptionID: sub.ID, Provider: sub.Provider, Repo: snap.fullName, Event: "push",
				Ref: ev.ref, Before: ev.before, After: ev.after, URL: sub.URL,
			}
			payload, headers, err := buildPushPayload(ev, sub.Secret)
			d.dispatch(ctx, sub, delivery, payload, headers, err, behavior)
		}
	}
}

// prAction is a provider-neutral pull request lifecycle event.
type prAction string

const (
	prActionOpened   prAction = "opened"
	prActionClosed   prAction = "closed"
	prActionReopened prAction = "reopened"
	prActionMerged   prAction = "merged"
)

// transitionAction maps a pull request state change to the event a provider sends for it, or "" for no event.
func transitionAction(before, after prState) prAction {
	switch {
	case before == after:
		return ""
	case after == prMerged:
		return prActionMerged
	case after == prClosed:
		return prActionClosed
	case after == prOpen && before == prClosed:
		return prActionReopened
	default:
		return ""
	}
}

// notifyPullRequestTransition sends the pull request event, if any, for a state change from before to after.
func (s *Server) notifyPullRequestTransition(ctx context.Context, pr *pullRequest, before, after prState) {
	if action := transitionAction(before, after); action != "" {
		s.webhooks.notifyPullRequest(ctx, s, pr, action)
	}
}

// notifyPullRequest delivers a pull request event for pr to every matching subscription, asynchronously, in the
// provider's own format: GitHub's and Gitea's "pull_request", GitLab's "Merge Request Hook", Azure DevOps'
// git.pullrequest.* service hook and Bitbucket Cloud's pullrequest:* events.
func (d *webhookDispatcher) notifyPullRequest(ctx context.Context, s *Server, pr *pullRequest, action prAction) {
	s.store.mu.Lock()
	r := pr.repo
	snap := snapshotRepo(r)
	number := pr.number
	s.store.mu.Unlock()

	d.mu.Lock()
	var subs []WebhookSubscription
	for _, sub := range d.subs {
		if sub.Provider == snap.provider && (sub.Repo == "" || strings.EqualFold(sub.Repo, snap.fullName)) {
			subs = append(subs, sub)
		}
	}
	d.mu.Unlock()
	if len(subs) == 0 {
		return
	}

	behavior := s.behavior.get(snap.provider)
	for _, sub := range subs {
		id := newGUID()
		delivery := WebhookDelivery{
			ID: id, SubscriptionID: sub.ID, Provider: sub.Provider, Repo: snap.fullName, Event: "pull_request",
			Action: string(action), Number: number, URL: sub.URL,
		}
		payload, headers, err := s.buildPullRequestPayload(pr, action, id, sub.ID, sub.Secret)
		if err == nil && payload == nil {
			// The provider sends no event for this transition.
			continue
		}
		d.dispatch(ctx, sub, delivery, payload, headers, err, behavior)
	}
}

// dispatch records a built delivery and sends it in the background. A build error is recorded without sending.
func (d *webhookDispatcher) dispatch(ctx context.Context, sub WebhookSubscription, delivery WebhookDelivery, payload []byte, headers http.Header, buildErr error, behavior Behavior) {
	if buildErr != nil {
		delivery.Error = buildErr.Error()
		delivery.Time = time.Now().UTC()
		d.record(delivery)
		return
	}
	delivery.Payload = payload
	delivery.Headers = map[string]string{}
	for k, v := range headers {
		delivery.Headers[k] = strings.Join(v, ",")
	}
	d.wg.Go(func() {
		d.deliver(context.WithoutCancel(ctx), sub, delivery, payload, headers, behavior)
	})
}

func (d *webhookDispatcher) deliver(ctx context.Context, sub WebhookSubscription, delivery WebhookDelivery, payload []byte, headers http.Header, behavior Behavior) {
	if behavior.WebhookDelay.Duration > 0 {
		select {
		case <-time.After(behavior.WebhookDelay.Duration):
		case <-ctx.Done():
		}
	}
	delivery.Time = time.Now().UTC()
	if behavior.WebhookDropRate > 0 && rand.Float64() < behavior.WebhookDropRate { //nolint:gosec // fault injection, not security
		delivery.Dropped = true
		d.record(delivery)
		return
	}

	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, sub.URL, bytes.NewReader(payload))
	if err != nil {
		delivery.Error = err.Error()
		d.record(delivery)
		return
	}
	for k, v := range headers {
		req.Header[k] = v
	}
	resp, err := d.client.Do(req)
	if err != nil {
		delivery.Error = err.Error()
		d.record(delivery)
		return
	}
	if resp == nil {
		delivery.Error = "nil response"
		d.record(delivery)
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	delivery.StatusCode = resp.StatusCode
	d.record(delivery)
}

func hmacHex(secret string, payload []byte, sha256Hash bool) string {
	var mac []byte
	if sha256Hash {
		h := hmac.New(sha256.New, []byte(secret))
		_, _ = h.Write(payload)
		mac = h.Sum(nil)
	} else {
		h := hmac.New(sha1.New, []byte(secret))
		_, _ = h.Write(payload)
		mac = h.Sum(nil)
	}
	return hex.EncodeToString(mac)
}

// buildPushPayload renders a push event in the provider's webhook format, with the headers the provider sends.
func buildPushPayload(ev pushEvent, secret string) ([]byte, http.Header, error) {
	var body any
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")

	switch ev.repo.provider {
	case GitHub:
		body = githubPushPayload(ev)
		headers.Set("User-Agent", "GitHub-Hookshot/scmserver")
		headers.Set("X-Github-Event", "push")
		headers.Set("X-Github-Delivery", ev.deliver)
		headers.Set("X-Github-Hook-Id", strconv.FormatInt(ev.hookID, 10))
		headers.Set("X-Github-Hook-Installation-Target-Type", "repository")
	case GitLab:
		body = gitlabPushPayload(ev)
		headers.Set("User-Agent", "GitLab/17.5.0")
		headers.Set("X-Gitlab-Event", "Push Hook")
		headers.Set("X-Gitlab-Event-Uuid", ev.deliver)
		headers.Set("X-Gitlab-Webhook-Uuid", ev.deliver)
		headers.Set("X-Gitlab-Instance", ev.baseURL)
		if secret != "" {
			headers.Set("X-Gitlab-Token", secret)
		}
	case Gitea, Forgejo:
		body = giteaPushPayload(ev)
	case BitbucketCloud:
		body = bitbucketPushPayload(ev)
		headers.Set("User-Agent", "Bitbucket-Webhooks/2.0")
		headers.Set("X-Event-Key", "repo:push")
		headers.Set("X-Hook-Uuid", "{"+newGUID()+"}")
		headers.Set("X-Request-Uuid", ev.deliver)
		headers.Set("X-Attempt-Number", "1")
	case AzureDevOps:
		body = azurePushPayload(ev)
		headers.Set("Content-Type", "application/json; charset=utf-8")
		headers.Set("X-Vss-Activityid", ev.deliver)
		headers.Set("X-Vss-Subscriptionid", newGUID())
		if secret != "" {
			headers.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("scmserver:"+secret)))
		}
	default:
		return nil, nil, fmt.Errorf("unsupported provider %q", ev.repo.provider)
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal %s push payload: %w", ev.repo.provider, err)
	}

	switch ev.repo.provider {
	case GitHub:
		if secret != "" {
			headers.Set("X-Hub-Signature-256", "sha256="+hmacHex(secret, payload, true))
			headers.Set("X-Hub-Signature", "sha1="+hmacHex(secret, payload, false))
		}
	case BitbucketCloud:
		if secret != "" {
			headers.Set("X-Hub-Signature", "sha256="+hmacHex(secret, payload, true))
		}
	case Gitea, Forgejo:
		addGiteaHeaders(headers, ev.repo.provider, "push", ev.deliver, secret, payload)
	default:
	}
	return payload, headers, nil
}

// addGiteaHeaders adds the header set Gitea and Forgejo send with every webhook. Both also send GitHub and Gogs
// compatibility headers (X-GitHub-Event is set with that exact, non-canonical spelling), so a receiver that checks
// GitHub headers first classifies their deliveries as GitHub.
func addGiteaHeaders(headers http.Header, p Provider, event, deliveryID, secret string, payload []byte) {
	sig256, sig1 := "", ""
	if secret != "" {
		sig256 = hmacHex(secret, payload, true)
		sig1 = hmacHex(secret, payload, false)
	}
	prefixes := []string{"X-Gitea", "X-Gogs"}
	if p == Forgejo {
		prefixes = append([]string{"X-Forgejo"}, prefixes...)
	}
	for _, prefix := range prefixes {
		headers.Set(prefix+"-Delivery", deliveryID)
		headers.Set(prefix+"-Event", event)
		headers.Set(prefix+"-Event-Type", event)
		headers.Set(prefix+"-Signature", sig256)
	}
	headers.Set("X-Hub-Signature", "sha1="+sig1)
	headers.Set("X-Hub-Signature-256", "sha256="+sig256)
	headers["X-GitHub-Delivery"] = []string{deliveryID}
	headers["X-GitHub-Event"] = []string{event}
	headers["X-GitHub-Event-Type"] = []string{event}
	headers.Set("User-Agent", "Go-http-client/1.1")
}

// buildPullRequestPayload renders a pull request event for pr in its provider's webhook format with the headers the
// provider sends. It returns a nil payload and nil error when the provider sends no event for action.
func (s *Server) buildPullRequestPayload(pr *pullRequest, action prAction, deliveryID string, hookID int64, secret string) ([]byte, http.Header, error) {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	r := pr.repo
	p := r.provider
	base := s.publicBaseURL(p)
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	var body any

	switch p {
	case GitHub:
		// https://docs.github.com/webhooks/webhook-events-and-payloads#pull_request: a merge is action "closed" with
		// pull_request.merged true.
		ghAction := map[prAction]string{prActionOpened: "opened", prActionClosed: "closed", prActionReopened: "reopened", prActionMerged: "closed"}[action]
		body = map[string]any{
			"action":       ghAction,
			"number":       pr.number,
			"pull_request": s.githubPullJSONLocked(pr, base),
			"repository":   s.githubRepoJSON(r, base),
			"sender":       s.githubUserJSON(pr.author),
		}
		headers.Set("User-Agent", "GitHub-Hookshot/scmserver")
		headers.Set("X-Github-Event", "pull_request")
		headers.Set("X-Github-Delivery", deliveryID)
		headers.Set("X-Github-Hook-Id", strconv.FormatInt(hookID, 10))
		headers.Set("X-Github-Hook-Installation-Target-Type", "repository")
	case GitLab:
		// https://docs.gitlab.com/user/project/integrations/webhook_events/#merge-request-events
		glAction := map[prAction]string{prActionOpened: "open", prActionClosed: "close", prActionReopened: "reopen", prActionMerged: "merge"}[action]
		attrs := s.gitlabMRJSONLocked(pr, base)
		attrs["action"] = glAction
		attrs["last_commit"] = map[string]any{"id": pr.headSHA}
		project := s.gitlabProjectJSON(r, base)
		body = map[string]any{
			"object_kind":       "merge_request",
			"event_type":        "merge_request",
			"user":              gitlabUserJSON(pr.author),
			"project":           project,
			"object_attributes": attrs,
			"labels":            []any{},
			"changes":           map[string]any{},
			"repository": map[string]any{
				"name": r.name, "url": base + "/" + r.fullName() + ".git", "homepage": base + "/" + r.fullName(),
			},
		}
		headers.Set("User-Agent", "GitLab/17.5.0")
		headers.Set("X-Gitlab-Event", "Merge Request Hook")
		headers.Set("X-Gitlab-Event-Uuid", deliveryID)
		headers.Set("X-Gitlab-Webhook-Uuid", deliveryID)
		headers.Set("X-Gitlab-Instance", base)
		if secret != "" {
			headers.Set("X-Gitlab-Token", secret)
		}
	case Gitea, Forgejo:
		// https://docs.gitea.com/usage/webhooks: the pull_request event uses GitHub's shape; a merge is "closed" with
		// pull_request.merged true.
		gtAction := map[prAction]string{prActionOpened: "opened", prActionClosed: "closed", prActionReopened: "reopened", prActionMerged: "closed"}[action]
		body = map[string]any{
			"action":       gtAction,
			"number":       pr.number,
			"pull_request": s.giteaPullJSONLocked(pr, base),
			"repository":   s.giteaRepoJSON(r, base),
			"sender":       giteaUserJSON(pr.author),
		}
	case BitbucketCloud:
		// https://support.atlassian.com/bitbucket-cloud/docs/event-payloads/#Pull-request-events. A declined pull
		// request cannot be reopened, so there is no event for it.
		eventKey := map[prAction]string{prActionOpened: "pullrequest:created", prActionClosed: "pullrequest:rejected", prActionMerged: "pullrequest:fulfilled"}[action]
		if eventKey == "" {
			return nil, nil, nil
		}
		body = map[string]any{
			"pullrequest": s.bitbucketPRJSONLocked(pr, base),
			"repository":  s.bitbucketRepoJSON(r, base),
			"actor":       bitbucketUserJSON(pr.author),
		}
		headers.Set("User-Agent", "Bitbucket-Webhooks/2.0")
		headers.Set("X-Event-Key", eventKey)
		headers.Set("X-Hook-Uuid", "{"+newGUID()+"}")
		headers.Set("X-Request-Uuid", deliveryID)
		headers.Set("X-Attempt-Number", "1")
	case AzureDevOps:
		// https://learn.microsoft.com/azure/devops/service-hooks/events#git.pullrequest.updated: status changes
		// (abandoned, reactivated, completed) are "Pull request updated" events; creation is git.pullrequest.created.
		eventType := "git.pullrequest.updated"
		if action == prActionOpened {
			eventType = "git.pullrequest.created"
		}
		text := fmt.Sprintf("%s %s pull request %d (%s) in %s", pr.author, action, pr.number, pr.title, r.name)
		body = map[string]any{
			"subscriptionId":  newGUID(),
			"notificationId":  1,
			"id":              deliveryID,
			"eventType":       eventType,
			"publisherId":     "tfs",
			"message":         map[string]any{"text": text, "html": text, "markdown": text},
			"detailedMessage": map[string]any{"text": text, "html": text, "markdown": text},
			"resource":        s.azurePRJSONLocked(pr, base, true),
			"resourceVersion": "1.0",
			"resourceContainers": map[string]any{
				"collection": map[string]any{"id": newGUID()},
				"account":    map[string]any{"id": newGUID()},
				"project":    map[string]any{"id": r.projectUUID},
			},
			"createdDate": s.now().Format(azureTimeFormat),
		}
		headers.Set("Content-Type", "application/json; charset=utf-8")
		headers.Set("X-Vss-Activityid", deliveryID)
		headers.Set("X-Vss-Subscriptionid", newGUID())
		if secret != "" {
			headers.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("scmserver:"+secret)))
		}
	default:
		return nil, nil, fmt.Errorf("unsupported provider %q", p)
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal %s pull request payload: %w", p, err)
	}
	switch p {
	case GitHub:
		if secret != "" {
			headers.Set("X-Hub-Signature-256", "sha256="+hmacHex(secret, payload, true))
			headers.Set("X-Hub-Signature", "sha1="+hmacHex(secret, payload, false))
		}
	case BitbucketCloud:
		if secret != "" {
			headers.Set("X-Hub-Signature", "sha256="+hmacHex(secret, payload, true))
		}
	case Gitea, Forgejo:
		addGiteaHeaders(headers, p, "pull_request", deliveryID, secret, payload)
	default:
	}
	return payload, headers, nil
}

func refShortName(ref string) string {
	if name, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
		return name
	}
	if name, ok := strings.CutPrefix(ref, "refs/tags/"); ok {
		return name
	}
	return ref
}

func headCommit(ev pushEvent) *commitInfo {
	if len(ev.commits) == 0 {
		return nil
	}
	return &ev.commits[0]
}

func githubCommitJSON(ev pushEvent, c commitInfo) map[string]any {
	return map[string]any{
		"id":        c.sha,
		"tree_id":   "",
		"distinct":  true,
		"message":   c.message,
		"timestamp": c.when.Format(time.RFC3339),
		"url":       fmt.Sprintf("%s/%s/commit/%s", ev.baseURL, ev.repo.fullName, c.sha),
		"author":    map[string]any{"name": c.authorName, "email": c.authorEmail},
		"committer": map[string]any{"name": c.authorName, "email": c.authorEmail},
		"added":     []string{},
		"removed":   []string{},
		"modified":  []string{},
	}
}

func githubPushPayload(ev pushEvent) map[string]any {
	commits := make([]map[string]any, 0, len(ev.commits))
	for i := len(ev.commits) - 1; i >= 0; i-- {
		commits = append(commits, githubCommitJSON(ev, ev.commits[i]))
	}
	var head any
	if c := headCommit(ev); c != nil {
		head = githubCommitJSON(ev, *c)
	}
	return map[string]any{
		"ref":         ev.ref,
		"before":      ev.before,
		"after":       ev.after,
		"created":     ev.before == zeroSHA,
		"deleted":     ev.after == zeroSHA,
		"forced":      false,
		"base_ref":    nil,
		"compare":     fmt.Sprintf("%s/%s/compare/%s...%s", ev.baseURL, ev.repo.fullName, ev.before[:12], ev.after[:12]),
		"commits":     commits,
		"head_commit": head,
		"repository": map[string]any{
			"id": ev.repo.id, "name": ev.repo.name, "full_name": ev.repo.fullName, "private": true,
			"owner":          map[string]any{"login": ev.repo.owner, "name": ev.repo.owner},
			"html_url":       ev.baseURL + "/" + ev.repo.fullName,
			"clone_url":      ev.baseURL + "/" + ev.repo.fullName + ".git",
			"default_branch": ev.repo.defaultBranch,
			"master_branch":  ev.repo.defaultBranch,
		},
		"pusher": map[string]any{"name": ev.pusher, "email": ev.pusher + "@users.noreply.github.test"},
		"sender": map[string]any{"login": ev.pusher, "type": "Bot"},
	}
}

func gitlabPushPayload(ev pushEvent) map[string]any {
	commits := make([]map[string]any, 0, len(ev.commits))
	for i := len(ev.commits) - 1; i >= 0; i-- {
		c := ev.commits[i]
		title, _, _ := strings.Cut(c.message, "\n")
		commits = append(commits, map[string]any{
			"id": c.sha, "message": c.message, "title": title, "timestamp": c.when.Format(time.RFC3339),
			"url":    fmt.Sprintf("%s/%s/-/commit/%s", ev.baseURL, ev.repo.fullName, c.sha),
			"author": map[string]any{"name": c.authorName, "email": c.authorEmail},
			"added":  []string{}, "modified": []string{}, "removed": []string{},
		})
	}
	project := map[string]any{
		"id": ev.repo.id, "name": ev.repo.name, "web_url": ev.baseURL + "/" + ev.repo.fullName,
		"git_http_url": ev.baseURL + "/" + ev.repo.fullName + ".git", "namespace": ev.repo.owner,
		"path_with_namespace": ev.repo.fullName, "default_branch": ev.repo.defaultBranch, "visibility_level": 0,
	}
	var checkout any = ev.after
	if ev.after == zeroSHA {
		checkout = nil
	}
	return map[string]any{
		"object_kind":         "push",
		"event_name":          "push",
		"before":              ev.before,
		"after":               ev.after,
		"ref":                 ev.ref,
		"ref_protected":       false,
		"checkout_sha":        checkout,
		"user_id":             1,
		"user_name":           ev.pusher,
		"user_username":       ev.pusher,
		"user_email":          "",
		"project_id":          ev.repo.id,
		"project":             project,
		"commits":             commits,
		"total_commits_count": len(commits),
		"repository": map[string]any{
			"name": ev.repo.name, "url": ev.baseURL + "/" + ev.repo.fullName + ".git",
			"homepage": ev.baseURL + "/" + ev.repo.fullName, "git_http_url": ev.baseURL + "/" + ev.repo.fullName + ".git",
		},
	}
}

func giteaPushPayload(ev pushEvent) map[string]any {
	commits := make([]map[string]any, 0, len(ev.commits))
	for i := len(ev.commits) - 1; i >= 0; i-- {
		c := ev.commits[i]
		commits = append(commits, map[string]any{
			"id": c.sha, "message": c.message + "\n", "timestamp": c.when.Format(time.RFC3339),
			"url":       fmt.Sprintf("%s/%s/commit/%s", ev.baseURL, ev.repo.fullName, c.sha),
			"author":    map[string]any{"name": c.authorName, "email": c.authorEmail, "username": ""},
			"committer": map[string]any{"name": c.authorName, "email": c.authorEmail, "username": ""},
			"added":     []string{}, "removed": []string{}, "modified": []string{},
		})
	}
	var head any
	if len(commits) > 0 {
		head = commits[len(commits)-1]
	}
	user := map[string]any{"id": 1, "login": ev.pusher, "username": ev.pusher, "full_name": ev.pusher, "email": ""}
	return map[string]any{
		"ref":           ev.ref,
		"before":        ev.before,
		"after":         ev.after,
		"compare_url":   fmt.Sprintf("%s/%s/compare/%s...%s", ev.baseURL, ev.repo.fullName, ev.before, ev.after),
		"commits":       commits,
		"total_commits": len(commits),
		"head_commit":   head,
		"repository": map[string]any{
			"id": ev.repo.id, "name": ev.repo.name, "full_name": ev.repo.fullName, "private": true,
			"owner":          map[string]any{"id": 1, "login": ev.repo.owner, "username": ev.repo.owner},
			"html_url":       ev.baseURL + "/" + ev.repo.fullName,
			"clone_url":      ev.baseURL + "/" + ev.repo.fullName + ".git",
			"default_branch": ev.repo.defaultBranch,
		},
		"pusher": user,
		"sender": user,
	}
}

func bitbucketPushPayload(ev pushEvent) map[string]any {
	branch := func(sha string) any {
		if sha == zeroSHA {
			return nil
		}
		return map[string]any{
			"type": "branch",
			"name": refShortName(ev.ref),
			"target": map[string]any{
				"type": "commit", "hash": sha,
				"links": map[string]any{"html": map[string]any{"href": fmt.Sprintf("%s/%s/commits/%s", ev.baseURL, ev.repo.fullName, sha)}},
			},
		}
	}
	commits := make([]map[string]any, 0, len(ev.commits))
	for _, c := range ev.commits {
		commits = append(commits, map[string]any{
			"type": "commit", "hash": c.sha, "message": c.message + "\n", "date": c.when.Format(time.RFC3339),
			"author": map[string]any{"type": "author", "raw": fmt.Sprintf("%s <%s>", c.authorName, c.authorEmail)},
		})
	}
	return map[string]any{
		"push": map[string]any{
			"changes": []map[string]any{{
				"old":       branch(ev.before),
				"new":       branch(ev.after),
				"created":   ev.before == zeroSHA,
				"closed":    ev.after == zeroSHA,
				"forced":    false,
				"truncated": false,
				"commits":   commits,
			}},
		},
		"repository": map[string]any{
			"type": "repository", "full_name": ev.repo.fullName, "name": ev.repo.name, "uuid": ev.repo.uuid,
			"is_private": true,
			"links":      map[string]any{"html": map[string]any{"href": ev.baseURL + "/" + ev.repo.fullName}},
			"workspace":  map[string]any{"type": "workspace", "slug": ev.repo.owner},
		},
		"actor": map[string]any{"type": "user", "display_name": ev.pusher, "nickname": ev.pusher, "uuid": "{" + newGUID() + "}"},
	}
}

func azurePushPayload(ev pushEvent) map[string]any {
	repoURL := fmt.Sprintf("%s/%s/%s/_apis/git/repositories/%s", ev.baseURL, ev.repo.owner, ev.repo.projectUUID, ev.repo.uuid)
	commits := make([]map[string]any, 0, len(ev.commits))
	for _, c := range ev.commits {
		who := map[string]any{"name": c.authorName, "email": c.authorEmail, "date": c.when.Format(azureTimeFormat)}
		commits = append(commits, map[string]any{
			"commitId": c.sha, "author": who, "committer": who, "comment": c.message,
			"url": repoURL + "/commits/" + c.sha,
		})
	}
	text := fmt.Sprintf("%s pushed updates to %s:%s.", ev.pusher, ev.repo.name, refShortName(ev.ref))
	return map[string]any{
		"subscriptionId":  newGUID(),
		"notificationId":  1,
		"id":              ev.deliver,
		"eventType":       "git.push",
		"publisherId":     "tfs",
		"message":         map[string]any{"text": text, "html": text, "markdown": text},
		"detailedMessage": map[string]any{"text": text, "html": text, "markdown": text},
		"resource": map[string]any{
			"commits":    commits,
			"refUpdates": []map[string]any{{"name": ev.ref, "oldObjectId": ev.before, "newObjectId": ev.after}},
			"repository": map[string]any{
				"id": ev.repo.uuid, "name": ev.repo.name, "url": repoURL,
				"project": map[string]any{
					"id": ev.repo.projectUUID, "name": ev.repo.project, "state": "wellFormed",
					"url": fmt.Sprintf("%s/%s/_apis/projects/%s", ev.baseURL, ev.repo.owner, ev.repo.projectUUID),
				},
				"defaultBranch": "refs/heads/" + ev.repo.defaultBranch,
				"remoteUrl":     fmt.Sprintf("%s/%s/%s/_git/%s", ev.baseURL, ev.repo.owner, ev.repo.project, ev.repo.name),
			},
			"pushedBy": map[string]any{"displayName": ev.pusher, "uniqueName": ev.pusher, "id": newGUID()},
			"pushId":   ev.hookID,
			"date":     ev.occurred.Format(azureTimeFormat),
			"url":      repoURL + "/pushes",
		},
		"resourceVersion": "1.0",
		"resourceContainers": map[string]any{
			"collection": map[string]any{"id": newGUID()},
			"account":    map[string]any{"id": newGUID()},
			"project":    map[string]any{"id": ev.repo.projectUUID},
		},
		"createdDate": ev.occurred.Format(azureTimeFormat),
	}
}
