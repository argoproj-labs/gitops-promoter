package scmserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// bitbucketDCShortHash is the length of the displayId Bitbucket Data Center gives commits.
	bitbucketDCShortHash = 11
	// bitbucketDCDefaultLimit and bitbucketDCMaxLimit bound the page size of paged endpoints.
	bitbucketDCDefaultLimit = 25
	bitbucketDCMaxLimit     = 1000
	// bitbucketDCMaxField, bitbucketDCMaxURL and bitbucketDCMaxRef are the build status field limits; the parent key
	// and ref share the longest.
	bitbucketDCMaxField = 255
	bitbucketDCMaxURL   = 450
	bitbucketDCMaxRef   = 1024
	// bitbucketDCPushCommits is how many of the pushed commits a repo:refs_changed payload lists.
	bitbucketDCPushCommits = 5

	bitbucketDCNoFastForward = "no-ff"
	bitbucketDCSquash        = "squash"
)

var (
	// bitbucketDCLegacyBuildStates are the states the deprecated /rest/build-status endpoint accepts.
	bitbucketDCLegacyBuildStates = []string{"SUCCESSFUL", "FAILED", "INPROGRESS"}
	// bitbucketDCBuildStates are the states the repository builds endpoint accepts.
	bitbucketDCBuildStates = []string{"SUCCESSFUL", "FAILED", "INPROGRESS", "CANCELLED", "UNKNOWN"}
	// bitbucketDCKnownStrategies are Bitbucket Data Center's merge strategy IDs. The server enables only no-ff (the
	// default) and squash; requesting another one fails as requesting a disabled strategy does.
	bitbucketDCKnownStrategies = []string{"no-ff", "ff", "ff-only", "rebase-no-ff", "rebase-ff-only", "squash", "squash-ff-only"}

	fullSHA1 = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
)

func (s *Server) bitbucketDCRoutes(mux *http.ServeMux) {
	for _, version := range []string{"1.0", "latest"} {
		api := func(pattern string, h func(http.ResponseWriter, *http.Request, principal)) {
			method, path, _ := strings.Cut(pattern, " ")
			mux.HandleFunc(method+" /rest/api/"+version+"/projects/{project}/repos/{slug}"+path, s.apiHandler(BitbucketDataCenter, h))
		}
		api("GET ", s.bitbucketDCGetRepo)
		api("GET /pull-requests", s.bitbucketDCListPRs)
		api("POST /pull-requests", s.bitbucketDCCreatePR)
		api("GET /pull-requests/{id}", s.bitbucketDCGetPR)
		api("PUT /pull-requests/{id}", s.bitbucketDCUpdatePR)
		api("POST /pull-requests/{id}/decline", s.bitbucketDCDeclinePR)
		api("POST /pull-requests/{id}/reopen", s.bitbucketDCReopenPR)
		api("GET /pull-requests/{id}/merge", s.bitbucketDCCanMerge)
		api("POST /pull-requests/{id}/merge", s.bitbucketDCMergePR)
		api("GET /pull-requests/{id}/activities", s.bitbucketDCActivities)
		api("POST /commits/{commit}/builds", s.bitbucketDCSetBuild)
		api("GET /commits/{commit}/builds", s.bitbucketDCGetBuild)
		// The deprecated build status resource is keyed by commit alone, not by repository.
		mux.HandleFunc("POST /rest/build-status/"+version+"/commits/{commit}", s.apiHandler(BitbucketDataCenter, s.bitbucketDCSetLegacyBuild))
		mux.HandleFunc("GET /rest/build-status/"+version+"/commits/{commit}", s.apiHandler(BitbucketDataCenter, s.bitbucketDCListLegacyBuilds))
	}
}

// bitbucketDCError is one entry of Bitbucket Data Center's {"errors": [...]} error body. Extra carries the fields
// some exceptions add, such as the current version of an out-of-date pull request.
type bitbucketDCError struct {
	Extra         map[string]any
	Context       string
	Message       string
	ExceptionName string
}

func writeBitbucketDCError(w http.ResponseWriter, status int, errs ...bitbucketDCError) {
	out := make([]map[string]any, 0, len(errs))
	for _, e := range errs {
		entry := map[string]any{"context": nilIfEmpty(e.Context), "message": e.Message, "exceptionName": nilIfEmpty(e.ExceptionName)}
		for k, v := range e.Extra {
			entry[k] = v
		}
		out = append(out, entry)
	}
	writeJSON(w, status, map[string]any{"errors": out})
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func bitbucketDCValidation(w http.ResponseWriter, context, message string) {
	writeBitbucketDCError(w, http.StatusBadRequest, bitbucketDCError{Context: context, Message: message})
}

func bitbucketDCPullException(name string) string {
	return "com.atlassian.bitbucket.pull." + name
}

// stableID derives a positive numeric ID from a name, for entities the server does not store (projects, users).
func stableID(name string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToUpper(name)))
	return h.Sum32()%1_000_000 + 1
}

func epochMillis(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UnixMilli()
}

func displayID(sha string) string {
	if len(sha) > bitbucketDCShortHash {
		return sha[:bitbucketDCShortHash]
	}
	return sha
}

// bitbucketDCMergeSubject is the subject Bitbucket Data Center generates for a merge commit.
func bitbucketDCMergeSubject(pr *pullRequest) string {
	return fmt.Sprintf("Merge pull request #%d in %s from %s to %s", pr.number, pr.repo.fullName(), pr.source, pr.target)
}

