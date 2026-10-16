package scmserver

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const gitlabAPI = "/api/v4"

func (s *Server) gitlabRoutes(mux *http.ServeMux) {
	api := func(pattern string, h func(http.ResponseWriter, *http.Request, principal)) {
		method, path, _ := strings.Cut(pattern, " ")
		mux.HandleFunc(method+" "+gitlabAPI+path, s.apiHandler(GitLab, h))
	}
	api("GET /user", s.gitlabCurrentUser)
	api("GET /projects/{id}", s.gitlabGetProject)
	api("GET /projects/{id}/merge_requests", s.gitlabListMRs)
	api("POST /projects/{id}/merge_requests", s.gitlabCreateMR)
	api("GET /projects/{id}/merge_requests/{iid}", s.gitlabGetMR)
	api("PUT /projects/{id}/merge_requests/{iid}", s.gitlabUpdateMR)
	api("PUT /projects/{id}/merge_requests/{iid}/merge", s.gitlabAcceptMR)
	api("GET /projects/{id}/labels", s.gitlabListLabels)
	api("POST /projects/{id}/labels", s.gitlabCreateLabel)
	api("POST /projects/{id}/statuses/{sha}", s.gitlabSetStatus)
	api("GET /projects/{id}/repository/commits/{sha}/statuses", s.gitlabListStatuses)
}

// gitlabNotFound writes GitLab's 404 body, e.g. {"message":"404 Project Not Found"}.
func gitlabNotFound(w http.ResponseWriter, what string) {
	writeProviderError(w, GitLab, http.StatusNotFound, "404 "+what+" Not Found")
}

// gitlabProject resolves a project by numeric ID or URL-encoded path.
func (s *Server) gitlabProject(w http.ResponseWriter, r *http.Request) (*repo, bool) {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	rp, err := s.gitlabProjectLocked(r.PathValue("id"))
	if err != nil {
		gitlabNotFound(w, "Project")
		return nil, false
	}
	return rp, true
}

// gitlabProjectLocked resolves a project by numeric ID or URL-encoded "namespace/name" path.
func (s *Server) gitlabProjectLocked(id string) (*repo, error) {
	if n, err := strconv.ParseInt(id, 10, 64); err == nil {
		return s.store.findRepoByIDLocked(GitLab, n)
	}
	path, err := url.PathUnescape(id)
	if err != nil {
		return nil, errNotFound
	}
	idx := strings.LastIndex(path, "/")
	if idx <= 0 {
		return nil, errNotFound
	}
	return s.store.findRepoLocked(GitLab, path[:idx], "", path[idx+1:])
}

func (s *Server) gitlabMR(w http.ResponseWriter, r *http.Request) (*pullRequest, bool) {
	rp, ok := s.gitlabProject(w, r)
	if !ok {
		return nil, false
	}
	iid, err := strconv.Atoi(r.PathValue("iid"))
	if err != nil {
		gitlabNotFound(w, "Merge Request")
		return nil, false
	}
	s.store.mu.Lock()
	pr, err := s.store.findPRLocked(rp, iid)
	s.store.mu.Unlock()
	if err != nil {
		gitlabNotFound(w, "Merge Request")
		return nil, false
	}
	return pr, true
}

func gitlabUserJSON(name string) map[string]any {
	return map[string]any{"id": 1, "username": name, "name": name, "state": "active", "web_url": ""}
}

func (s *Server) gitlabCurrentUser(w http.ResponseWriter, _ *http.Request, who principal) {
	writeJSON(w, http.StatusOK, gitlabUserJSON(who.name))
}

func (s *Server) gitlabProjectJSON(r *repo, base string) map[string]any {
	return map[string]any{
		"id":                  r.id,
		"name":                r.name,
		"path":                r.name,
		"path_with_namespace": r.fullName(),
		"name_with_namespace": strings.ReplaceAll(r.fullName(), "/", " / "),
		"default_branch":      r.defaultBranch,
		"web_url":             base + "/" + r.fullName(),
		"http_url_to_repo":    base + "/" + r.fullName() + ".git",
		"visibility":          "private",
		"merge_method":        "merge",
		"created_at":          formatTime(r.createdAt, gitlabTimeFormat),
		"namespace":           map[string]any{"full_path": r.owner, "path": r.owner, "kind": "group"},
	}
}

