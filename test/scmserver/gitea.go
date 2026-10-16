package scmserver

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

const giteaAPI = "/api/v1"

// giteaVersions are the versions reported by /api/v1/version. Forgejo reports its own version with the Gitea
// version it is compatible with as build metadata.
var giteaVersions = map[Provider]string{
	Gitea:   "1.22.0",
	Forgejo: "9.0.0+gitea-1.22.0",
}

func (s *Server) giteaRoutes(p Provider, mux *http.ServeMux) {
	api := func(pattern string, h func(http.ResponseWriter, *http.Request, Provider, principal)) {
		method, path, _ := strings.Cut(pattern, " ")
		mux.HandleFunc(method+" "+giteaAPI+path, s.apiHandler(p, func(w http.ResponseWriter, r *http.Request, who principal) {
			h(w, r, p, who)
		}))
	}
	// The version endpoint is public, and the SDKs call it when a client is created.
	mux.HandleFunc("GET "+giteaAPI+"/version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"version": giteaVersions[p]})
	})
	api("GET /user", s.giteaCurrentUser)
	api("GET /repos/{owner}/{repo}", s.giteaGetRepo)
	api("GET /repos/{owner}/{repo}/pulls", s.giteaListPulls)
	api("POST /repos/{owner}/{repo}/pulls", s.giteaCreatePull)
	api("GET /repos/{owner}/{repo}/pulls/{index}", s.giteaGetPull)
	api("PATCH /repos/{owner}/{repo}/pulls/{index}", s.giteaEditPull)
	api("GET /repos/{owner}/{repo}/pulls/{index}/merge", s.giteaIsMerged)
	api("POST /repos/{owner}/{repo}/pulls/{index}/merge", s.giteaMergePull)
	api("GET /repos/{owner}/{repo}/labels", s.giteaListLabels)
	api("POST /repos/{owner}/{repo}/labels", s.giteaCreateLabel)
	api("GET /repos/{owner}/{repo}/issues/{index}/labels", s.giteaListIssueLabels)
	api("POST /repos/{owner}/{repo}/issues/{index}/labels", s.giteaAddIssueLabels)
	api("DELETE /repos/{owner}/{repo}/issues/{index}/labels/{id}", s.giteaRemoveIssueLabel)
	api("POST /repos/{owner}/{repo}/statuses/{sha}", s.giteaCreateStatus)
	api("GET /repos/{owner}/{repo}/statuses/{sha}", s.giteaListStatuses)
	api("GET /repos/{owner}/{repo}/commits/{ref}/statuses", s.giteaListStatuses)
}

func (s *Server) giteaRepo(w http.ResponseWriter, r *http.Request, p Provider) (*repo, bool) {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	rp, err := s.store.findRepoLocked(p, r.PathValue("owner"), "", r.PathValue("repo"))
	if err != nil {
		writeProviderError(w, p, http.StatusNotFound, "The target couldn't be found.")
		return nil, false
	}
	return rp, true
}

func (s *Server) giteaPull(w http.ResponseWriter, r *http.Request, p Provider) (*pullRequest, bool) {
	rp, ok := s.giteaRepo(w, r, p)
	if !ok {
		return nil, false
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil {
		writeProviderError(w, p, http.StatusNotFound, "The target couldn't be found.")
		return nil, false
	}
	s.store.mu.Lock()
	pr, err := s.store.findPRLocked(rp, index)
	s.store.mu.Unlock()
	if err != nil {
		writeProviderError(w, p, http.StatusNotFound, "The target couldn't be found.")
		return nil, false
	}
	return pr, true
}

func giteaUserJSON(name string) map[string]any {
	return map[string]any{"id": 1, "login": name, "username": name, "full_name": name, "email": name + "@noreply.example.com"}
}

func (s *Server) giteaCurrentUser(w http.ResponseWriter, _ *http.Request, _ Provider, who principal) {
	writeJSON(w, http.StatusOK, giteaUserJSON(who.name))
}

func (s *Server) giteaRepoJSON(r *repo, base string) map[string]any {
	return map[string]any{
		"id": r.id, "name": r.name, "full_name": r.fullName(), "private": true, "owner": giteaUserJSON(r.owner),
		"html_url": base + "/" + r.fullName(), "clone_url": base + "/" + r.fullName() + ".git",
		"default_branch": r.defaultBranch, "created_at": formatTime(r.createdAt, giteaTimeFormat),
		"allow_merge_commits": true, "default_merge_style": "merge",
	}
}

func (s *Server) giteaGetRepo(w http.ResponseWriter, r *http.Request, p Provider, _ principal) {
	rp, ok := s.giteaRepo(w, r, p)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.giteaRepoJSON(rp, requestBaseURL(r)))
}