func bitbucketDCUserJSON(name string) map[string]any {
	return map[string]any{
		"name": name, "emailAddress": name + "@users.noreply.bitbucketdatacenter.test", "active": true,
		"displayName": name, "id": stableID(name), "slug": strings.ToLower(name), "type": "NORMAL",
	}
}

func bitbucketDCRepoJSON(projectKey, slug string, id int64, base string) map[string]any {
	return map[string]any{
		"slug": slug, "id": id, "name": slug, "hierarchyId": fmt.Sprintf("%020x", id), "scmId": "git",
		"state": "AVAILABLE", "statusMessage": "Available", "forkable": true, "public": false, "archived": false,
		"project": map[string]any{"key": projectKey, "id": stableID(projectKey), "name": projectKey, "public": false, "type": "NORMAL"},
		"links": map[string]any{
			"clone": []any{map[string]any{"href": fmt.Sprintf("%s/scm/%s/%s.git", base, projectKey, slug), "name": "http"}},
			"self":  []any{map[string]any{"href": fmt.Sprintf("%s/projects/%s/repos/%s/browse", base, projectKey, slug)}},
		},
	}
}

func (s *Server) bitbucketDCPRJSONLocked(pr *pullRequest, base string) map[string]any {
	r := pr.repo
	ref := func(branch, sha string) map[string]any {
		return map[string]any{
			"id": "refs/heads/" + branch, "displayId": branch, "latestCommit": sha, "type": "BRANCH",
			"repository": bitbucketDCRepoJSON(r.owner, r.name, r.id, base),
		}
	}
	properties := map[string]any{"resolvedTaskCount": 0, "openTaskCount": 0}
	switch pr.state {
	case prOpen:
		outcome := "CLEAN"
		switch {
		case !pr.mergeabilityKnown(s.now()):
			outcome = "UNKNOWN"
		case pr.conflicts:
			outcome = "CONFLICTED"
		default:
		}
		properties["mergeResult"] = map[string]any{"outcome": outcome, "current": true}
	case prMerged:
		// Squash merges leave mergeCommit out; the MERGED activity still names the commit.
		if !pr.squashed {
			properties["mergeCommit"] = map[string]any{"id": pr.mergeCommitSHA, "displayId": displayID(pr.mergeCommitSHA)}
		}
	default:
	}
	body := map[string]any{
		"id":          pr.number,
		"version":     pr.version,
		"title":       pr.title,
		"state":       map[prState]string{prOpen: "OPEN", prClosed: "DECLINED", prMerged: "MERGED"}[pr.state],
		"open":        pr.state == prOpen,
		"closed":      pr.state != prOpen,
		"draft":       false,
		"createdDate": epochMillis(pr.createdAt),
		"updatedDate": epochMillis(pr.updatedAt),
		"fromRef":     ref(pr.source, pr.headSHA),
		"toRef":       ref(pr.target, pr.baseSHA),
		"locked":      false,
		"author": map[string]any{
			"user": bitbucketDCUserJSON(pr.author), "role": "AUTHOR", "approved": false, "status": "UNAPPROVED",
		},
		"reviewers":    []any{},
		"participants": []any{},
		"properties":   properties,
		"links": map[string]any{"self": []any{map[string]any{
			"href": fmt.Sprintf("%s/projects/%s/repos/%s/pull-requests/%d", base, r.owner, r.name, pr.number),
		}}},
	}
	// Bitbucket Data Center leaves description out when it is empty.
	if pr.body != "" {
		body["description"] = pr.body
	}
	if pr.state != prOpen {
		body["closedDate"] = epochMillis(pr.closedAt)
	}
	return body
}

// bitbucketDCPage reads the 0-based start and the limit of a paged request.
func bitbucketDCPage(r *http.Request) (start, limit int) {
	start, err := strconv.Atoi(r.URL.Query().Get("start"))
	if err != nil || start < 0 {
		start = 0
	}
	limit, err = strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = bitbucketDCDefaultLimit
	}
	return start, min(limit, bitbucketDCMaxLimit)
}

// bitbucketDCPaged writes values[start:start+limit] as a Bitbucket Data Center page.
func bitbucketDCPaged[T any](w http.ResponseWriter, r *http.Request, values []T, render func(T) any) {
	start, limit := bitbucketDCPage(r)
	from := min(start, len(values))
	to := min(from+limit, len(values))
	out := make([]any, 0, to-from)
	for _, v := range values[from:to] {
		out = append(out, render(v))
	}
	page := map[string]any{"size": len(out), "limit": limit, "start": start, "isLastPage": to >= len(values), "values": out}
	if to < len(values) {
		page["nextPageStart"] = to
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) bitbucketDCRepo(w http.ResponseWriter, r *http.Request) (*repo, bool) {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	project, slug := r.PathValue("project"), r.PathValue("slug")
	rp, err := s.store.findRepoLocked(BitbucketDataCenter, project, "", slug)
	if err != nil {
		writeBitbucketDCError(w, http.StatusNotFound, bitbucketDCError{
			Message:       fmt.Sprintf("Repository %s of project with key %s does not exist.", slug, project),
			ExceptionName: "com.atlassian.bitbucket.repository.NoSuchRepositoryException",
		})
		return nil, false
	}
	return rp, true
}

func (s *Server) bitbucketDCPR(w http.ResponseWriter, r *http.Request) (*pullRequest, bool) {
	rp, ok := s.bitbucketDCRepo(w, r)
	if !ok {
		return nil, false
	}
	notFound := func() {
		writeBitbucketDCError(w, http.StatusNotFound, bitbucketDCError{
			Message:       fmt.Sprintf("Pull request %s does not exist in %s/%s.", r.PathValue("id"), r.PathValue("project"), r.PathValue("slug")),
			ExceptionName: bitbucketDCPullException("NoSuchPullRequestException"),
		})
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		notFound()
		return nil, false
	}
	s.store.mu.Lock()
	pr, err := s.store.findPRLocked(rp, id)
	s.store.mu.Unlock()
	if err != nil {
		notFound()
		return nil, false
	}
	return pr, true
}

func (s *Server) bitbucketDCGetRepo(w http.ResponseWriter, r *http.Request, _ principal) {
	rp, ok := s.bitbucketDCRepo(w, r)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, bitbucketDCRepoJSON(rp.owner, rp.name, rp.id, requestBaseURL(r)))
}