func (s *Server) gitlabGetProject(w http.ResponseWriter, r *http.Request, _ principal) {
	rp, ok := s.gitlabProject(w, r)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.gitlabProjectJSON(rp, requestBaseURL(r)))
}

// gitlabMergeStatusLocked returns merge_status and detailed_merge_status of an open merge request.
func (s *Server) gitlabMergeStatusLocked(pr *pullRequest) (mergeStatus, detailed string) {
	switch {
	case pr.state != prOpen:
		return "can_be_merged", "not_open"
	case !pr.mergeabilityKnown(s.now()):
		return "checking", "checking"
	case pr.conflicts:
		return "cannot_be_merged", "conflict"
	case len(s.missingRequiredStatusesLocked(pr)) > 0:
		return "can_be_merged", "ci_must_pass"
	default:
		return "can_be_merged", "mergeable"
	}
}

func (s *Server) gitlabMRJSONLocked(pr *pullRequest, base string) map[string]any {
	r := pr.repo
	state := map[prState]string{prOpen: "opened", prClosed: "closed", prMerged: "merged"}[pr.state]
	mergeStatus, detailed := s.gitlabMergeStatusLocked(pr)
	// A squash merge reports its commit as squash_commit_sha and no merge commit, as GitLab does for projects that
	// fast-forward squashed merge requests.
	var mergeCommit, squashCommit any
	if pr.state == prMerged {
		if pr.squashed {
			squashCommit = pr.mergeCommitSHA
		} else {
			mergeCommit = pr.mergeCommitSHA
		}
	}
	var mergedBy any
	if pr.state == prMerged {
		mergedBy = gitlabUserJSON(pr.author)
	}
	labels := slices.Clone(pr.labels)
	if labels == nil {
		labels = []string{}
	}
	return map[string]any{
		"id":                           pr.globalID,
		"iid":                          pr.number,
		"project_id":                   r.id,
		"title":                        pr.title,
		"description":                  pr.body,
		"state":                        state,
		"created_at":                   formatTime(pr.createdAt, gitlabTimeFormat),
		"updated_at":                   formatTime(pr.updatedAt, gitlabTimeFormat),
		"merged_at":                    formatTime(pr.mergedAt, gitlabTimeFormat),
		"closed_at":                    formatTime(closedNotMerged(pr), gitlabTimeFormat),
		"merged_by":                    mergedBy,
		"merge_user":                   mergedBy,
		"target_branch":                pr.target,
		"source_branch":                pr.source,
		"source_project_id":            r.id,
		"target_project_id":            r.id,
		"author":                       gitlabUserJSON(pr.author),
		"labels":                       labels,
		"draft":                        false,
		"work_in_progress":             false,
		"merge_when_pipeline_succeeds": false,
		"merge_status":                 mergeStatus,
		"detailed_merge_status":        detailed,
		"has_conflicts":                pr.conflicts,
		"sha":                          pr.headSHA,
		"merge_commit_sha":             mergeCommit,
		"squash_commit_sha":            squashCommit,
		"squash":                       pr.squashed,
		"should_remove_source_branch":  false,
		"force_remove_source_branch":   false,
		"web_url":                      fmt.Sprintf("%s/%s/-/merge_requests/%d", base, r.fullName(), pr.number),
		"references": map[string]any{
			"short": fmt.Sprintf("!%d", pr.number), "relative": fmt.Sprintf("!%d", pr.number),
			"full": fmt.Sprintf("%s!%d", r.fullName(), pr.number),
		},
		"diff_refs":                     map[string]any{"base_sha": pr.baseSHA, "head_sha": pr.headSHA, "start_sha": pr.baseSHA},
		"blocking_discussions_resolved": true,
		"user":                          map[string]any{"can_merge": true},
	}
}

// closedNotMerged returns the close time of a closed (not merged) pull request.
func closedNotMerged(pr *pullRequest) time.Time {
	if pr.state == prClosed {
		return pr.closedAt
	}
	return time.Time{}
}

