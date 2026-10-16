package scmserver

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const githubAPI = "/api/v3"

func (s *Server) githubRoutes(mux *http.ServeMux) {
	api := func(pattern string, h func(http.ResponseWriter, *http.Request, principal)) {
		method, path, _ := strings.Cut(pattern, " ")
		mux.HandleFunc(method+" "+githubAPI+path, s.apiHandler(GitHub, h))
	}
	app := func(pattern string, h func(http.ResponseWriter, *http.Request, *githubApp)) {
		method, path, _ := strings.Cut(pattern, " ")
		mux.HandleFunc(method+" "+githubAPI+path, s.githubAppHandler(h))
	}
	// The Checks API is only available to GitHub Apps: personal access tokens get 403.
	checks := func(pattern string, h func(http.ResponseWriter, *http.Request, principal)) {
		api(pattern, func(w http.ResponseWriter, r *http.Request, who principal) {
			if who.installationID == 0 {
				writeProviderError(w, GitHub, http.StatusForbidden, "You must authenticate via a GitHub App.")
				return
			}
			h(w, r, who)
		})
	}

	app("GET /app", s.githubGetApp)
	app("GET /app/installations", s.githubListInstallations)
	app("POST /app/installations/{id}/access_tokens", s.githubCreateInstallationToken)
	app("GET /repos/{owner}/{repo}/installation", s.githubRepoInstallation)

	api("GET /repos/{owner}/{repo}", s.githubGetRepo)
	api("GET /repos/{owner}/{repo}/pulls", s.githubListPulls)
	api("POST /repos/{owner}/{repo}/pulls", s.githubCreatePull)
	api("GET /repos/{owner}/{repo}/pulls/{number}", s.githubGetPull)
	api("PATCH /repos/{owner}/{repo}/pulls/{number}", s.githubEditPull)
	api("GET /repos/{owner}/{repo}/pulls/{number}/merge", s.githubIsMerged)
	api("PUT /repos/{owner}/{repo}/pulls/{number}/merge", s.githubMergePull)
	api("GET /repos/{owner}/{repo}/labels", s.githubListLabels)
	api("POST /repos/{owner}/{repo}/labels", s.githubCreateLabel)
	api("GET /repos/{owner}/{repo}/issues/{number}/labels", s.githubListIssueLabels)
	api("POST /repos/{owner}/{repo}/issues/{number}/labels", s.githubAddIssueLabels)
	api("DELETE /repos/{owner}/{repo}/issues/{number}/labels/{name}", s.githubRemoveIssueLabel)
	checks("POST /repos/{owner}/{repo}/check-runs", s.githubCreateCheckRun)
	checks("GET /repos/{owner}/{repo}/check-runs/{id}", s.githubGetCheckRun)
	checks("PATCH /repos/{owner}/{repo}/check-runs/{id}", s.githubUpdateCheckRun)
	checks("GET /repos/{owner}/{repo}/commits/{ref}/check-runs", s.githubListCheckRuns)
	api("POST /repos/{owner}/{repo}/statuses/{sha}", s.githubCreateStatus)
	api("GET /repos/{owner}/{repo}/commits/{ref}/status", s.githubCombinedStatus)
}

// githubAppHandler authenticates GitHub App JWT requests. JWT requests count against the App's own rate limit and
// carry rate limit headers, as on GitHub.
func (s *Server) githubAppHandler(h func(http.ResponseWriter, *http.Request, *githubApp)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scheme, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		if !strings.EqualFold(scheme, "bearer") {
			writeProviderError(w, GitHub, http.StatusUnauthorized, "A JSON web token could not be decoded")
			return
		}
		app, problem := s.verifyGitHubJWT(token)
		if app == nil {
			writeProviderError(w, GitHub, http.StatusUnauthorized, problem)
			return
		}
		who := principal{name: "app:" + strconv.FormatInt(app.id, 10), key: "app:" + strconv.FormatInt(app.id, 10)}
		if rec, ok := w.(*recorder); ok {
			rec.principal = who.name
		}
		if !s.rateLimit(w, GitHub, who) {
			return
		}
		h(w, r, app)
	}
}

func (s *Server) githubGetApp(w http.ResponseWriter, r *http.Request, app *githubApp) {
	writeJSON(w, http.StatusOK, map[string]any{
		"id": app.id, "slug": "promoter-app", "name": "Promoter App",
		"owner": map[string]any{"login": "promoter", "type": "Organization"},
	})
}

func githubInstallationJSON(app *githubApp, inst githubInstallation) map[string]any {
	return map[string]any{
		"id":                   inst.id,
		"app_id":               app.id,
		"app_slug":             "promoter-app",
		"target_type":          "Organization",
		"repository_selection": "all",
		"account":              map[string]any{"login": inst.account, "id": inst.id, "type": "Organization"},
		"permissions":          map[string]string{"contents": "write", "pull_requests": "write", "checks": "write"},
	}
}