func (s *Server) bitbucketDCGetPR(w http.ResponseWriter, r *http.Request, _ principal) {
	pr, ok := s.bitbucketDCPR(w, r)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.bitbucketDCPRJSONLocked(pr, requestBaseURL(r)))
}

// bitbucketDCListPRs lists pull requests. state defaults to OPEN, direction to INCOMING (at filters on the target
// branch; OUTGOING filters on the source branch) and order to NEWEST.
func (s *Server) bitbucketDCListPRs(w http.ResponseWriter, r *http.Request, _ principal) {
	rp, ok := s.bitbucketDCRepo(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	upper := func(key, fallback string) string {
		if v := strings.ToUpper(query.Get(key)); v != "" {
			return v
		}
		return fallback
	}
	state, direction, order := upper("state", "OPEN"), upper("direction", "INCOMING"), upper("order", "NEWEST")
	switch {
	case !slices.Contains([]string{"OPEN", "DECLINED", "MERGED", "ALL"}, state):
		bitbucketDCValidation(w, "state", fmt.Sprintf("%q is not a valid pull request state.", query.Get("state")))
		return
	case direction != "INCOMING" && direction != "OUTGOING":
		bitbucketDCValidation(w, "direction", fmt.Sprintf("%q is not a valid pull request direction.", query.Get("direction")))
		return
	case order != "NEWEST" && order != "OLDEST":
		bitbucketDCValidation(w, "order", fmt.Sprintf("%q is not a valid pull request order.", query.Get("order")))
		return
	default:
	}
	at, filterAt := "", query.Has("at")
	if filterAt {
		at = query.Get("at")
		if !strings.HasPrefix(at, "refs/") {
			at = "refs/heads/" + at
		}
	}

	now := s.now()
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	prs := slices.Clone(s.store.prs[rp.id])
	if order == "NEWEST" {
		slices.Reverse(prs)
	}
	matches := make([]*pullRequest, 0, len(prs))
	for _, pr := range prs {
		if !pr.visibleInLists(now) {
			continue
		}
		prStateName := map[prState]string{prOpen: "OPEN", prClosed: "DECLINED", prMerged: "MERGED"}[pr.state]
		if state != "ALL" && state != prStateName {
			continue
		}
		branch := pr.target
		if direction == "OUTGOING" {
			branch = pr.source
		}
		if filterAt && at != "refs/heads/"+branch {
			continue
		}
		matches = append(matches, pr)
	}
	base := requestBaseURL(r)
	bitbucketDCPaged(w, r, matches, func(pr *pullRequest) any { return s.bitbucketDCPRJSONLocked(pr, base) })
}

type bitbucketDCRefRequest struct {
	Repository *bitbucketDCRepoRef `json:"repository"`
	ID         string              `json:"id"`
}

type bitbucketDCRepoRef struct {
	Project *bitbucketDCProjectRef `json:"project"`
	Slug    string                 `json:"slug"`
}

type bitbucketDCProjectRef struct {
	Key string `json:"key"`
}

type bitbucketDCPRRequest struct {
	Version     *int                   `json:"version"`
	Title       *string                `json:"title"`
	Description *string                `json:"description"`
	FromRef     *bitbucketDCRefRequest `json:"fromRef"`
	ToRef       *bitbucketDCRefRequest `json:"toRef"`
}

// bitbucketDCResolveRef checks that ref names a branch of rp and returns the branch name. It writes the provider's error and
// returns false otherwise.
func (s *Server) bitbucketDCResolveRef(w http.ResponseWriter, r *http.Request, rp *repo, ref *bitbucketDCRefRequest, field string) (string, bool) {
	if ref == nil || ref.ID == "" {
		bitbucketDCValidation(w, field, "You must specify a "+field+".")
		return "", false
	}
	if ref.Repository != nil && ref.Repository.Slug != "" {
		key := ""
		if ref.Repository.Project != nil {
			key = ref.Repository.Project.Key
		}
		s.store.mu.Lock()
		other, err := s.store.findRepoLocked(BitbucketDataCenter, key, "", ref.Repository.Slug)
		s.store.mu.Unlock()
		switch {
		case err != nil:
			writeBitbucketDCError(w, http.StatusNotFound, bitbucketDCError{
				Message:       fmt.Sprintf("Repository %s of project with key %s does not exist.", ref.Repository.Slug, key),
				ExceptionName: "com.atlassian.bitbucket.repository.NoSuchRepositoryException",
			})
			return "", false
		case other != rp:
			// Pull requests between repositories need them to share a fork hierarchy, and the server creates none.
			writeBitbucketDCError(w, http.StatusConflict, bitbucketDCError{
				Message:       "Pull requests can only be opened between repositories in the same fork hierarchy.",
				ExceptionName: bitbucketDCPullException("InvalidPullRequestTargetException"),
			})
			return "", false
		default:
		}
	}
	branch := strings.TrimPrefix(ref.ID, "refs/heads/")
	if _, err := s.git.branchSHA(r.Context(), rp.dir, branch); err != nil {
		writeBitbucketDCError(w, http.StatusNotFound, bitbucketDCError{
			Message:       fmt.Sprintf("Repository %q of project with key %q has no branch %q.", rp.name, rp.owner, "refs/heads/"+branch),
			ExceptionName: "com.atlassian.bitbucket.commit.NoSuchCommitException",
		})
		return "", false
	}
	return branch, true
}

// bitbucketDCCheckTarget rejects a pull request from source into target that would be invalid, empty or a duplicate
// of another open pull request than self, as creating and retargeting do.
func (s *Server) bitbucketDCCheckTarget(w http.ResponseWriter, r *http.Request, rp *repo, self *pullRequest, source, target string) bool {
	if source == target {
		writeBitbucketDCError(w, http.StatusConflict, bitbucketDCError{
			Message:       "The source and target branches of a pull request must be different.",
			ExceptionName: bitbucketDCPullException("InvalidPullRequestTargetException"),
		})
		return false
	}
	s.store.mu.Lock()
	existing := s.store.openPRForBranchesLocked(rp, source, target)
	if existing != nil && existing != self {
		existingJSON := s.bitbucketDCPRJSONLocked(existing, requestBaseURL(r))
		s.store.mu.Unlock()
		writeBitbucketDCError(w, http.StatusConflict, bitbucketDCError{
			Message:       "Only one pull request may be open for a given source and target branch",
			ExceptionName: bitbucketDCPullException("DuplicatePullRequestException"),
			Extra:         map[string]any{"existingPullRequest": existingJSON},
		})
		return false
	}
	s.store.mu.Unlock()
	ctx := r.Context()
	head, headErr := s.git.branchSHA(ctx, rp.dir, source)
	base, baseErr := s.git.branchSHA(ctx, rp.dir, target)
	if headErr != nil || baseErr != nil {
		writeProviderError(w, BitbucketDataCenter, http.StatusInternalServerError, "resolve pull request branches")
		return false
	}
	if contained, err := s.git.isAncestor(ctx, rp.dir, head, base); err == nil && contained {
		writeBitbucketDCError(w, http.StatusConflict, bitbucketDCError{
			Message:       fmt.Sprintf("Branch %q is already up-to-date with all the commits on branch %q.", target, source),
			ExceptionName: bitbucketDCPullException("EmptyPullRequestException"),
		})
		return false
	}
	return true
}

// bitbucketDCCreatePR creates a pull request. Unlike Bitbucket Cloud, Bitbucket Data Center refuses a second open
// pull request for the same branches with 409.
func (s *Server) bitbucketDCCreatePR(w http.ResponseWriter, r *http.Request, who principal) {
	rp, ok := s.bitbucketDCRepo(w, r)
	if !ok {
		return
	}
	var req bitbucketDCPRRequest
	if !decodeJSON(w, r, BitbucketDataCenter, &req) {
		return
	}
	if req.Title == nil || strings.TrimSpace(*req.Title) == "" {
		bitbucketDCValidation(w, "title", "You must specify a title.")
		return
	}
	source, ok := s.bitbucketDCResolveRef(w, r, rp, req.FromRef, "fromRef")
	if !ok {
		return
	}
	target, ok := s.bitbucketDCResolveRef(w, r, rp, req.ToRef, "toRef")
	if !ok {
		return
	}
	if !s.bitbucketDCCheckTarget(w, r, rp, nil, source, target) {
		return
	}
	description := ""
	if req.Description != nil {
		description = *req.Description
	}
	s.store.mu.Lock()
	pr := s.store.newPRLocked(rp, *req.Title, description, source, target, who.name, s.now())
	pr.hiddenUntil = pr.createdAt.Add(s.behavior.get(BitbucketDataCenter).ListLag.Duration)
	s.store.mu.Unlock()
	ctx := r.Context()
	if err := s.refreshPR(ctx, pr); err != nil {
		writeProviderError(w, BitbucketDataCenter, http.StatusInternalServerError, err.Error())
		return
	}
	s.webhooks.notifyPullRequest(ctx, s, pr, prActionOpened)
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusCreated, s.bitbucketDCPRJSONLocked(pr, requestBaseURL(r)))
}