func giteaLabelJSON(r *repo, l *label, base string) map[string]any {
	return map[string]any{
		"id": l.id, "name": l.name, "color": strings.TrimPrefix(l.color, "#"), "description": l.description,
		"exclusive": false, "is_archived": false,
		"url": fmt.Sprintf("%s%s/repos/%s/labels/%d", base, giteaAPI, r.fullName(), l.id),
	}
}

func (s *Server) giteaPullJSONLocked(pr *pullRequest, base string) map[string]any {
	r := pr.repo
	now := s.now()
	labels := make([]map[string]any, 0, len(pr.labels))
	for _, name := range pr.labels {
		if l := r.labelLocked(name); l != nil {
			labels = append(labels, giteaLabelJSON(r, l, base))
		}
	}
	state := "open"
	if pr.state != prOpen {
		state = "closed"
	}
	mergeable := pr.state == prOpen && pr.mergeabilityKnown(now) && !pr.conflicts
	var mergeCommit, mergedBy any
	if pr.state == prMerged {
		mergeCommit = pr.mergeCommitSHA
		mergedBy = giteaUserJSON(pr.author)
	}
	branch := func(name, sha string) map[string]any {
		return map[string]any{"label": name, "ref": name, "sha": sha, "repo_id": r.id, "repo": s.giteaRepoJSON(r, base)}
	}
	return map[string]any{
		"id":               pr.globalID,
		"url":              fmt.Sprintf("%s/%s/pulls/%d", base, r.fullName(), pr.number),
		"number":           pr.number,
		"user":             giteaUserJSON(pr.author),
		"title":            pr.title,
		"body":             pr.body,
		"labels":           labels,
		"state":            state,
		"draft":            false,
		"is_locked":        false,
		"comments":         0,
		"html_url":         fmt.Sprintf("%s/%s/pulls/%d", base, r.fullName(), pr.number),
		"diff_url":         fmt.Sprintf("%s/%s/pulls/%d.diff", base, r.fullName(), pr.number),
		"patch_url":        fmt.Sprintf("%s/%s/pulls/%d.patch", base, r.fullName(), pr.number),
		"mergeable":        mergeable,
		"merged":           pr.state == prMerged,
		"merged_at":        formatTime(pr.mergedAt, giteaTimeFormat),
		"merge_commit_sha": mergeCommit,
		"merged_by":        mergedBy,
		"base":             branch(pr.target, pr.baseSHA),
		"head":             branch(pr.source, pr.headSHA),
		"merge_base":       pr.baseSHA,
		"created_at":       formatTime(pr.createdAt, giteaTimeFormat),
		"updated_at":       formatTime(pr.updatedAt, giteaTimeFormat),
		"closed_at":        formatTime(pr.closedAt, giteaTimeFormat),
	}
}