func (s *Server) githubListInstallations(w http.ResponseWriter, r *http.Request, app *githubApp) {
	s.store.mu.Lock()
	installations := make([]githubInstallation, len(app.installations))
	copy(installations, app.installations)
	s.store.mu.Unlock()
	page, size := pageParams(r, "per_page", 30, 100)
	start, end := pageWindow(len(installations), page, size)
	out := make([]map[string]any, 0, end-start)
	for _, inst := range installations[start:end] {
		out = append(out, githubInstallationJSON(app, inst))
	}
	setLinkHeader(w, r, page, lastPage(len(installations), size))
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) githubCreateInstallationToken(w http.ResponseWriter, r *http.Request, app *githubApp) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeProviderError(w, GitHub, http.StatusNotFound, "Not Found")
		return
	}
	s.store.mu.Lock()
	found := slices.ContainsFunc(app.installations, func(i githubInstallation) bool { return i.id == id })
	s.store.mu.Unlock()
	if !found {
		writeProviderError(w, GitHub, http.StatusNotFound, "Not Found")
		return
	}
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		writeProviderError(w, GitHub, http.StatusInternalServerError, err.Error())
		return
	}
	token := "ghs_" + hex.EncodeToString(buf)
	expires := s.now().Add(s.behavior.get(GitHub).TokenTTL.Duration).Truncate(time.Second)
	s.store.mu.Lock()
	s.store.installTokens[token] = installationToken{installationID: id, expiresAt: expires}
	s.store.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":                token,
		"expires_at":           expires.Format(githubTimeFormat),
		"permissions":          map[string]string{"contents": "write", "pull_requests": "write", "checks": "write"},
		"repository_selection": "all",
	})
}

func (s *Server) githubRepoInstallation(w http.ResponseWriter, r *http.Request, app *githubApp) {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if _, err := s.store.findRepoLocked(GitHub, r.PathValue("owner"), "", r.PathValue("repo")); err != nil {
		writeProviderError(w, GitHub, http.StatusNotFound, "Not Found")
		return
	}
	for _, inst := range app.installations {
		if strings.EqualFold(inst.account, r.PathValue("owner")) {
			writeJSON(w, http.StatusOK, githubInstallationJSON(app, inst))
			return
		}
	}
	writeProviderError(w, GitHub, http.StatusNotFound, "Not Found")
}

// githubRepo resolves the repository of a request. Installation tokens only see repositories of their installation's
// account; others get 404, as on GitHub.
func (s *Server) githubRepo(w http.ResponseWriter, r *http.Request, who principal) (*repo, bool) {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	rp, err := s.store.findRepoLocked(GitHub, r.PathValue("owner"), "", r.PathValue("repo"))
	if err != nil {
		writeProviderError(w, GitHub, http.StatusNotFound, "Not Found")
		return nil, false
	}
	if who.installationID != 0 && !s.installationCoversLocked(who.installationID, rp.owner) {
		writeProviderError(w, GitHub, http.StatusNotFound, "Not Found")
		return nil, false
	}
	return rp, true
}

func (s *Server) installationCoversLocked(installationID int64, owner string) bool {
	for _, app := range s.store.apps {
		for _, inst := range app.installations {
			if inst.id == installationID {
				return strings.EqualFold(inst.account, owner)
			}
		}
	}
	return false
}

func (s *Server) githubPull(w http.ResponseWriter, r *http.Request, who principal) (*pullRequest, bool) {
	rp, ok := s.githubRepo(w, r, who)
	if !ok {
		return nil, false
	}
	number, err := strconv.Atoi(r.PathValue("number"))
	if err != nil {
		writeProviderError(w, GitHub, http.StatusNotFound, "Not Found")
		return nil, false
	}
	s.store.mu.Lock()
	pr, err := s.store.findPRLocked(rp, number)
	s.store.mu.Unlock()
	if err != nil {
		writeProviderError(w, GitHub, http.StatusNotFound, "Not Found")
		return nil, false
	}
	return pr, true
}

func githubValidationFailed(w http.ResponseWriter, resource, field, code, message string) {
	e := map[string]any{"resource": resource, "code": code}
	if field != "" {
		e["field"] = field
	}
	if message != "" {
		e["message"] = message
	}
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"message":           "Validation Failed",
		"errors":            []any{e},
		"documentation_url": "https://docs.github.com/enterprise-server@3.14/rest/pulls/pulls#create-a-pull-request",
		"status":            "422",
	})
}

func (s *Server) githubUserJSON(name string) map[string]any {
	userType := "User"
	if strings.HasSuffix(name, "[bot]") {
		userType = "Bot"
	}
	return map[string]any{"login": name, "id": 1, "type": userType, "site_admin": false}
}