// bitbucketDCRequestedVersion is the version a request expects: the body's, else the "version" query parameter,
// else -1, which never matches.
func bitbucketDCRequestedVersion(r *http.Request, body *int) int {
	if body != nil {
		return *body
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("version")); err == nil {
		return v
	}
	return -1
}

// bitbucketDCOutOfDateLocked is the error for a request that names another version than the pull request's.
func (s *Server) bitbucketDCOutOfDateLocked(pr *pullRequest, expected int, base string) bitbucketDCError {
	return bitbucketDCError{
		Message:       "You are attempting to modify a pull request based on out-of-date information.",
		ExceptionName: bitbucketDCPullException("PullRequestOutOfDateException"),
		Extra:         map[string]any{"currentVersion": pr.version, "expectedVersion": expected, "pullRequest": s.bitbucketDCPRJSONLocked(pr, base)},
	}
}

func bitbucketDCIllegalState(pr *pullRequest, action string) bitbucketDCError {
	state := map[prState]string{prOpen: "open", prClosed: "declined", prMerged: "merged"}[pr.state]
	return bitbucketDCError{
		Message:       fmt.Sprintf("Pull request %d cannot be %s because it is %s.", pr.number, action, state),
		ExceptionName: bitbucketDCPullException("IllegalPullRequestStateException"),
	}
}