func (s *Server) giteaListPulls(w http.ResponseWriter, r *http.Request, p Provider, _ principal) {
	rp, ok := s.giteaRepo(w, r, p)
	if !ok {
		return
	}
	state := r.URL.Query().Get("state")
	if state == "" {
		state = "open"
	}
	now := s.now()
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	var matches []*pullRequest
	prs := s.store.prs[rp.id]
	for i := len(prs) - 1; i >= 0; i-- { // newest first
		pr := prs[i]
		if !pr.visibleInLists(now) {
			continue
		}
		if state != "all" && (state == "open") != (pr.state == prOpen) {
			continue
		}
		matches = append(matches, pr)
	}
	// limit=0 (what the SDK sends by default) means the server default page size of 30; the maximum is 50.
	page, size := pageParams(r, "limit", 30, 50)
	start, end := pageWindow(len(matches), page, size)
	out := make([]map[string]any, 0, end-start)
	for _, pr := range matches[start:end] {
		out = append(out, s.giteaPullJSONLocked(pr, requestBaseURL(r)))
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(len(matches)))
	setLinkHeader(w, r, page, lastPage(len(matches), size))
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) giteaCreatePull(w http.ResponseWriter, r *http.Request, p Provider, who principal) {
	rp, ok := s.giteaRepo(w, r, p)
	if !ok {
		return
	}
	var req struct {
		Head   string  `json:"head"`
		Base   string  `json:"base"`
		Title  string  `json:"title"`
		Body   string  `json:"body"`
		Labels []int64 `json:"labels"`
	}
	if !decodeJSON(w, r, p, &req) {
		return
	}
	ctx := r.Context()
	headSHA, headErr := s.git.branchSHA(ctx, rp.dir, req.Head)
	baseSHA, baseErr := s.git.branchSHA(ctx, rp.dir, req.Base)
	if headErr != nil || baseErr != nil {
		writeProviderError(w, p, http.StatusNotFound, "The target couldn't be found.")
		return
	}
	if req.Head == req.Base {
		writeProviderError(w, p, http.StatusUnprocessableEntity, "Invalid PullRequest: There are no changes between the head and the base")
		return
	}
	if contained, err := s.git.isAncestor(ctx, rp.dir, headSHA, baseSHA); err == nil && contained {
		writeProviderError(w, p, http.StatusUnprocessableEntity, "There are no changes between the head and the base")
		return
	}
	s.store.mu.Lock()
	if existing := s.store.openPRForBranchesLocked(rp, req.Head, req.Base); existing != nil {
		s.store.mu.Unlock()
		writeProviderError(w, p, http.StatusConflict, fmt.Sprintf(
			"pull request already exists for these targets [id: %d, issue_id: %d, head_repo_id: %d, base_repo_id: %d, head_branch: %s, base_branch: %s]",
			existing.globalID, existing.globalID, rp.id, rp.id, req.Head, req.Base))
		return
	}
	pr := s.store.newPRLocked(rp, req.Title, req.Body, req.Head, req.Base, who.name, s.now())
	pr.hiddenUntil = pr.createdAt.Add(s.behavior.get(p).ListLag.Duration)
	for _, id := range req.Labels {
		if l := labelByID(rp, id); l != nil {
			pr.labels = addLabel(pr.labels, l.name)
		}
	}
	s.store.mu.Unlock()
	if err := s.refreshPR(ctx, pr); err != nil {
		writeProviderError(w, p, http.StatusInternalServerError, err.Error())
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusCreated, s.giteaPullJSONLocked(pr, requestBaseURL(r)))
}

func labelByID(r *repo, id int64) *label {
	for _, l := range r.labels {
		if l.id == id {
			return l
		}
	}
	return nil
}

func (s *Server) giteaGetPull(w http.ResponseWriter, r *http.Request, p Provider, _ principal) {
	pr, ok := s.giteaPull(w, r, p)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.giteaPullJSONLocked(pr, requestBaseURL(r)))
}