func (s *Server) githubRepoJSON(r *repo, base string) map[string]any {
	return map[string]any{
		"id":             r.id,
		"node_id":        "R_" + strconv.FormatInt(r.id, 10),
		"name":           r.name,
		"full_name":      r.fullName(),
		"private":        true,
		"owner":          s.githubUserJSON(r.owner),
		"html_url":       base + "/" + r.fullName(),
		"url":            base + githubAPI + "/repos/" + r.fullName(),
		"clone_url":      base + "/" + r.fullName() + ".git",
		"default_branch": r.defaultBranch,
		"visibility":     "private",
		"created_at":     formatTime(r.createdAt, githubTimeFormat),
	}
}

func (s *Server) githubLabelJSON(r *repo, l *label, base string) map[string]any {
	return map[string]any{
		"id": l.id, "node_id": "LA_" + strconv.FormatInt(l.id, 10), "name": l.name, "color": l.color,
		"description": l.description, "default": false,
		"url": base + githubAPI + "/repos/" + r.fullName() + "/labels/" + l.name,
	}
}

// githubPullJSONLocked renders a pull request. The caller holds store.mu.
func (s *Server) githubPullJSONLocked(pr *pullRequest, base string) map[string]any {
	r := pr.repo
	now := s.now()
	var mergeable any
	mergeableState := "unknown"
	mergeCommit := any(nil)
	switch {
	case pr.state == prMerged:
		mergeCommit = pr.mergeCommitSHA
		mergeable, mergeableState = nil, "unknown"
	case pr.state == prClosed:
		mergeable = nil
	case !pr.mergeabilityKnown(now):
		mergeable = nil
	case pr.conflicts:
		mergeable, mergeableState = false, "dirty"
	default:
		mergeable, mergeableState = true, "clean"
		if missing := s.missingRequiredStatusesLocked(pr); len(missing) > 0 {
			mergeableState = "blocked"
		}
	}
	// Before merging, merge_commit_sha is the test merge commit; GitHub keeps it on a pull request closed unmerged.
	if pr.state != prMerged && pr.previewMergeSHA != "" {
		mergeCommit = pr.previewMergeSHA
	}
	labels := make([]map[string]any, 0, len(pr.labels))
	for _, name := range pr.labels {
		if l := r.labelLocked(name); l != nil {
			labels = append(labels, s.githubLabelJSON(r, l, base))
		}
	}
	state := "open"
	if pr.state != prOpen {
		state = "closed"
	}
	ref := func(branch, sha string) map[string]any {
		return map[string]any{
			"label": r.owner + ":" + branch, "ref": branch, "sha": sha,
			"user": s.githubUserJSON(r.owner), "repo": s.githubRepoJSON(r, base),
		}
	}
	apiURL := fmt.Sprintf("%s%s/repos/%s/pulls/%d", base, githubAPI, r.fullName(), pr.number)
	var mergedBy any
	if pr.state == prMerged {
		mergedBy = s.githubUserJSON(pr.author)
	}
	return map[string]any{
		"url":                 apiURL,
		"id":                  pr.globalID,
		"node_id":             "PR_" + strconv.FormatInt(pr.globalID, 10),
		"html_url":            fmt.Sprintf("%s/%s/pull/%d", base, r.fullName(), pr.number),
		"diff_url":            fmt.Sprintf("%s/%s/pull/%d.diff", base, r.fullName(), pr.number),
		"patch_url":           fmt.Sprintf("%s/%s/pull/%d.patch", base, r.fullName(), pr.number),
		"issue_url":           fmt.Sprintf("%s%s/repos/%s/issues/%d", base, githubAPI, r.fullName(), pr.number),
		"number":              pr.number,
		"state":               state,
		"locked":              false,
		"title":               pr.title,
		"user":                s.githubUserJSON(pr.author),
		"body":                pr.body,
		"labels":              labels,
		"created_at":          formatTime(pr.createdAt, githubTimeFormat),
		"updated_at":          formatTime(pr.updatedAt, githubTimeFormat),
		"closed_at":           formatTime(pr.closedAt, githubTimeFormat),
		"merged_at":           formatTime(pr.mergedAt, githubTimeFormat),
		"merge_commit_sha":    mergeCommit,
		"head":                ref(pr.source, pr.headSHA),
		"base":                ref(pr.target, pr.baseSHA),
		"draft":               false,
		"merged":              pr.state == prMerged,
		"mergeable":           mergeable,
		"rebaseable":          mergeable,
		"mergeable_state":     mergeableState,
		"merged_by":           mergedBy,
		"author_association":  "NONE",
		"auto_merge":          nil,
		"maintainer_can_edit": false,
	}
}

// missingRequiredStatusesLocked is missingRequiredStatuses for callers holding store.mu.
func (s *Server) missingRequiredStatusesLocked(pr *pullRequest) []string {
	var missing []string
	for _, name := range pr.repo.protection[pr.target] {
		cs := s.store.latestStatusLocked(pr.repo, pr.headSHA, name)
		if cs == nil || !isSuccessState(cs) {
			missing = append(missing, name)
		}
	}
	return missing
}

func (s *Server) githubGetRepo(w http.ResponseWriter, r *http.Request, who principal) {
	rp, ok := s.githubRepo(w, r, who)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.githubRepoJSON(rp, requestBaseURL(r)))
}