// bitbucketDCUpdatePR updates the title, description or target branch of a pull request. The request must name the
// pull request's current version.
func (s *Server) bitbucketDCUpdatePR(w http.ResponseWriter, r *http.Request, _ principal) {
	pr, ok := s.bitbucketDCPR(w, r)
	if !ok {
		return
	}
	var req bitbucketDCPRRequest
	if !decodeJSON(w, r, BitbucketDataCenter, &req) {
		return
	}
	if req.Version == nil {
		bitbucketDCValidation(w, "version", "You must specify the version of the pull request being updated.")
		return
	}
	if req.Title != nil && strings.TrimSpace(*req.Title) == "" {
		bitbucketDCValidation(w, "title", "You must specify a title.")
		return
	}
	base := requestBaseURL(r)
	s.store.mu.Lock()
	rp, state, source, target := pr.repo, pr.state, pr.source, pr.target
	if pr.version != *req.Version {
		e := s.bitbucketDCOutOfDateLocked(pr, *req.Version, base)
		s.store.mu.Unlock()
		writeBitbucketDCError(w, http.StatusConflict, e)
		return
	}
	s.store.mu.Unlock()

	newTarget := target
	if req.ToRef != nil && req.ToRef.ID != "" {
		var ok bool
		if newTarget, ok = s.bitbucketDCResolveRef(w, r, rp, req.ToRef, "toRef"); !ok {
			return
		}
	}
	retarget := newTarget != target
	if retarget {
		if state != prOpen {
			s.store.mu.Lock()
			e := bitbucketDCIllegalState(pr, "retargeted")
			s.store.mu.Unlock()
			writeBitbucketDCError(w, http.StatusConflict, e)
			return
		}
		if !s.bitbucketDCCheckTarget(w, r, rp, pr, source, newTarget) {
			return
		}
	}

	s.store.mu.Lock()
	changed := retarget
	if req.Title != nil && *req.Title != pr.title {
		pr.title, changed = *req.Title, true
	}
	if req.Description != nil && *req.Description != pr.body {
		pr.body, changed = *req.Description, true
	}
	if changed {
		pr.target = newTarget
		pr.version++
		pr.updatedAt = s.now()
	}
	s.store.mu.Unlock()
	if retarget {
		if err := s.refreshPR(r.Context(), pr); err != nil {
			writeProviderError(w, BitbucketDataCenter, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.bitbucketDCPRJSONLocked(pr, base))
}

// bitbucketDCVersionBody is the optional body of decline and reopen requests.
type bitbucketDCVersionBody struct {
	Version *int `json:"version"`
}

// bitbucketDCDeclinePR declines an open pull request. The version is checked before the state, so declining twice
// with the same version reports the second request as out of date.
func (s *Server) bitbucketDCDeclinePR(w http.ResponseWriter, r *http.Request, _ principal) {
	pr, ok := s.bitbucketDCPR(w, r)
	if !ok {
		return
	}
	var body bitbucketDCVersionBody
	if r.ContentLength > 0 && !decodeJSON(w, r, BitbucketDataCenter, &body) {
		return
	}
	expected := bitbucketDCRequestedVersion(r, body.Version)
	base := requestBaseURL(r)
	s.store.mu.Lock()
	if pr.version != expected {
		e := s.bitbucketDCOutOfDateLocked(pr, expected, base)
		s.store.mu.Unlock()
		writeBitbucketDCError(w, http.StatusConflict, e)
		return
	}
	if pr.state != prOpen {
		e := bitbucketDCIllegalState(pr, "declined")
		s.store.mu.Unlock()
		writeBitbucketDCError(w, http.StatusConflict, e)
		return
	}
	now := s.now()
	pr.state = prClosed
	pr.closedAt = now
	pr.updatedAt = now
	pr.version++
	s.store.mu.Unlock()
	s.webhooks.notifyPullRequest(r.Context(), s, pr, prActionClosed)
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.bitbucketDCPRJSONLocked(pr, base))
}

// bitbucketDCReopenPR reopens a declined pull request, unless another pull request is open for its branches.
func (s *Server) bitbucketDCReopenPR(w http.ResponseWriter, r *http.Request, _ principal) {
	pr, ok := s.bitbucketDCPR(w, r)
	if !ok {
		return
	}
	var body bitbucketDCVersionBody
	if r.ContentLength > 0 && !decodeJSON(w, r, BitbucketDataCenter, &body) {
		return
	}
	expected := bitbucketDCRequestedVersion(r, body.Version)
	base := requestBaseURL(r)
	s.store.mu.Lock()
	var failure *bitbucketDCError
	switch {
	case pr.version != expected:
		e := s.bitbucketDCOutOfDateLocked(pr, expected, base)
		failure = &e
	case pr.state != prClosed:
		e := bitbucketDCIllegalState(pr, "reopened")
		failure = &e
	case s.store.openPRForBranchesLocked(pr.repo, pr.source, pr.target) != nil:
		failure = &bitbucketDCError{
			Message:       "Only one pull request may be open for a given source and target branch",
			ExceptionName: bitbucketDCPullException("DuplicatePullRequestException"),
		}
	default:
		pr.state = prOpen
		pr.closedAt = time.Time{}
		pr.updatedAt = s.now()
		pr.version++
	}
	s.store.mu.Unlock()
	if failure != nil {
		writeBitbucketDCError(w, http.StatusConflict, *failure)
		return
	}
	ctx := r.Context()
	if err := s.refreshPR(ctx, pr); err != nil {
		writeProviderError(w, BitbucketDataCenter, http.StatusInternalServerError, err.Error())
		return
	}
	s.webhooks.notifyPullRequest(ctx, s, pr, prActionReopened)
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.bitbucketDCPRJSONLocked(pr, base))
}