// setGitLabPagination writes GitLab's offset pagination headers.
func setGitLabPagination(w http.ResponseWriter, r *http.Request, page, size, total int) {
	last := lastPage(total, size)
	h := w.Header()
	h.Set("X-Page", strconv.Itoa(page))
	h.Set("X-Per-Page", strconv.Itoa(size))
	h.Set("X-Total", strconv.Itoa(total))
	h.Set("X-Total-Pages", strconv.Itoa(last))
	next, prev := "", ""
	if page < last {
		next = strconv.Itoa(page + 1)
	}
	if page > 1 {
		prev = strconv.Itoa(page - 1)
	}
	h.Set("X-Next-Page", next)
	h.Set("X-Prev-Page", prev)
	setLinkHeader(w, r, page, last)
}

func (s *Server) gitlabListMRs(w http.ResponseWriter, r *http.Request, _ principal) {
	rp, ok := s.gitlabProject(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	state := q.Get("state")
	now := s.now()
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	prs := s.store.prs[rp.id]
	matches := make([]*pullRequest, 0, len(prs))
	for _, pr := range slices.Backward(prs) { // newest first: order_by=created_at, sort=desc
		if !pr.visibleInLists(now) {
			continue
		}
		native := map[prState]string{prOpen: "opened", prClosed: "closed", prMerged: "merged"}[pr.state]
		if state != "" && state != "all" && state != native {
			continue
		}
		if v := q.Get("source_branch"); v != "" && v != pr.source {
			continue
		}
		if v := q.Get("target_branch"); v != "" && v != pr.target {
			continue
		}
		if v := q.Get("labels"); v != "" && !hasAllLabels(pr.labels, strings.Split(v, ",")) {
			continue
		}
		matches = append(matches, pr)
	}
	page, size := pageParams(r, "per_page", 20, 100)
	start, end := pageWindow(len(matches), page, size)
	out := make([]map[string]any, 0, end-start)
	for _, pr := range matches[start:end] {
		out = append(out, s.gitlabMRJSONLocked(pr, requestBaseURL(r)))
	}
	setGitLabPagination(w, r, page, size, len(matches))
	writeJSON(w, http.StatusOK, out)
}

func hasAllLabels(have, want []string) bool {
	for _, w := range want {
		if !slices.ContainsFunc(have, func(h string) bool { return strings.EqualFold(h, strings.TrimSpace(w)) }) {
			return false
		}
	}
	return true
}

// gitlabLabelsParam accepts GitLab's comma-separated string or an array of label names.
func gitlabLabelsParam(v any) []string {
	switch labels := v.(type) {
	case string:
		var out []string
		for l := range strings.SplitSeq(labels, ",") {
			if l = strings.TrimSpace(l); l != "" {
				out = append(out, l)
			}
		}
		return out
	case []any:
		var out []string
		for _, l := range labels {
			if name, ok := l.(string); ok && name != "" {
				out = append(out, name)
			}
		}
		return out
	default:
		return nil
	}
}

func (s *Server) gitlabCreateMR(w http.ResponseWriter, r *http.Request, who principal) {
	rp, ok := s.gitlabProject(w, r)
	if !ok {
		return
	}
	var req struct {
		Labels       any    `json:"labels"`
		SourceBranch string `json:"source_branch"`
		TargetBranch string `json:"target_branch"`
		Title        string `json:"title"`
		Description  string `json:"description"`
	}
	if !decodeJSON(w, r, GitLab, &req) {
		return
	}
	if req.SourceBranch == "" || req.TargetBranch == "" || req.Title == "" {
		writeProviderError(w, GitLab, http.StatusBadRequest, "title, source_branch, target_branch are missing")
		return
	}
	ctx := r.Context()
	if _, err := s.git.branchSHA(ctx, rp.dir, req.SourceBranch); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"message": []string{fmt.Sprintf("Source branch %q does not exist", req.SourceBranch)}})
		return
	}
	if _, err := s.git.branchSHA(ctx, rp.dir, req.TargetBranch); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"message": []string{fmt.Sprintf("Target branch %q does not exist", req.TargetBranch)}})
		return
	}
	s.store.mu.Lock()
	if existing := s.store.openPRForBranchesLocked(rp, req.SourceBranch, req.TargetBranch); existing != nil {
		s.store.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]any{"message": []string{
			fmt.Sprintf("Another open merge request already exists for this source branch: !%d", existing.number),
		}})
		return
	}
	pr := s.store.newPRLocked(rp, req.Title, req.Description, req.SourceBranch, req.TargetBranch, who.name, s.now())
	pr.hiddenUntil = pr.createdAt.Add(s.behavior.get(GitLab).ListLag.Duration)
	for _, name := range gitlabLabelsParam(req.Labels) {
		l := s.store.ensureLabelLocked(rp, name, "#6699cc")
		pr.labels = addLabel(pr.labels, l.name)
	}
	s.store.mu.Unlock()
	if err := s.refreshPR(ctx, pr); err != nil {
		writeProviderError(w, GitLab, http.StatusInternalServerError, err.Error())
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusCreated, s.gitlabMRJSONLocked(pr, requestBaseURL(r)))
}