func (s *Server) githubListPulls(w http.ResponseWriter, r *http.Request, who principal) {
	rp, ok := s.githubRepo(w, r, who)
	if !ok {
		return
	}
	q := r.URL.Query()
	state := q.Get("state")
	if state == "" {
		state = "open"
	}
	headOwner, headBranch, hasOwner := strings.Cut(q.Get("head"), ":")
	if !hasOwner {
		headBranch, headOwner = headOwner, ""
	}
	base := q.Get("base")
	now := s.now()

	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	prs := s.store.prs[rp.id]
	matches := make([]*pullRequest, 0, len(prs))
	for _, pr := range slices.Backward(prs) { // newest first, GitHub's default sort
		if !pr.visibleInLists(now) {
			continue
		}
		if state != "all" && (state == "open") != (pr.state == prOpen) {
			continue
		}
		if q.Get("head") != "" && (pr.source != headBranch || (headOwner != "" && !strings.EqualFold(headOwner, rp.owner))) {
			continue
		}
		if base != "" && pr.target != base {
			continue
		}
		matches = append(matches, pr)
	}
	page, size := pageParams(r, "per_page", 30, 100)
	start, end := pageWindow(len(matches), page, size)
	out := make([]map[string]any, 0, end-start)
	for _, pr := range matches[start:end] {
		out = append(out, s.githubPullJSONLocked(pr, requestBaseURL(r)))
	}
	setLinkHeader(w, r, page, lastPage(len(matches), size))
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) githubCreatePull(w http.ResponseWriter, r *http.Request, who principal) {
	rp, ok := s.githubRepo(w, r, who)
	if !ok {
		return
	}
	var req struct {
		Title string `json:"title"`
		Head  string `json:"head"`
		Base  string `json:"base"`
		Body  string `json:"body"`
	}
	if !decodeJSON(w, r, GitHub, &req) {
		return
	}
	head := req.Head
	if owner, branch, ok := strings.Cut(req.Head, ":"); ok {
		if !strings.EqualFold(owner, rp.owner) {
			githubValidationFailed(w, "PullRequest", "head", "invalid", "")
			return
		}
		head = branch
	}
	if req.Title == "" {
		githubValidationFailed(w, "PullRequest", "title", "missing_field", "")
		return
	}
	ctx := r.Context()
	headSHA, err := s.git.branchSHA(ctx, rp.dir, head)
	if err != nil {
		githubValidationFailed(w, "PullRequest", "head", "invalid", "")
		return
	}
	baseSHA, err := s.git.branchSHA(ctx, rp.dir, req.Base)
	if err != nil {
		githubValidationFailed(w, "PullRequest", "base", "invalid", "")
		return
	}
	if contained, err := s.git.isAncestor(ctx, rp.dir, headSHA, baseSHA); err == nil && contained {
		githubValidationFailed(w, "PullRequest", "", "custom", fmt.Sprintf("No commits between %s and %s", req.Base, head))
		return
	}

	s.store.mu.Lock()
	if existing := s.store.openPRForBranchesLocked(rp, head, req.Base); existing != nil {
		s.store.mu.Unlock()
		githubValidationFailed(w, "PullRequest", "", "custom", fmt.Sprintf("A pull request already exists for %s:%s.", rp.owner, head))
		return
	}
	pr := s.store.newPRLocked(rp, req.Title, req.Body, head, req.Base, who.name, s.now())
	pr.hiddenUntil = pr.createdAt.Add(s.behavior.get(GitHub).ListLag.Duration)
	s.store.mu.Unlock()

	if err := s.refreshPR(ctx, pr); err != nil {
		writeProviderError(w, GitHub, http.StatusInternalServerError, err.Error())
		return
	}
	s.webhooks.notifyPullRequest(ctx, s, pr, prActionOpened)
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	w.Header().Set("Location", fmt.Sprintf("%s%s/repos/%s/pulls/%d", requestBaseURL(r), githubAPI, rp.fullName(), pr.number))
	writeJSON(w, http.StatusCreated, s.githubPullJSONLocked(pr, requestBaseURL(r)))
}

func (s *Server) githubGetPull(w http.ResponseWriter, r *http.Request, who principal) {
	pr, ok := s.githubPull(w, r, who)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.githubPullJSONLocked(pr, requestBaseURL(r)))
}