// bitbucketDCMissingBuildsLocked returns the required build parent keys of pr's target branch that have no
// successful build on its head. The Required Builds merge check matches builds by their parent key, not their key,
// and reads only statuses posted to the repository builds resource, not those posted to the deprecated
// /rest/build-status resource. The latest build with the parent key decides.
func (s *Server) bitbucketDCMissingBuildsLocked(pr *pullRequest) []string {
	var missing []string
	for _, parent := range pr.repo.protection[pr.target] {
		var latest *commitStatus
		for _, cs := range s.store.statuses[pr.repo.id] {
			if cs.sha == pr.headSHA && cs.parent == parent && !cs.legacyBuild &&
				(latest == nil || !cs.updatedAt.Before(latest.updatedAt)) {
				latest = cs
			}
		}
		if latest == nil || latest.state != "SUCCESSFUL" {
			missing = append(missing, parent)
		}
	}
	return missing
}

// bitbucketDCVetoes returns the merge check vetoes for the given missing required builds.
func bitbucketDCVetoes(missing []string) []any {
	if len(missing) == 0 {
		return []any{}
	}
	return []any{map[string]any{
		"summaryMessage":  "Not all required builds are successful yet",
		"detailedMessage": "Required builds without a successful build on the latest commit: " + strings.Join(missing, ", "),
	}}
}

func (s *Server) bitbucketDCCanMerge(w http.ResponseWriter, r *http.Request, _ principal) {
	pr, ok := s.bitbucketDCPR(w, r)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if pr.state != prOpen {
		writeBitbucketDCError(w, http.StatusConflict, bitbucketDCIllegalState(pr, "merged"))
		return
	}
	missing := s.bitbucketDCMissingBuildsLocked(pr)
	outcome := "CLEAN"
	switch {
	case !pr.mergeabilityKnown(s.now()):
		outcome = "UNKNOWN"
	case pr.conflicts:
		outcome = "CONFLICTED"
	default:
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"canMerge": !pr.conflicts && len(missing) == 0, "conflicted": pr.conflicts, "outcome": outcome,
		"vetoes": bitbucketDCVetoes(missing),
	})
}

type bitbucketDCMergeBody struct {
	Version     *int            `json:"version"`
	Message     string          `json:"message"`
	StrategyID  string          `json:"strategyId"`
	AutoSubject json.RawMessage `json:"autoSubject"`
	AutoMerge   bool            `json:"autoMerge"`
}

// autoSubject reports whether the generated subject is prepended to the message. It defaults to true and accepts
// a boolean or the string "false".
func (b bitbucketDCMergeBody) autoSubject() bool {
	v := strings.Trim(strings.TrimSpace(string(b.AutoSubject)), `"`)
	return !strings.EqualFold(v, "false")
}

func bitbucketDCVetoed(conflicted bool, missing []string) bitbucketDCError {
	return bitbucketDCError{
		Message:       "Merging the pull request has been vetoed.",
		ExceptionName: bitbucketDCPullException("PullRequestMergeVetoedException"),
		Extra:         map[string]any{"conflicted": conflicted, "vetoes": bitbucketDCVetoes(missing)},
	}
}

// bitbucketDCMergePR merges a pull request synchronously. It must name the pull request's current version; there is
// no head SHA guard, so it merges whatever the source branch points to. Conflicts and failed merge checks are vetoes
// (409).
func (s *Server) bitbucketDCMergePR(w http.ResponseWriter, r *http.Request, who principal) {
	pr, ok := s.bitbucketDCPR(w, r)
	if !ok {
		return
	}
	var body bitbucketDCMergeBody
	if r.ContentLength > 0 && !decodeJSON(w, r, BitbucketDataCenter, &body) {
		return
	}
	if body.AutoMerge {
		writeBitbucketDCError(w, http.StatusForbidden, bitbucketDCError{Message: "Auto-merge is not enabled for this repository."})
		return
	}
	strategy := body.StrategyID
	if strategy == "" {
		strategy = bitbucketDCNoFastForward
	}
	if strategy != bitbucketDCNoFastForward && strategy != bitbucketDCSquash {
		message := fmt.Sprintf("%q is not a valid merge strategy.", strategy)
		if slices.Contains(bitbucketDCKnownStrategies, strategy) {
			message = fmt.Sprintf("The merge strategy %q is not enabled for this repository.", strategy)
		}
		bitbucketDCValidation(w, "strategyId", message)
		return
	}
	expected := bitbucketDCRequestedVersion(r, body.Version)
	base := requestBaseURL(r)

	s.store.mu.Lock()
	var failure *bitbucketDCError
	switch {
	case pr.version != expected:
		e := s.bitbucketDCOutOfDateLocked(pr, expected, base)
		failure = &e
	case pr.state != prOpen:
		e := bitbucketDCIllegalState(pr, "merged")
		failure = &e
	case pr.conflicts:
		e := bitbucketDCVetoed(true, nil)
		failure = &e
	default:
		if missing := s.bitbucketDCMissingBuildsLocked(pr); len(missing) > 0 {
			e := bitbucketDCVetoed(false, missing)
			failure = &e
		}
	}
	message := bitbucketDCMergeSubject(pr)
	switch {
	case body.Message != "" && body.autoSubject():
		message += "\n\n" + body.Message
	case body.Message != "":
		message = body.Message
	default:
	}
	s.store.mu.Unlock()
	if failure != nil {
		writeBitbucketDCError(w, http.StatusConflict, *failure)
		return
	}

	_, err := s.landMerge(r.Context(), mergeRequest{
		pr: pr, message: message, author: actor(BitbucketDataCenter, who.name), squash: strategy == bitbucketDCSquash,
	})
	switch {
	case errors.Is(err, errMergeConflict):
		writeBitbucketDCError(w, http.StatusConflict, bitbucketDCVetoed(true, nil))
		return
	case errors.Is(err, errNotOpen):
		s.store.mu.Lock()
		e := bitbucketDCIllegalState(pr, "merged")
		s.store.mu.Unlock()
		writeBitbucketDCError(w, http.StatusConflict, e)
		return
	case err != nil:
		writeProviderError(w, BitbucketDataCenter, http.StatusInternalServerError, err.Error())
		return
	default:
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.bitbucketDCPRJSONLocked(pr, base))
}