func (s *Server) gitlabGetMR(w http.ResponseWriter, r *http.Request, _ principal) {
	pr, ok := s.gitlabMR(w, r)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.gitlabMRJSONLocked(pr, requestBaseURL(r)))
}

func (s *Server) gitlabUpdateMR(w http.ResponseWriter, r *http.Request, _ principal) {
	pr, ok := s.gitlabMR(w, r)
	if !ok {
		return
	}
	var req struct {
		Title        *string `json:"title"`
		Description  *string `json:"description"`
		StateEvent   *string `json:"state_event"`
		TargetBranch *string `json:"target_branch"`
		Labels       any     `json:"labels"`
		AddLabels    any     `json:"add_labels"`
		RemoveLabels any     `json:"remove_labels"`
	}
	if !decodeJSON(w, r, GitLab, &req) {
		return
	}
	reopened := false
	s.store.mu.Lock()
	now := s.now()
	if req.Title != nil {
		pr.title = *req.Title
	}
	if req.Description != nil {
		pr.body = *req.Description
	}
	if req.TargetBranch != nil && *req.TargetBranch != "" {
		pr.target = *req.TargetBranch
	}
	if req.Labels != nil {
		pr.labels = nil
		for _, name := range gitlabLabelsParam(req.Labels) {
			pr.labels = addLabel(pr.labels, s.store.ensureLabelLocked(pr.repo, name, "#6699cc").name)
		}
	}
	for _, name := range gitlabLabelsParam(req.AddLabels) {
		// GitLab creates project labels that do not exist yet.
		pr.labels = addLabel(pr.labels, s.store.ensureLabelLocked(pr.repo, name, "#6699cc").name)
	}
	for _, name := range gitlabLabelsParam(req.RemoveLabels) {
		pr.labels, _ = removeLabel(pr.labels, name)
	}
	if req.StateEvent != nil {
		switch {
		case *req.StateEvent == "close" && pr.state == prOpen:
			pr.state = prClosed
			pr.closedAt = now
		case *req.StateEvent == "reopen" && pr.state == prClosed:
			pr.state = prOpen
			pr.closedAt = time.Time{}
			reopened = true
		default:
		}
	}
	pr.updatedAt = now
	s.store.mu.Unlock()
	if reopened || req.TargetBranch != nil {
		if err := s.refreshPR(r.Context(), pr); err != nil {
			writeProviderError(w, GitLab, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.gitlabMRJSONLocked(pr, requestBaseURL(r)))
}

func (s *Server) gitlabAcceptMR(w http.ResponseWriter, r *http.Request, who principal) {
	pr, ok := s.gitlabMR(w, r)
	if !ok {
		return
	}
	var req struct {
		MergeCommitMessage *string `json:"merge_commit_message"`
		SHA                string  `json:"sha"`
		Squash             bool    `json:"squash"`
		AutoMerge          bool    `json:"auto_merge"`
		MergeWhenPipeline  bool    `json:"merge_when_pipeline_succeeds"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, GitLab, &req) {
		return
	}
	s.store.mu.Lock()
	state := pr.state
	_, detailed := s.gitlabMergeStatusLocked(pr)
	head := pr.headSHA
	message := ""
	if req.MergeCommitMessage != nil {
		message = *req.MergeCommitMessage
	}
	message = defaultMergeMessage(pr, message)
	s.store.mu.Unlock()

	switch {
	case state != prOpen, detailed == "checking", detailed == "ci_must_pass":
		writeProviderError(w, GitLab, http.StatusMethodNotAllowed, "405 Method Not Allowed")
		return
	case detailed == "conflict":
		writeProviderError(w, GitLab, http.StatusNotAcceptable, "Branch cannot be merged")
		return
	case req.SHA != "" && !strings.EqualFold(req.SHA, head):
		writeProviderError(w, GitLab, http.StatusConflict, "SHA does not match HEAD of source branch: "+head)
		return
	case req.MergeCommitMessage != nil && strings.TrimSpace(*req.MergeCommitMessage) == "":
		writeProviderError(w, GitLab, http.StatusUnprocessableEntity, "Failed to merge: merge commit message can't be blank")
		return
	default:
	}
	_, err := s.landMerge(r.Context(), mergeRequest{pr: pr, expectedHead: req.SHA, message: message, author: actor(GitLab, who.name)})
	switch {
	case errors.Is(err, errHeadMismatch):
		writeProviderError(w, GitLab, http.StatusConflict, "SHA does not match HEAD of source branch")
		return
	case errors.Is(err, errMergeConflict), errors.Is(err, errBranchNotFound):
		writeProviderError(w, GitLab, http.StatusNotAcceptable, "Branch cannot be merged")
		return
	case errors.Is(err, errNotOpen):
		writeProviderError(w, GitLab, http.StatusMethodNotAllowed, "405 Method Not Allowed")
		return
	case err != nil:
		writeProviderError(w, GitLab, http.StatusInternalServerError, err.Error())
		return
	default:
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.gitlabMRJSONLocked(pr, requestBaseURL(r)))
}

func gitlabLabelJSON(l *label) map[string]any {
	return map[string]any{
		"id": l.id, "name": l.name, "color": l.color, "text_color": "#FFFFFF", "description": l.description,
		"open_issues_count": 0, "open_merge_requests_count": 0, "subscribed": false, "priority": nil,
		"is_project_label": true,
	}
}

func (s *Server) gitlabListLabels(w http.ResponseWriter, r *http.Request, _ principal) {
	rp, ok := s.gitlabProject(w, r)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	page, size := pageParams(r, "per_page", 20, 100)
	start, end := pageWindow(len(rp.labels), page, size)
	out := make([]map[string]any, 0, end-start)
	for _, l := range rp.labels[start:end] {
		out = append(out, gitlabLabelJSON(l))
	}
	setGitLabPagination(w, r, page, size, len(rp.labels))
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) gitlabCreateLabel(w http.ResponseWriter, r *http.Request, _ principal) {
	rp, ok := s.gitlabProject(w, r)
	if !ok {
		return
	}
	var req struct {
		Name        string `json:"name"`
		Color       string `json:"color"`
		Description string `json:"description"`
	}
	if !decodeJSON(w, r, GitLab, &req) {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if rp.labelLocked(req.Name) != nil {
		writeProviderError(w, GitLab, http.StatusConflict, "Label already exists")
		return
	}
	l := s.store.ensureLabelLocked(rp, req.Name, req.Color)
	l.description = req.Description
	writeJSON(w, http.StatusCreated, gitlabLabelJSON(l))
}

// gitlabStatusEvents maps the requested state to GitLab's commit status state machine event and its allowed source
// states (app/models/commit_status.rb).
var gitlabStatusEvents = map[string]struct {
	event string
	from  []string
}{
	"pending":  {"enqueue", []string{"created", "waiting_for_resource", "preparing", "skipped", "manual", "scheduled"}},
	"running":  {"run", []string{"pending"}},
	"success":  {"success", []string{"created", "waiting_for_resource", "preparing", "pending", "running"}},
	"failed":   {"drop", []string{"created", "waiting_for_resource", "preparing", "pending", "running", "manual", "scheduled"}},
	"canceled": {"cancel", []string{"created", "waiting_for_resource", "preparing", "pending", "running", "manual", "scheduled"}},
	"skipped":  {"skip", []string{"created", "waiting_for_resource", "preparing"}},
}

func (s *Server) gitlabStatusJSON(cs *commitStatus, ref string) map[string]any {
	var finished any
	if slices.Contains([]string{"success", "failed", "canceled", "skipped"}, cs.state) {
		finished = formatTime(cs.updatedAt, gitlabTimeFormat)
	}
	return map[string]any{
		"id": cs.id, "sha": cs.sha, "ref": ref, "status": cs.state, "name": cs.context,
		"target_url": cs.targetURL, "description": cs.description, "created_at": formatTime(cs.createdAt, gitlabTimeFormat),
		"started_at": formatTime(cs.startedAt, gitlabTimeFormat), "finished_at": finished, "allow_failure": false,
		"coverage": nil, "author": gitlabUserJSON("gitlab-user"),
	}
}

// gitlabSetStatus implements POST /projects/:id/statuses/:sha. Like GitLab, it reuses a pending or running status of
// the same name and applies the requested state as a state machine event, rejecting transitions the state machine
// does not allow with 400 "Cannot transition status via :<event> from :<state>".
func (s *Server) gitlabSetStatus(w http.ResponseWriter, r *http.Request, _ principal) {
	rp, ok := s.gitlabProject(w, r)
	if !ok {
		return
	}
	var req struct {
		State       string `json:"state"`
		Ref         string `json:"ref"`
		Name        string `json:"name"`
		Context     string `json:"context"`
		TargetURL   string `json:"target_url"`
		Description string `json:"description"`
	}
	if !decodeJSON(w, r, GitLab, &req) {
		return
	}
	transition, ok := gitlabStatusEvents[req.State]
	if !ok {
		writeProviderError(w, GitLab, http.StatusBadRequest, "state does not have a valid value")
		return
	}
	sha, err := s.git.resolve(r.Context(), rp.dir, r.PathValue("sha"))
	if err != nil {
		gitlabNotFound(w, "Commit")
		return
	}
	name := req.Name
	if name == "" {
		name = req.Context
	}
	if name == "" {
		name = "default"
	}
	now := s.now()
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	cs := s.store.latestStatusLocked(rp, sha, name)
	if cs == nil || (cs.state != "pending" && cs.state != "running") {
		cs = &commitStatus{sha: sha, context: name, state: "created", createdAt: now}
		defer func() {
			if cs.state != "created" {
				s.store.addStatusLocked(rp, cs)
			}
		}()
	}
	if req.State == "running" && cs.state != "pending" && slices.Contains(gitlabStatusEvents["pending"].from, cs.state) {
		cs.state = "pending" // the API enqueues before running
	}
	if !slices.Contains(transition.from, cs.state) {
		writeProviderError(w, GitLab, http.StatusBadRequest,
			fmt.Sprintf("Cannot transition status via :%s from :%s (Reason(s): Status cannot transition via \"%s\")", transition.event, cs.state, transition.event))
		return
	}
	cs.state = req.State
	cs.description = req.Description
	cs.targetURL = req.TargetURL
	cs.updatedAt = now
	if req.State == "running" {
		cs.startedAt = now
	}
	writeJSON(w, http.StatusCreated, s.gitlabStatusJSON(cs, req.Ref))
}

func (s *Server) gitlabListStatuses(w http.ResponseWriter, r *http.Request, _ principal) {
	rp, ok := s.gitlabProject(w, r)
	if !ok {
		return
	}
	sha, err := s.git.resolve(r.Context(), rp.dir, r.PathValue("sha"))
	if err != nil {
		gitlabNotFound(w, "Commit")
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	out := []map[string]any{}
	for _, cs := range s.store.statusesLocked(rp, sha) {
		out = append(out, s.gitlabStatusJSON(cs, ""))
	}
	writeJSON(w, http.StatusOK, out)
}