func (s *Server) githubEditPull(w http.ResponseWriter, r *http.Request, who principal) {
	pr, ok := s.githubPull(w, r, who)
	if !ok {
		return
	}
	var req struct {
		Title *string `json:"title"`
		Body  *string `json:"body"`
		State *string `json:"state"`
		Base  *string `json:"base"`
	}
	if !decodeJSON(w, r, GitHub, &req) {
		return
	}
	reopened := false
	s.store.mu.Lock()
	before := pr.state
	now := s.now()
	if req.Title != nil {
		pr.title = *req.Title
	}
	if req.Body != nil {
		pr.body = *req.Body
	}
	if req.Base != nil && *req.Base != "" {
		pr.target = *req.Base
	}
	if req.State != nil {
		switch {
		case *req.State == "closed" && pr.state == prOpen:
			pr.state = prClosed
			pr.closedAt = now
		case *req.State == "open" && pr.state == prClosed:
			pr.state = prOpen
			pr.closedAt = time.Time{}
			reopened = true
		case *req.State == "open" && pr.state == prMerged:
			s.store.mu.Unlock()
			githubValidationFailed(w, "PullRequest", "state", "invalid", "state cannot be changed. The pull request has been merged.")
			return
		case *req.State != "open" && *req.State != "closed":
			s.store.mu.Unlock()
			githubValidationFailed(w, "PullRequest", "state", "invalid", "")
			return
		default:
		}
	}
	pr.updatedAt = now
	after := pr.state
	s.store.mu.Unlock()
	s.notifyPullRequestTransition(r.Context(), pr, before, after)
	if reopened || req.Base != nil {
		if err := s.refreshPR(r.Context(), pr); err != nil {
			writeProviderError(w, GitHub, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.githubPullJSONLocked(pr, requestBaseURL(r)))
}

func (s *Server) githubIsMerged(w http.ResponseWriter, r *http.Request, who principal) {
	pr, ok := s.githubPull(w, r, who)
	if !ok {
		return
	}
	s.store.mu.Lock()
	merged := pr.state == prMerged
	s.store.mu.Unlock()
	if merged {
		writeNoContent(w, http.StatusNoContent)
		return
	}
	writeProviderError(w, GitHub, http.StatusNotFound, "Not Found")
}

func (s *Server) githubMergePull(w http.ResponseWriter, r *http.Request, who principal) {
	pr, ok := s.githubPull(w, r, who)
	if !ok {
		return
	}
	var req struct {
		CommitTitle   string `json:"commit_title"`
		CommitMessage string `json:"commit_message"`
		SHA           string `json:"sha"`
		MergeMethod   string `json:"merge_method"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, GitHub, &req) {
		return
	}
	switch req.MergeMethod {
	case "", "merge", "squash":
	case "rebase":
		// Rebase merges replay the head commits, which the server does not do; the hosted repositories have them
		// disabled, which GitHub reports with 405.
		writeProviderError(w, GitHub, http.StatusMethodNotAllowed, "Rebase merges are not allowed on this repository.")
		return
	default:
		githubValidationFailed(w, "PullRequest", "merge_method", "invalid", "")
		return
	}
	squash := req.MergeMethod == "squash"
	s.store.mu.Lock()
	state := pr.state
	message := defaultMergeMessage(pr, req.CommitMessage)
	if req.CommitTitle != "" {
		body := req.CommitMessage
		if body == "" {
			body = pr.title
		}
		message = req.CommitTitle + "\n\n" + body
	}
	// A squash commit's title defaults to "<title> (#N)" and its body to commit_message.
	squashMessage := defaultSquashMessage(pr, "")
	if req.CommitTitle != "" {
		squashMessage = req.CommitTitle
	}
	if req.CommitMessage != "" {
		squashMessage += "\n\n" + req.CommitMessage
	}
	missing := s.missingRequiredStatusesLocked(pr)
	s.store.mu.Unlock()
	if state != prOpen {
		writeProviderError(w, GitHub, http.StatusMethodNotAllowed, "Pull Request is not mergeable")
		return
	}
	if len(missing) == 1 {
		writeProviderError(w, GitHub, http.StatusMethodNotAllowed, fmt.Sprintf("Required status check %q is expected.", missing[0]))
		return
	}
	if len(missing) > 1 {
		writeProviderError(w, GitHub, http.StatusMethodNotAllowed, fmt.Sprintf("%d of %d required status checks are expected.", len(missing), len(missing)))
		return
	}
	sha, err := s.landMerge(r.Context(), mergeRequest{
		pr: pr, expectedHead: req.SHA, message: message, squashMessage: squashMessage, author: actor(GitHub, who.name), squash: squash,
	})
	switch {
	case errors.Is(err, errHeadMismatch):
		writeProviderError(w, GitHub, http.StatusConflict, "Head branch was modified. Review and try the merge again.")
	case errors.Is(err, errMergeConflict), errors.Is(err, errNotOpen), errors.Is(err, errBranchNotFound):
		writeProviderError(w, GitHub, http.StatusMethodNotAllowed, "Pull Request is not mergeable")
	case err != nil:
		writeProviderError(w, GitHub, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusOK, map[string]any{"sha": sha, "merged": true, "message": "Pull Request successfully merged"})
	}
}

func (s *Server) githubListLabels(w http.ResponseWriter, r *http.Request, who principal) {
	rp, ok := s.githubRepo(w, r, who)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	page, size := pageParams(r, "per_page", 30, 100)
	start, end := pageWindow(len(rp.labels), page, size)
	out := make([]map[string]any, 0, end-start)
	for _, l := range rp.labels[start:end] {
		out = append(out, s.githubLabelJSON(rp, l, requestBaseURL(r)))
	}
	setLinkHeader(w, r, page, lastPage(len(rp.labels), size))
	writeJSON(w, http.StatusOK, out)
}

var githubLabelColorRe = regexp.MustCompile(`^[0-9a-fA-F]{6}$`)

func (s *Server) githubCreateLabel(w http.ResponseWriter, r *http.Request, who principal) {
	rp, ok := s.githubRepo(w, r, who)
	if !ok {
		return
	}
	var req struct {
		Name        string `json:"name"`
		Color       string `json:"color"`
		Description string `json:"description"`
	}
	if !decodeJSON(w, r, GitHub, &req) {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if rp.labelLocked(req.Name) != nil {
		githubValidationFailed(w, "Label", "name", "already_exists", "")
		return
	}
	// The color is a hexadecimal code without the leading "#".
	if !githubLabelColorRe.MatchString(req.Color) {
		githubValidationFailed(w, "Label", "color", "invalid", "")
		return
	}
	l := s.store.ensureLabelLocked(rp, req.Name, req.Color)
	l.description = req.Description
	writeJSON(w, http.StatusCreated, s.githubLabelJSON(rp, l, requestBaseURL(r)))
}

func (s *Server) githubIssueLabelsLocked(pr *pullRequest, base string) []map[string]any {
	out := make([]map[string]any, 0, len(pr.labels))
	for _, name := range pr.labels {
		if l := pr.repo.labelLocked(name); l != nil {
			out = append(out, s.githubLabelJSON(pr.repo, l, base))
		}
	}
	return out
}

func (s *Server) githubListIssueLabels(w http.ResponseWriter, r *http.Request, who principal) {
	pr, ok := s.githubPull(w, r, who)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.githubIssueLabelsLocked(pr, requestBaseURL(r)))
}

func (s *Server) githubAddIssueLabels(w http.ResponseWriter, r *http.Request, who principal) {
	pr, ok := s.githubPull(w, r, who)
	if !ok {
		return
	}
	// The body is either a bare array of names or {"labels": [...]}.
	var raw any
	if !decodeJSON(w, r, GitHub, &raw) {
		return
	}
	var names []string
	switch v := raw.(type) {
	case []any:
		for _, n := range v {
			if name, ok := n.(string); ok {
				names = append(names, name)
			}
		}
	case map[string]any:
		if list, ok := v["labels"].([]any); ok {
			for _, n := range list {
				if name, ok := n.(string); ok {
					names = append(names, name)
				}
			}
		}
	default:
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	for _, name := range names {
		// GitHub creates labels that do not exist yet when they are added to an issue.
		l := s.store.ensureLabelLocked(pr.repo, name, "ededed")
		pr.labels = addLabel(pr.labels, l.name)
	}
	pr.updatedAt = s.now()
	writeJSON(w, http.StatusOK, s.githubIssueLabelsLocked(pr, requestBaseURL(r)))
}

func (s *Server) githubRemoveIssueLabel(w http.ResponseWriter, r *http.Request, who principal) {
	pr, ok := s.githubPull(w, r, who)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	labels, removed := removeLabel(pr.labels, r.PathValue("name"))
	if !removed {
		writeProviderError(w, GitHub, http.StatusNotFound, "Label does not exist")
		return
	}
	pr.labels = labels
	pr.updatedAt = s.now()
	writeJSON(w, http.StatusOK, s.githubIssueLabelsLocked(pr, requestBaseURL(r)))
}

type githubCheckRunRequest struct {
	Name        *string               `json:"name"`
	HeadSHA     *string               `json:"head_sha"`
	Status      *string               `json:"status"`
	Conclusion  *string               `json:"conclusion"`
	DetailsURL  *string               `json:"details_url"`
	ExternalID  *string               `json:"external_id"`
	StartedAt   *string               `json:"started_at"`
	CompletedAt *string               `json:"completed_at"`
	Output      *githubCheckRunOutput `json:"output"`
}

type githubCheckRunOutput struct {
	Title   *string `json:"title"`
	Summary *string `json:"summary"`
}

var (
	githubCheckStatuses    = []string{"queued", "in_progress", "completed", "waiting", "requested", "pending"}
	githubCheckConclusions = []string{"action_required", "cancelled", "failure", "neutral", "success", "skipped", "stale", "timed_out"}
)

// applyCheckRun validates and applies a check run create or update. The caller holds store.mu.
func applyCheckRun(cs *commitStatus, req githubCheckRunRequest, now time.Time) string {
	if req.Name != nil {
		cs.context = *req.Name
	}
	if req.DetailsURL != nil {
		// details_url must be an HTTP or HTTPS URL; GitHub rejects an empty string.
		u, err := url.Parse(*req.DetailsURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "Invalid request.\n\nFor 'properties/details_url', " + strconv.Quote(*req.DetailsURL) + " is not a valid URL."
		}
		cs.targetURL = *req.DetailsURL
	}
	if req.Output != nil {
		// output.title and output.summary are required whenever output is given.
		if req.Output.Title == nil || req.Output.Summary == nil {
			return "Invalid request.\n\nNo subschema in \"anyOf\" matched.\nFor 'properties/output', nil is not an object.\nFor 'anyOf/1', \"title\" wasn't supplied.\nFor 'anyOf/1', \"summary\" wasn't supplied."
		}
		cs.title = *req.Output.Title
		cs.summary = *req.Output.Summary
		cs.description = *req.Output.Title
	}
	if req.Status != nil {
		if !slices.Contains(githubCheckStatuses, *req.Status) {
			return "Invalid request.\n\n" + *req.Status + " is not a member of " + strings.Join(githubCheckStatuses, ", ") + "."
		}
		cs.status = *req.Status
	}
	if req.Conclusion != nil && *req.Conclusion != "" {
		if !slices.Contains(githubCheckConclusions, *req.Conclusion) {
			return "Invalid request.\n\n" + *req.Conclusion + " is not a member of " + strings.Join(githubCheckConclusions, ", ") + "."
		}
		// Providing a conclusion automatically completes the check run.
		cs.conclusion = *req.Conclusion
		cs.status = "completed"
	}
	if cs.status == "" {
		cs.status = "queued"
	}
	if cs.status == "completed" && cs.conclusion == "" {
		return "Invalid request.\n\nNo subschema in \"anyOf\" matched.\n\"conclusion\" wasn't supplied."
	}
	if cs.status != "completed" {
		cs.conclusion = ""
	}
	if req.StartedAt != nil {
		if t, err := time.Parse(time.RFC3339, *req.StartedAt); err == nil {
			cs.startedAt = t
		}
	}
	if cs.startedAt.IsZero() {
		cs.startedAt = now
	}
	if cs.status == "completed" {
		cs.completedAt = now
		if req.CompletedAt != nil {
			if t, err := time.Parse(time.RFC3339, *req.CompletedAt); err == nil {
				cs.completedAt = t
			}
		}
	}
	cs.state = cs.status
	cs.updatedAt = now
	return ""
}

func (s *Server) githubCheckRunJSONLocked(r *repo, cs *commitStatus, base string) map[string]any {
	var conclusion any
	if cs.conclusion != "" {
		conclusion = cs.conclusion
	}
	return map[string]any{
		"id":           cs.id,
		"node_id":      "CR_" + strconv.FormatInt(cs.id, 10),
		"head_sha":     cs.sha,
		"external_id":  "",
		"url":          fmt.Sprintf("%s%s/repos/%s/check-runs/%d", base, githubAPI, r.fullName(), cs.id),
		"html_url":     fmt.Sprintf("%s/%s/runs/%d", base, r.fullName(), cs.id),
		"details_url":  cs.targetURL,
		"status":       cs.status,
		"conclusion":   conclusion,
		"started_at":   formatTime(cs.startedAt, githubTimeFormat),
		"completed_at": formatTime(cs.completedAt, githubTimeFormat),
		"name":         cs.context,
		"output": map[string]any{
			"title": cs.title, "summary": cs.summary, "text": nil, "annotations_count": 0,
		},
		"check_suite":   map[string]any{"id": cs.id},
		"app":           map[string]any{"id": 1, "slug": "promoter-app", "name": "Promoter App"},
		"pull_requests": []any{},
	}
}

func (s *Server) githubCreateCheckRun(w http.ResponseWriter, r *http.Request, who principal) {
	rp, ok := s.githubRepo(w, r, who)
	if !ok {
		return
	}
	var req githubCheckRunRequest
	if !decodeJSON(w, r, GitHub, &req) {
		return
	}
	if req.Name == nil || *req.Name == "" || req.HeadSHA == nil {
		githubValidationFailed(w, "CheckRun", "name", "missing_field", "")
		return
	}
	sha, err := s.git.resolve(r.Context(), rp.dir, *req.HeadSHA)
	if err != nil || !strings.EqualFold(sha, *req.HeadSHA) {
		writeProviderError(w, GitHub, http.StatusUnprocessableEntity, "No commit found for SHA: "+*req.HeadSHA)
		return
	}
	now := s.now()
	cs := &commitStatus{sha: sha, createdAt: now}
	if problem := applyCheckRun(cs, req, now); problem != "" {
		writeProviderError(w, GitHub, http.StatusUnprocessableEntity, problem)
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	s.store.addStatusLocked(rp, cs)
	writeJSON(w, http.StatusCreated, s.githubCheckRunJSONLocked(rp, cs, requestBaseURL(r)))
}

func (s *Server) githubCheckRun(w http.ResponseWriter, r *http.Request, who principal) (*repo, *commitStatus, bool) {
	rp, ok := s.githubRepo(w, r, who)
	if !ok {
		return nil, nil, false
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeProviderError(w, GitHub, http.StatusNotFound, "Not Found")
		return nil, nil, false
	}
	s.store.mu.Lock()
	cs := s.store.findStatusByIDLocked(rp, id)
	s.store.mu.Unlock()
	if cs == nil || cs.status == "" {
		writeProviderError(w, GitHub, http.StatusNotFound, "Not Found")
		return nil, nil, false
	}
	return rp, cs, true
}

func (s *Server) githubGetCheckRun(w http.ResponseWriter, r *http.Request, who principal) {
	rp, cs, ok := s.githubCheckRun(w, r, who)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.githubCheckRunJSONLocked(rp, cs, requestBaseURL(r)))
}

func (s *Server) githubUpdateCheckRun(w http.ResponseWriter, r *http.Request, who principal) {
	rp, cs, ok := s.githubCheckRun(w, r, who)
	if !ok {
		return
	}
	var req githubCheckRunRequest
	if !decodeJSON(w, r, GitHub, &req) {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	updated := *cs
	if problem := applyCheckRun(&updated, req, s.now()); problem != "" {
		writeProviderError(w, GitHub, http.StatusUnprocessableEntity, problem)
		return
	}
	*cs = updated
	writeJSON(w, http.StatusOK, s.githubCheckRunJSONLocked(rp, cs, requestBaseURL(r)))
}

func (s *Server) githubListCheckRuns(w http.ResponseWriter, r *http.Request, who principal) {
	rp, ok := s.githubRepo(w, r, who)
	if !ok {
		return
	}
	sha, err := s.git.resolve(r.Context(), rp.dir, r.PathValue("ref"))
	if err != nil {
		writeProviderError(w, GitHub, http.StatusUnprocessableEntity, "No commit found for SHA: "+r.PathValue("ref"))
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	runs := []map[string]any{}
	for _, cs := range s.store.statusesLocked(rp, sha) {
		if cs.status != "" {
			runs = append(runs, s.githubCheckRunJSONLocked(rp, cs, requestBaseURL(r)))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"total_count": len(runs), "check_runs": runs})
}

func (s *Server) githubCreateStatus(w http.ResponseWriter, r *http.Request, who principal) {
	rp, ok := s.githubRepo(w, r, who)
	if !ok {
		return
	}
	var req struct {
		State       string `json:"state"`
		TargetURL   string `json:"target_url"`
		Description string `json:"description"`
		Context     string `json:"context"`
	}
	if !decodeJSON(w, r, GitHub, &req) {
		return
	}
	if !slices.Contains([]string{"error", "failure", "pending", "success"}, req.State) {
		githubValidationFailed(w, "Status", "state", "custom", "state is not included in the list")
		return
	}
	sha, err := s.git.resolve(r.Context(), rp.dir, r.PathValue("sha"))
	if err != nil {
		githubValidationFailed(w, "Status", "sha", "custom", "No commit found for SHA: "+r.PathValue("sha"))
		return
	}
	if req.Context == "" {
		req.Context = "default"
	}
	now := s.now()
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	cs := s.store.addStatusLocked(rp, &commitStatus{
		sha: sha, context: req.Context, state: req.State, description: req.Description, targetURL: req.TargetURL,
		createdAt: now, updatedAt: now,
	})
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": cs.id, "state": cs.state, "description": cs.description, "target_url": cs.targetURL,
		"context": cs.context, "created_at": formatTime(now, githubTimeFormat), "updated_at": formatTime(now, githubTimeFormat),
	})
}

func (s *Server) githubCombinedStatus(w http.ResponseWriter, r *http.Request, who principal) {
	rp, ok := s.githubRepo(w, r, who)
	if !ok {
		return
	}
	sha, err := s.git.resolve(r.Context(), rp.dir, r.PathValue("ref"))
	if err != nil {
		writeProviderError(w, GitHub, http.StatusNotFound, "No commit found for SHA: "+r.PathValue("ref"))
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	// The latest status per context, in the order the contexts first appeared.
	latest := []*commitStatus{}
	for _, cs := range s.store.statusesLocked(rp, sha) {
		if cs.status != "" {
			continue
		}
		if i := slices.IndexFunc(latest, func(l *commitStatus) bool { return l.context == cs.context }); i >= 0 {
			latest[i] = cs
		} else {
			latest = append(latest, cs)
		}
	}
	state := "success"
	statuses := make([]map[string]any, 0, len(latest))
	for _, cs := range latest {
		switch cs.state {
		case "failure", "error":
			state = "failure"
		case "pending":
			if state != "failure" {
				state = "pending"
			}
		default:
		}
		statuses = append(statuses, map[string]any{
			"id": cs.id, "state": cs.state, "context": cs.context, "description": cs.description, "target_url": cs.targetURL,
		})
	}
	if len(latest) == 0 {
		state = "pending"
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": state, "sha": sha, "total_count": len(statuses), "statuses": statuses})
}