// bitbucketDCActivities lists a pull request's activities, newest first: OPENED, then DECLINED or MERGED once it
// is settled. A MERGED activity names the commit that landed on the target branch, even when the pull request's
// properties.mergeCommit is absent.
func (s *Server) bitbucketDCActivities(w http.ResponseWriter, r *http.Request, _ principal) {
	pr, ok := s.bitbucketDCPR(w, r)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	activity := func(id int64, at time.Time, action string) map[string]any {
		return map[string]any{"id": id, "createdDate": epochMillis(at), "user": bitbucketDCUserJSON(pr.author), "action": action}
	}
	activities := []map[string]any{activity(pr.globalID*10, pr.createdAt, "OPENED")}
	switch pr.state {
	case prMerged:
		merged := activity(pr.globalID*10+1, pr.mergedAt, "MERGED")
		merged["commit"] = map[string]any{"id": pr.mergeCommitSHA, "displayId": displayID(pr.mergeCommitSHA)}
		activities = append(activities, merged)
	case prClosed:
		activities = append(activities, activity(pr.globalID*10+1, pr.closedAt, "DECLINED"))
	default:
	}
	slices.Reverse(activities)
	bitbucketDCPaged(w, r, activities, func(a map[string]any) any { return a })
}

type bitbucketDCBuildRequest struct {
	State       string `json:"state"`
	Key         string `json:"key"`
	Parent      string `json:"parent"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Description string `json:"description"`
	Ref         string `json:"ref"`
}

// validate checks a build status the way both build status resources do, against the allowed states.
func (req bitbucketDCBuildRequest) validate(w http.ResponseWriter, states []string) bool {
	switch {
	case req.Key == "":
		bitbucketDCValidation(w, "key", "A build status key is required.")
	case req.URL == "":
		bitbucketDCValidation(w, "url", "A build status URL is required.")
	case !slices.Contains(states, req.State):
		bitbucketDCValidation(w, "state", fmt.Sprintf("%q is not a valid build state; use one of %s.", req.State, strings.Join(states, ", ")))
	case len(req.Key) > bitbucketDCMaxField || len(req.Name) > bitbucketDCMaxField || len(req.Description) > bitbucketDCMaxField:
		bitbucketDCValidation(w, "key", fmt.Sprintf("Build status fields are limited to %d characters.", bitbucketDCMaxField))
	case len(req.URL) > bitbucketDCMaxURL:
		bitbucketDCValidation(w, "url", fmt.Sprintf("The build status URL is limited to %d characters.", bitbucketDCMaxURL))
	case len(req.Parent) > bitbucketDCMaxRef:
		bitbucketDCValidation(w, "parent", fmt.Sprintf("The parent key is limited to %d characters.", bitbucketDCMaxRef))
	case req.Ref != "" && !strings.HasPrefix(req.Ref, "refs/"):
		bitbucketDCValidation(w, "ref", "The ref must be a fully qualified ref such as refs/heads/main.")
	default:
		return true
	}
	return false
}

// bitbucketDCStatusLocked returns the build status with key on sha in rp, posted to the deprecated resource when
// legacy is true or to the repository builds resource otherwise.
func (s *Server) bitbucketDCStatusLocked(rp *repo, sha, key string, legacy bool) *commitStatus {
	var latest *commitStatus
	for _, cs := range s.store.statuses[rp.id] {
		if cs.sha == sha && cs.context == key && cs.legacyBuild == legacy {
			latest = cs
		}
	}
	return latest
}

// bitbucketDCUpsertBuildLocked stores a build status, replacing the one with the same key, as Bitbucket Data Center does.
func (s *Server) bitbucketDCUpsertBuildLocked(rp *repo, sha string, req bitbucketDCBuildRequest, legacy bool, now time.Time) {
	cs := s.bitbucketDCStatusLocked(rp, sha, req.Key, legacy)
	if cs == nil {
		cs = s.store.addStatusLocked(rp, &commitStatus{sha: sha, context: req.Key, createdAt: now, legacyBuild: legacy})
	}
	cs.state = req.State
	cs.parent = req.Parent
	cs.name = req.Name
	cs.description = req.Description
	cs.targetURL = req.URL
	cs.updatedAt = now
}

// bitbucketDCBuildJSON renders a build status: the deprecated resource's shape for a legacy status, the repository
// builds resource's otherwise.
func bitbucketDCBuildJSON(cs *commitStatus, rp *repo) map[string]any {
	body := map[string]any{
		"key": cs.context, "state": cs.state, "name": cs.name, "url": cs.targetURL, "description": cs.description,
	}
	if cs.legacyBuild {
		body["dateAdded"] = epochMillis(cs.updatedAt)
		return body
	}
	if cs.parent != "" {
		body["parent"] = cs.parent
	}
	body["createdDate"] = epochMillis(cs.createdAt)
	body["updatedDate"] = epochMillis(cs.updatedAt)
	body["projectKey"] = rp.owner
	body["repositorySlug"] = rp.name
	return body
}

// bitbucketDCSetLegacyBuild stores a build status through the deprecated /rest/build-status resource. It is keyed by
// commit only, so the status appears in every repository holding the commit. It answers 204 even when no hosted
// repository has the commit.
func (s *Server) bitbucketDCSetLegacyBuild(w http.ResponseWriter, r *http.Request, _ principal) {
	sha := r.PathValue("commit")
	if !fullSHA1.MatchString(sha) {
		bitbucketDCValidation(w, "commitId", fmt.Sprintf("%q is not a full 40-character SHA-1 commit ID.", sha))
		return
	}
	sha = strings.ToLower(sha)
	var req bitbucketDCBuildRequest
	if !decodeJSON(w, r, BitbucketDataCenter, &req) || !req.validate(w, bitbucketDCLegacyBuildStates) {
		return
	}
	for _, rp := range s.bitbucketDCReposWithCommit(r, sha) {
		s.store.mu.Lock()
		s.bitbucketDCUpsertBuildLocked(rp, sha, req, true, s.now())
		s.store.mu.Unlock()
	}
	writeNoContent(w, http.StatusNoContent)
}

func (s *Server) bitbucketDCListLegacyBuilds(w http.ResponseWriter, r *http.Request, _ principal) {
	sha := strings.ToLower(r.PathValue("commit"))
	builds := []*commitStatus{}
	seen := map[string]bool{}
	for _, rp := range s.bitbucketDCReposWithCommit(r, sha) {
		s.store.mu.Lock()
		for _, cs := range slices.Backward(s.store.statusesLocked(rp, sha)) {
			if cs.legacyBuild && !seen[cs.context] {
				seen[cs.context] = true
				builds = append(builds, cs)
			}
		}
		s.store.mu.Unlock()
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	bitbucketDCPaged(w, r, builds, func(cs *commitStatus) any { return bitbucketDCBuildJSON(cs, nil) })
}

// bitbucketDCReposWithCommit returns the hosted Bitbucket Data Center repositories that contain sha.
func (s *Server) bitbucketDCReposWithCommit(r *http.Request, sha string) []*repo {
	s.store.mu.Lock()
	var candidates []*repo
	for _, rp := range s.store.sortedReposLocked() {
		if rp.provider == BitbucketDataCenter {
			candidates = append(candidates, rp)
		}
	}
	s.store.mu.Unlock()
	var out []*repo
	for _, rp := range candidates {
		if _, err := s.git.resolve(r.Context(), rp.dir, sha); err == nil {
			out = append(out, rp)
		}
	}
	return out
}

// bitbucketDCCommit resolves the {commit} path value in rp, writing Bitbucket Data Center's 404 when it is unknown.
func (s *Server) bitbucketDCCommit(w http.ResponseWriter, r *http.Request, rp *repo) (string, bool) {
	sha, err := s.git.resolve(r.Context(), rp.dir, r.PathValue("commit"))
	if err != nil {
		writeBitbucketDCError(w, http.StatusNotFound, bitbucketDCError{
			Message:       fmt.Sprintf("Commit %q does not exist in repository %q.", r.PathValue("commit"), rp.name),
			ExceptionName: "com.atlassian.bitbucket.commit.NoSuchCommitException",
		})
		return "", false
	}
	return sha, true
}

// bitbucketDCSetBuild stores a build status through the repository builds resource, which the Required Builds merge
// check reads.
func (s *Server) bitbucketDCSetBuild(w http.ResponseWriter, r *http.Request, _ principal) {
	rp, ok := s.bitbucketDCRepo(w, r)
	if !ok {
		return
	}
	var req bitbucketDCBuildRequest
	if !decodeJSON(w, r, BitbucketDataCenter, &req) || !req.validate(w, bitbucketDCBuildStates) {
		return
	}
	sha, ok := s.bitbucketDCCommit(w, r, rp)
	if !ok {
		return
	}
	s.store.mu.Lock()
	s.bitbucketDCUpsertBuildLocked(rp, sha, req, false, s.now())
	s.store.mu.Unlock()
	writeNoContent(w, http.StatusNoContent)
}

func (s *Server) bitbucketDCGetBuild(w http.ResponseWriter, r *http.Request, _ principal) {
	rp, ok := s.bitbucketDCRepo(w, r)
	if !ok {
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		bitbucketDCValidation(w, "key", "A build status key is required.")
		return
	}
	sha, ok := s.bitbucketDCCommit(w, r, rp)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	cs := s.bitbucketDCStatusLocked(rp, sha, key, false)
	if cs == nil {
		writeBitbucketDCError(w, http.StatusNotFound, bitbucketDCError{
			Message:       fmt.Sprintf("No build status with key %q exists for commit %s.", key, sha),
			ExceptionName: "com.atlassian.bitbucket.build.NoSuchBuildStatusException",
		})
		return
	}
	writeJSON(w, http.StatusOK, bitbucketDCBuildJSON(cs, rp))
}