// giteaEditPull implements PATCH /repos/:owner/:repo/pulls/:index. Like Gitea, an empty title, empty base and zero
// milestone mean "unchanged", while any non-null body (even "") replaces the description. Gitea answers with 201.
func (s *Server) giteaEditPull(w http.ResponseWriter, r *http.Request, p Provider, _ principal) {
	pr, ok := s.giteaPull(w, r, p)
	if !ok {
		return
	}
	var req struct {
		Body   *string  `json:"body"`
		State  *string  `json:"state"`
		Labels *[]int64 `json:"labels"`
		Title  string   `json:"title"`
		Base   string   `json:"base"`
	}
	if !decodeJSON(w, r, p, &req) {
		return
	}
	reopened := false
	s.store.mu.Lock()
	now := s.now()
	if strings.TrimSpace(req.Title) != "" {
		pr.title = req.Title
	}
	if req.Body != nil {
		pr.body = *req.Body
	}
	if req.Base != "" && req.Base != pr.target {
		pr.target = req.Base
	}
	if req.Labels != nil {
		pr.labels = nil
		for _, id := range *req.Labels {
			if l := labelByID(pr.repo, id); l != nil {
				pr.labels = addLabel(pr.labels, l.name)
			}
		}
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
		case pr.state == prMerged:
			s.store.mu.Unlock()
			writeProviderError(w, p, http.StatusPreconditionFailed, "cannot change state of this pull request, it was already merged")
			return
		default:
		}
	}
	pr.updatedAt = now
	s.store.mu.Unlock()
	if reopened || req.Base != "" {
		if err := s.refreshPR(r.Context(), pr); err != nil {
			writeProviderError(w, p, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusCreated, s.giteaPullJSONLocked(pr, requestBaseURL(r)))
}

func (s *Server) giteaIsMerged(w http.ResponseWriter, r *http.Request, p Provider, _ principal) {
	pr, ok := s.giteaPull(w, r, p)
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
	writeNoContent(w, http.StatusNotFound)
}

// giteaMergePull implements POST /repos/:owner/:repo/pulls/:index/merge with Gitea's status codes: 405 when the pull
// request is closed, still being checked, conflicting or blocked by required statuses, and 409 when head_commit_id is
// stale. Both SDKs report these failures as success, exactly as against a real server.
func (s *Server) giteaMergePull(w http.ResponseWriter, r *http.Request, p Provider, who principal) {
	pr, ok := s.giteaPull(w, r, p)
	if !ok {
		return
	}
	var req struct {
		Do           string `json:"Do"`
		Title        string `json:"MergeTitleField"`
		Message      string `json:"MergeMessageField"`
		HeadCommitID string `json:"head_commit_id"`
		ForceMerge   bool   `json:"force_merge"`
	}
	if !decodeJSON(w, r, p, &req) {
		return
	}
	if req.Do != "" && req.Do != "merge" {
		writeProviderError(w, p, http.StatusMethodNotAllowed, "Invalid merge style: "+req.Do)
		return
	}
	s.store.mu.Lock()
	state := pr.state
	known := pr.mergeabilityKnown(s.now())
	conflicts := pr.conflicts
	missing := s.missingRequiredStatusesLocked(pr)
	message := defaultMergeMessage(pr, req.Message)
	if req.Title != "" {
		message = req.Title
		if req.Message != "" {
			message += "\n\n" + req.Message
		}
	}
	s.store.mu.Unlock()
	switch {
	case state == prMerged:
		writeProviderError(w, p, http.StatusMethodNotAllowed, "The PR is already merged")
		return
	case state != prOpen:
		writeProviderError(w, p, http.StatusMethodNotAllowed, "The PR is closed")
		return
	case !known, conflicts:
		writeProviderError(w, p, http.StatusMethodNotAllowed, "Please try again later")
		return
	case len(missing) > 0 && !req.ForceMerge:
		writeProviderError(w, p, http.StatusMethodNotAllowed, "Not all required status checks successful")
		return
	default:
	}
	_, err := s.landMerge(r.Context(), mergeRequest{pr: pr, expectedHead: req.HeadCommitID, message: message, author: actor(p, who.name)})
	switch {
	case errors.Is(err, errHeadMismatch):
		writeProviderError(w, p, http.StatusConflict, "head out of date")
	case errors.Is(err, errMergeConflict):
		writeProviderError(w, p, http.StatusConflict, "Merge conflicts")
	case errors.Is(err, errNotOpen), errors.Is(err, errBranchNotFound):
		writeProviderError(w, p, http.StatusMethodNotAllowed, "Please try again later")
	case err != nil:
		writeProviderError(w, p, http.StatusInternalServerError, err.Error())
	default:
		writeNoContent(w, http.StatusOK)
	}
}

func (s *Server) giteaListLabels(w http.ResponseWriter, r *http.Request, p Provider, _ principal) {
	rp, ok := s.giteaRepo(w, r, p)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	page, size := pageParams(r, "limit", 30, 50)
	start, end := pageWindow(len(rp.labels), page, size)
	out := make([]map[string]any, 0, end-start)
	for _, l := range rp.labels[start:end] {
		out = append(out, giteaLabelJSON(rp, l, requestBaseURL(r)))
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(len(rp.labels)))
	setLinkHeader(w, r, page, lastPage(len(rp.labels), size))
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) giteaCreateLabel(w http.ResponseWriter, r *http.Request, p Provider, _ principal) {
	rp, ok := s.giteaRepo(w, r, p)
	if !ok {
		return
	}
	var req struct {
		Name        string `json:"name"`
		Color       string `json:"color"`
		Description string `json:"description"`
	}
	if !decodeJSON(w, r, p, &req) {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if rp.labelLocked(req.Name) != nil {
		writeProviderError(w, p, http.StatusUnprocessableEntity, "label already exists")
		return
	}
	l := s.store.ensureLabelLocked(rp, req.Name, strings.TrimPrefix(req.Color, "#"))
	l.description = req.Description
	writeJSON(w, http.StatusCreated, giteaLabelJSON(rp, l, requestBaseURL(r)))
}

func (s *Server) giteaIssueLabelsLocked(pr *pullRequest, base string) []map[string]any {
	out := make([]map[string]any, 0, len(pr.labels))
	for _, name := range pr.labels {
		if l := pr.repo.labelLocked(name); l != nil {
			out = append(out, giteaLabelJSON(pr.repo, l, base))
		}
	}
	return out
}

func (s *Server) giteaListIssueLabels(w http.ResponseWriter, r *http.Request, p Provider, _ principal) {
	pr, ok := s.giteaPull(w, r, p)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.giteaIssueLabelsLocked(pr, requestBaseURL(r)))
}

func (s *Server) giteaAddIssueLabels(w http.ResponseWriter, r *http.Request, p Provider, _ principal) {
	pr, ok := s.giteaPull(w, r, p)
	if !ok {
		return
	}
	var req struct {
		Labels []any `json:"labels"`
	}
	if !decodeJSON(w, r, p, &req) {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	for _, ref := range req.Labels {
		var l *label
		switch v := ref.(type) {
		case float64:
			l = labelByID(pr.repo, int64(v))
		case string:
			l = pr.repo.labelLocked(v)
		default:
		}
		if l == nil {
			writeProviderError(w, p, http.StatusUnprocessableEntity, fmt.Sprintf("label does not exist [id: %v]", ref))
			return
		}
		pr.labels = addLabel(pr.labels, l.name)
	}
	pr.updatedAt = s.now()
	writeJSON(w, http.StatusOK, s.giteaIssueLabelsLocked(pr, requestBaseURL(r)))
}

func (s *Server) giteaRemoveIssueLabel(w http.ResponseWriter, r *http.Request, p Provider, _ principal) {
	pr, ok := s.giteaPull(w, r, p)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	var l *label
	if err == nil {
		l = labelByID(pr.repo, id)
	}
	if l == nil {
		writeProviderError(w, p, http.StatusUnprocessableEntity, "label does not exist [id: "+r.PathValue("id")+"]")
		return
	}
	pr.labels, _ = removeLabel(pr.labels, l.name)
	pr.updatedAt = s.now()
	writeNoContent(w, http.StatusNoContent)
}

var giteaStatusStates = []string{"pending", "success", "error", "failure", "warning"}

func (s *Server) giteaStatusJSON(r *repo, cs *commitStatus, base string) map[string]any {
	return map[string]any{
		"id": cs.id, "status": cs.state, "target_url": cs.targetURL, "description": cs.description,
		"url":     fmt.Sprintf("%s%s/repos/%s/statuses/%s", base, giteaAPI, r.fullName(), cs.sha),
		"context": cs.context, "creator": giteaUserJSON(r.provider.String() + "-user"),
		"created_at": formatTime(cs.createdAt, giteaTimeFormat), "updated_at": formatTime(cs.updatedAt, giteaTimeFormat),
	}
}

func (s *Server) giteaCreateStatus(w http.ResponseWriter, r *http.Request, p Provider, _ principal) {
	rp, ok := s.giteaRepo(w, r, p)
	if !ok {
		return
	}
	var req struct {
		State       string `json:"state"`
		TargetURL   string `json:"target_url"`
		Description string `json:"description"`
		Context     string `json:"context"`
	}
	if !decodeJSON(w, r, p, &req) {
		return
	}
	if !slices.Contains(giteaStatusStates, req.State) {
		writeProviderError(w, p, http.StatusUnprocessableEntity, "invalid state: "+req.State)
		return
	}
	sha, err := s.git.resolve(r.Context(), rp.dir, r.PathValue("sha"))
	if err != nil {
		writeProviderError(w, p, http.StatusNotFound, "The target couldn't be found.")
		return
	}
	now := s.now()
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	cs := s.store.addStatusLocked(rp, &commitStatus{
		sha: sha, context: req.Context, state: req.State, description: req.Description, targetURL: req.TargetURL,
		createdAt: now, updatedAt: now,
	})
	writeJSON(w, http.StatusCreated, s.giteaStatusJSON(rp, cs, requestBaseURL(r)))
}

func (s *Server) giteaListStatuses(w http.ResponseWriter, r *http.Request, p Provider, _ principal) {
	rp, ok := s.giteaRepo(w, r, p)
	if !ok {
		return
	}
	ref := r.PathValue("sha")
	if ref == "" {
		ref = r.PathValue("ref")
	}
	sha, err := s.git.resolve(r.Context(), rp.dir, ref)
	if err != nil {
		writeProviderError(w, p, http.StatusNotFound, "The target couldn't be found.")
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	statuses := s.store.statusesLocked(rp, sha)
	out := make([]map[string]any, 0, len(statuses))
	for i := len(statuses) - 1; i >= 0; i-- {
		out = append(out, s.giteaStatusJSON(rp, statuses[i], requestBaseURL(r)))
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(len(out)))
	writeJSON(w, http.StatusOK, out)
}
