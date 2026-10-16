package scmserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	bitbucketAPI = "/2.0"
	// bitbucketShortHash is the length of the abbreviated hashes Bitbucket Cloud returns in pull request payloads.
	bitbucketShortHash = 12
)

var bitbucketBuildStates = []string{"SUCCESSFUL", "FAILED", "INPROGRESS", "STOPPED"}

func (s *Server) bitbucketRoutes(mux *http.ServeMux) {
	api := func(pattern string, h func(http.ResponseWriter, *http.Request, principal)) {
		method, path, _ := strings.Cut(pattern, " ")
		mux.HandleFunc(method+" "+bitbucketAPI+path, s.apiHandler(BitbucketCloud, h))
	}
	api("GET /user", s.bitbucketCurrentUser)
	api("GET /repositories/{workspace}/{slug}", s.bitbucketGetRepo)
	api("GET /repositories/{workspace}/{slug}/pullrequests", s.bitbucketListPRs)
	api("GET /repositories/{workspace}/{slug}/pullrequests/", s.bitbucketListPRs)
	api("POST /repositories/{workspace}/{slug}/pullrequests", s.bitbucketCreatePR)
	api("POST /repositories/{workspace}/{slug}/pullrequests/", s.bitbucketCreatePR)
	api("GET /repositories/{workspace}/{slug}/pullrequests/{id}", s.bitbucketGetPR)
	api("PUT /repositories/{workspace}/{slug}/pullrequests/{id}", s.bitbucketUpdatePR)
	api("POST /repositories/{workspace}/{slug}/pullrequests/{id}/decline", s.bitbucketDeclinePR)
	api("POST /repositories/{workspace}/{slug}/pullrequests/{id}/merge", s.bitbucketMergePR)
	api("GET /repositories/{workspace}/{slug}/pullrequests/{id}/merge/task-status/{task}", s.bitbucketMergeTaskStatus)
	api("GET /repositories/{workspace}/{slug}/commit/{revision}", s.bitbucketGetCommit)
	api("POST /repositories/{workspace}/{slug}/commit/{revision}/statuses/build", s.bitbucketCreateBuildStatus)
	api("GET /repositories/{workspace}/{slug}/commit/{revision}/statuses/build/{key}", s.bitbucketGetBuildStatus)
	api("GET /repositories/{workspace}/{slug}/commit/{revision}/statuses", s.bitbucketListBuildStatuses)
}

func bitbucketError(w http.ResponseWriter, status int, message string, fields map[string][]string) {
	e := map[string]any{"message": message}
	if len(fields) > 0 {
		e["fields"] = fields
	}
	writeJSON(w, status, map[string]any{"type": "error", "error": e})
}

func (s *Server) bitbucketRepo(w http.ResponseWriter, r *http.Request) (*repo, bool) {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	rp, err := s.store.findRepoLocked(BitbucketCloud, r.PathValue("workspace"), "", r.PathValue("slug"))
	if err != nil {
		bitbucketError(w, http.StatusNotFound, "Repository "+r.PathValue("workspace")+"/"+r.PathValue("slug")+" not found", nil)
		return nil, false
	}
	return rp, true
}

func (s *Server) bitbucketPR(w http.ResponseWriter, r *http.Request) (*pullRequest, bool) {
	rp, ok := s.bitbucketRepo(w, r)
	if !ok {
		return nil, false
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		bitbucketError(w, http.StatusNotFound, "Pull request not found", nil)
		return nil, false
	}
	s.store.mu.Lock()
	pr, err := s.store.findPRLocked(rp, id)
	s.store.mu.Unlock()
	if err != nil {
		bitbucketError(w, http.StatusNotFound, "Pull request not found", nil)
		return nil, false
	}
	return pr, true
}

func bitbucketUserJSON(name string) map[string]any {
	return map[string]any{"type": "user", "display_name": name, "nickname": name, "uuid": "{00000000-0000-0000-0000-000000000001}", "account_id": "1"}
}

func (s *Server) bitbucketCurrentUser(w http.ResponseWriter, _ *http.Request, who principal) {
	writeJSON(w, http.StatusOK, bitbucketUserJSON(who.name))
}

func (s *Server) bitbucketRepoJSON(r *repo, base string) map[string]any {
	return map[string]any{
		"type": "repository", "full_name": r.fullName(), "name": r.name, "slug": r.name, "uuid": r.uuid,
		"is_private": true, "scm": "git", "mainbranch": map[string]any{"type": "branch", "name": r.defaultBranch},
		"created_on": formatTime(r.createdAt, bitbucketTimeFormat),
		"links": map[string]any{
			"html":  map[string]any{"href": base + "/" + r.fullName()},
			"clone": []any{map[string]any{"name": "https", "href": base + "/" + r.fullName() + ".git"}},
		},
		"workspace": map[string]any{"type": "workspace", "slug": r.owner},
	}
}

func (s *Server) bitbucketGetRepo(w http.ResponseWriter, r *http.Request, _ principal) {
	rp, ok := s.bitbucketRepo(w, r)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.bitbucketRepoJSON(rp, requestBaseURL(r)))
}

func shortHash(sha string) string {
	if len(sha) > bitbucketShortHash {
		return sha[:bitbucketShortHash]
	}
	return sha
}

func (s *Server) bitbucketPRJSONLocked(pr *pullRequest, base string) map[string]any {
	r := pr.repo
	state := map[prState]string{prOpen: "OPEN", prClosed: "DECLINED", prMerged: "MERGED"}[pr.state]
	endpoint := func(branch, sha string) map[string]any {
		return map[string]any{
			"branch":     map[string]any{"name": branch},
			"commit":     map[string]any{"type": "commit", "hash": shortHash(sha)},
			"repository": map[string]any{"type": "repository", "full_name": r.fullName(), "name": r.name, "uuid": r.uuid},
		}
	}
	var mergeCommit, closedBy any
	if pr.state == prMerged {
		mergeCommit = map[string]any{"type": "commit", "hash": shortHash(pr.mergeCommitSHA)}
	}
	if pr.state != prOpen {
		closedBy = bitbucketUserJSON(pr.author)
	}
	self := fmt.Sprintf("%s%s/repositories/%s/pullrequests/%d", base, bitbucketAPI, r.fullName(), pr.number)
	return map[string]any{
		"type":                "pullrequest",
		"id":                  pr.number,
		"title":               pr.title,
		"description":         pr.body,
		"summary":             map[string]any{"type": "rendered", "raw": pr.body, "markup": "markdown"},
		"state":               state,
		"author":              bitbucketUserJSON(pr.author),
		"source":              endpoint(pr.source, pr.headSHA),
		"destination":         endpoint(pr.target, pr.baseSHA),
		"merge_commit":        mergeCommit,
		"comment_count":       0,
		"task_count":          0,
		"close_source_branch": false,
		"closed_by":           closedBy,
		"reason":              "",
		"created_on":          formatTime(pr.createdAt, bitbucketTimeFormat),
		"updated_on":          formatTime(pr.updatedAt, bitbucketTimeFormat),
		"reviewers":           []any{},
		"participants":        []any{},
		"links": map[string]any{
			"self":     map[string]any{"href": self},
			"html":     map[string]any{"href": fmt.Sprintf("%s/%s/pull-requests/%d", base, r.fullName(), pr.number)},
			"merge":    map[string]any{"href": self + "/merge"},
			"decline":  map[string]any{"href": self + "/decline"},
			"statuses": map[string]any{"href": self + "/statuses"},
		},
	}
}

// bbqlTerm is one `field op "value"` comparison of a Bitbucket query.
type bbqlTerm struct {
	field string
	op    string
	value string
}

var bbqlTermRe = regexp.MustCompile(`^\s*([a-z_.]+)\s*(=|!=|~|!~)\s*(?:"((?:[^"\\]|\\.)*)"|([^\s"]+))\s*$`)

// parseBBQL parses the subset of the Bitbucket query language used against pull requests: comparisons joined by AND.
func parseBBQL(q string) ([]bbqlTerm, error) {
	if strings.TrimSpace(q) == "" {
		return nil, nil
	}
	parts := regexp.MustCompile(`(?i)\s+AND\s+`).Split(q, -1)
	terms := make([]bbqlTerm, 0, len(parts))
	for _, part := range parts {
		m := bbqlTermRe.FindStringSubmatch(part)
		if m == nil {
			return nil, fmt.Errorf("Invalid query syntax: %q", part) //nolint:staticcheck // Bitbucket's own capitalization
		}
		value := m[4]
		if m[4] == "" {
			unquoted, err := strconv.Unquote(`"` + m[3] + `"`)
			if err != nil {
				return nil, fmt.Errorf("invalid string literal %q", m[3])
			}
			value = unquoted
		}
		terms = append(terms, bbqlTerm{field: m[1], op: m[2], value: value})
	}
	return terms, nil
}

func bbqlField(pr *pullRequest, field string) (string, bool) {
	switch field {
	case "source.branch.name":
		return pr.source, true
	case "destination.branch.name":
		return pr.target, true
	case "state":
		return map[prState]string{prOpen: "OPEN", prClosed: "DECLINED", prMerged: "MERGED"}[pr.state], true
	case "id":
		return strconv.Itoa(pr.number), true
	case "title":
		return pr.title, true
	case "author.nickname", "author.display_name":
		return pr.author, true
	default:
		return "", false
	}
}

func bbqlMatches(pr *pullRequest, terms []bbqlTerm) (bool, error) {
	for _, t := range terms {
		got, ok := bbqlField(pr, t.field)
		if !ok {
			return false, fmt.Errorf("Invalid field name %q", t.field) //nolint:staticcheck // Bitbucket's own capitalization
		}
		var match bool
		switch t.op {
		case "=":
			match = got == t.value
		case "!=":
			match = got != t.value
		case "~":
			match = strings.Contains(strings.ToLower(got), strings.ToLower(t.value))
		case "!~":
			match = !strings.Contains(strings.ToLower(got), strings.ToLower(t.value))
		default:
		}
		if !match {
			return false, nil
		}
	}
	return true, nil
}

func (s *Server) bitbucketListPRs(w http.ResponseWriter, r *http.Request, _ principal) {
	rp, ok := s.bitbucketRepo(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	terms, err := parseBBQL(query.Get("q"))
	if err != nil {
		bitbucketError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	// Without a state parameter or a state term in q, only open pull requests are listed.
	states := query["state"]
	if len(states) == 0 && !slices.ContainsFunc(terms, func(t bbqlTerm) bool { return t.field == "state" }) {
		states = []string{"OPEN"}
	}
	now := s.now()
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	prs := s.store.prs[rp.id]
	matches := make([]*pullRequest, 0, len(prs))
	for _, pr := range slices.Backward(prs) { // newest first
		if !pr.visibleInLists(now) {
			continue
		}
		state, _ := bbqlField(pr, "state")
		if len(states) > 0 && !slices.ContainsFunc(states, func(s string) bool { return strings.EqualFold(s, state) }) {
			continue
		}
		ok, err := bbqlMatches(pr, terms)
		if err != nil {
			bitbucketError(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		if ok {
			matches = append(matches, pr)
		}
	}
	page, size := pageParams(r, "pagelen", 10, 50)
	start, end := pageWindow(len(matches), page, size)
	values := make([]map[string]any, 0, end-start)
	for _, pr := range matches[start:end] {
		values = append(values, s.bitbucketPRJSONLocked(pr, requestBaseURL(r)))
	}
	body := map[string]any{"pagelen": size, "size": len(matches), "page": page, "values": values}
	if page < lastPage(len(matches), size) {
		body["next"] = pageURL(r, page+1)
	}
	if page > 1 {
		body["previous"] = pageURL(r, page-1)
	}
	writeJSON(w, http.StatusOK, body)
}

type bitbucketPRRequest struct {
	Source      *bitbucketPREndpoint `json:"source"`
	Destination *bitbucketPREndpoint `json:"destination"`
	Title       *string              `json:"title"`
	Description *string              `json:"description"`
}

type bitbucketPREndpoint struct {
	Branch *bitbucketBranch `json:"branch"`
}

type bitbucketBranch struct {
	Name string `json:"name"`
}

func (req bitbucketPRRequest) branches() (source, destination string) {
	if req.Source != nil && req.Source.Branch != nil {
		source = req.Source.Branch.Name
	}
	if req.Destination != nil && req.Destination.Branch != nil {
		destination = req.Destination.Branch.Name
	}
	return source, destination
}

// bitbucketCreatePR creates a pull request. When an open pull request already exists for the same branches, the
// server updates and returns it rather than failing (behavior of Bitbucket Cloud as understood, not verified).
func (s *Server) bitbucketCreatePR(w http.ResponseWriter, r *http.Request, who principal) {
	rp, ok := s.bitbucketRepo(w, r)
	if !ok {
		return
	}
	var req bitbucketPRRequest
	if !decodeJSON(w, r, BitbucketCloud, &req) {
		return
	}
	source, destination := req.branches()
	if destination == "" {
		s.store.mu.Lock()
		destination = rp.defaultBranch
		s.store.mu.Unlock()
	}
	if req.Title == nil || *req.Title == "" {
		bitbucketError(w, http.StatusBadRequest, "title: This field is required.", map[string][]string{"title": {"This field is required."}})
		return
	}
	ctx := r.Context()
	headSHA, err := s.git.branchSHA(ctx, rp.dir, source)
	if err != nil {
		bitbucketError(w, http.StatusBadRequest, "source: Branch not found", map[string][]string{"source": {"Branch not found"}})
		return
	}
	baseSHA, err := s.git.branchSHA(ctx, rp.dir, destination)
	if err != nil {
		bitbucketError(w, http.StatusBadRequest, "destination: Branch not found", map[string][]string{"destination": {"Branch not found"}})
		return
	}
	if contained, err := s.git.isAncestor(ctx, rp.dir, headSHA, baseSHA); err == nil && contained {
		bitbucketError(w, http.StatusBadRequest, "There are no changes to be pulled", nil)
		return
	}
	description := ""
	if req.Description != nil {
		description = *req.Description
	}
	s.store.mu.Lock()
	pr := s.store.openPRForBranchesLocked(rp, source, destination)
	status := http.StatusCreated
	if pr != nil {
		pr.title = *req.Title
		pr.body = description
		pr.updatedAt = s.now()
		status = http.StatusOK
	} else {
		pr = s.store.newPRLocked(rp, *req.Title, description, source, destination, who.name, s.now())
		pr.hiddenUntil = pr.createdAt.Add(s.behavior.get(BitbucketCloud).ListLag.Duration)
	}
	s.store.mu.Unlock()
	if err := s.refreshPR(ctx, pr); err != nil {
		writeProviderError(w, BitbucketCloud, http.StatusInternalServerError, err.Error())
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	w.Header().Set("Location", fmt.Sprintf("%s%s/repositories/%s/pullrequests/%d", requestBaseURL(r), bitbucketAPI, rp.fullName(), pr.number))
	writeJSON(w, status, s.bitbucketPRJSONLocked(pr, requestBaseURL(r)))
}

func (s *Server) bitbucketGetPR(w http.ResponseWriter, r *http.Request, _ principal) {
	pr, ok := s.bitbucketPR(w, r)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.bitbucketPRJSONLocked(pr, requestBaseURL(r)))
}

func (s *Server) bitbucketUpdatePR(w http.ResponseWriter, r *http.Request, _ principal) {
	pr, ok := s.bitbucketPR(w, r)
	if !ok {
		return
	}
	var req bitbucketPRRequest
	if !decodeJSON(w, r, BitbucketCloud, &req) {
		return
	}
	_, destination := req.branches()
	s.store.mu.Lock()
	if pr.state != prOpen {
		s.store.mu.Unlock()
		bitbucketError(w, http.StatusBadRequest, "Only open pull requests can be updated.", nil)
		return
	}
	if req.Title != nil {
		if *req.Title == "" {
			s.store.mu.Unlock()
			bitbucketError(w, http.StatusBadRequest, "title: This field is required.", map[string][]string{"title": {"This field is required."}})
			return
		}
		pr.title = *req.Title
	}
	if req.Description != nil {
		pr.body = *req.Description
	}
	retarget := destination != "" && destination != pr.target
	if retarget {
		pr.target = destination
	}
	pr.updatedAt = s.now()
	s.store.mu.Unlock()
	if retarget {
		if err := s.refreshPR(r.Context(), pr); err != nil {
			writeProviderError(w, BitbucketCloud, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.bitbucketPRJSONLocked(pr, requestBaseURL(r)))
}

func (s *Server) bitbucketDeclinePR(w http.ResponseWriter, r *http.Request, _ principal) {
	pr, ok := s.bitbucketPR(w, r)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if pr.state != prOpen {
		bitbucketError(w, http.StatusBadRequest, "You can't decline a pull request that is not open.", nil)
		return
	}
	now := s.now()
	pr.state = prClosed
	pr.closedAt = now
	pr.updatedAt = now
	pr.asyncMergePending = false
	writeJSON(w, http.StatusOK, s.bitbucketPRJSONLocked(pr, requestBaseURL(r)))
}

func (s *Server) bitbucketTaskURL(r *http.Request, pr *pullRequest, task string) string {
	return fmt.Sprintf("%s%s/repositories/%s/pullrequests/%d/merge/task-status/%s",
		requestBaseURL(r), bitbucketAPI, pr.repo.fullName(), pr.number, task)
}

// bitbucketMergePR merges a pull request. Bitbucket Cloud does not take an expected head SHA, so the merge lands
// whatever the source branch points to. With AsyncMergeDelay set it answers 202 and completes in the background.
func (s *Server) bitbucketMergePR(w http.ResponseWriter, r *http.Request, who principal) {
	pr, ok := s.bitbucketPR(w, r)
	if !ok {
		return
	}
	var req struct {
		Message       string `json:"message"`
		MergeStrategy string `json:"merge_strategy"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, BitbucketCloud, &req) {
		return
	}
	if req.MergeStrategy != "" && req.MergeStrategy != "merge_commit" {
		bitbucketError(w, http.StatusBadRequest, "merge_strategy: "+req.MergeStrategy+" is not allowed for this repository", nil)
		return
	}
	behavior := s.behavior.get(BitbucketCloud)
	s.store.mu.Lock()
	state, conflicts, pending := pr.state, pr.conflicts, pr.asyncMergePending
	missing := s.missingRequiredStatusesLocked(pr)
	message := defaultMergeMessage(pr, req.Message)
	s.store.mu.Unlock()
	switch {
	case state == prMerged:
		bitbucketError(w, http.StatusBadRequest, "This pull request is already merged.", nil)
		return
	case state != prOpen:
		bitbucketError(w, http.StatusBadRequest, "You can't merge a declined pull request.", nil)
		return
	case conflicts:
		bitbucketError(w, http.StatusBadRequest, "You can't merge until you resolve all merge conflicts.", nil)
		return
	case len(missing) > 0:
		bitbucketError(w, http.StatusBadRequest, fmt.Sprintf("You can't merge until you have %d successful builds.", len(missing)), nil)
		return
	case pending:
		bitbucketError(w, http.StatusConflict, "A merge is already in progress for this pull request.", nil)
		return
	default:
	}

	if behavior.AsyncMergeDelay.Duration > 0 {
		task := newGUID()
		s.store.mu.Lock()
		pr.asyncMergePending = true
		pr.asyncMergeTaskID = task
		pr.asyncMergeMessage = message
		pr.asyncMergeDueAt = s.now().Add(behavior.AsyncMergeDelay.Duration)
		pr.author = who.name
		s.store.mu.Unlock()
		taskURL := s.bitbucketTaskURL(r, pr, task)
		w.Header().Set("Location", taskURL)
		writeJSON(w, http.StatusAccepted, map[string]any{
			"task_status": "PENDING",
			"links":       map[string]any{"self": map[string]any{"href": taskURL}},
		})
		return
	}

	_, err := s.landMerge(r.Context(), mergeRequest{pr: pr, message: message, author: actor(BitbucketCloud, who.name)})
	switch {
	case errors.Is(err, errMergeConflict):
		bitbucketError(w, http.StatusBadRequest, "You can't merge until you resolve all merge conflicts.", nil)
		return
	case errors.Is(err, errNotOpen), errors.Is(err, errBranchNotFound):
		bitbucketError(w, http.StatusBadRequest, err.Error(), nil)
		return
	case err != nil:
		writeProviderError(w, BitbucketCloud, http.StatusInternalServerError, err.Error())
		return
	default:
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.bitbucketPRJSONLocked(pr, requestBaseURL(r)))
}

func (s *Server) bitbucketMergeTaskStatus(w http.ResponseWriter, r *http.Request, _ principal) {
	pr, ok := s.bitbucketPR(w, r)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if pr.asyncMergeTaskID != r.PathValue("task") {
		bitbucketError(w, http.StatusNotFound, "Task not found", nil)
		return
	}
	if pr.asyncMergePending {
		writeJSON(w, http.StatusOK, map[string]any{"task_status": "PENDING"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task_status": "SUCCESS", "merge_result": s.bitbucketPRJSONLocked(pr, requestBaseURL(r))})
}

// processBitbucketAsyncMerges completes asynchronous merges whose delay elapsed.
func (s *Server) processBitbucketAsyncMerges(ctx context.Context) {
	now := s.now()
	s.store.mu.Lock()
	var due []*pullRequest
	for _, r := range s.store.repos {
		if r.provider != BitbucketCloud {
			continue
		}
		for _, pr := range s.store.prs[r.id] {
			if pr.asyncMergePending && !now.Before(pr.asyncMergeDueAt) {
				due = append(due, pr)
			}
		}
	}
	s.store.mu.Unlock()
	for _, pr := range due {
		s.store.mu.Lock()
		message, author := pr.asyncMergeMessage, pr.author
		s.store.mu.Unlock()
		if _, err := s.landMerge(ctx, mergeRequest{pr: pr, message: message, author: actor(BitbucketCloud, author)}); err != nil {
			s.log.Error("complete Bitbucket asynchronous merge", "repo", pr.repo.fullName(), "id", pr.number, "error", err)
			s.store.mu.Lock()
			pr.asyncMergePending = false
			s.store.mu.Unlock()
		}
	}
}

func (s *Server) bitbucketGetCommit(w http.ResponseWriter, r *http.Request, _ principal) {
	rp, ok := s.bitbucketRepo(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	sha, err := s.git.resolve(ctx, rp.dir, r.PathValue("revision"))
	if err != nil {
		bitbucketError(w, http.StatusNotFound, "Commit not found", nil)
		return
	}
	commits, err := s.git.log(ctx, rp.dir, "", sha, 1)
	if err != nil || len(commits) == 0 {
		writeProviderError(w, BitbucketCloud, http.StatusInternalServerError, "read commit")
		return
	}
	c := commits[0]
	base := requestBaseURL(r)
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"type": "commit", "hash": c.sha, "message": c.message + "\n", "date": formatTime(c.when, bitbucketTimeFormat),
		"author":     map[string]any{"type": "author", "raw": fmt.Sprintf("%s <%s>", c.authorName, c.authorEmail)},
		"repository": map[string]any{"type": "repository", "full_name": rp.fullName(), "name": rp.name, "uuid": rp.uuid},
		"links":      map[string]any{"html": map[string]any{"href": base + "/" + rp.fullName() + "/commits/" + c.sha}},
	})
}

func (s *Server) bitbucketStatusJSON(r *repo, cs *commitStatus, base string) map[string]any {
	return map[string]any{
		"type": "build", "key": cs.context, "state": cs.state, "name": cs.name, "description": cs.description,
		"url": cs.targetURL, "refname": nil, "created_on": formatTime(cs.createdAt, bitbucketTimeFormat),
		"updated_on": formatTime(cs.updatedAt, bitbucketTimeFormat),
		"commit":     map[string]any{"type": "commit", "hash": cs.sha},
		"links": map[string]any{"self": map[string]any{
			"href": fmt.Sprintf("%s%s/repositories/%s/commit/%s/statuses/build/%s", base, bitbucketAPI, r.fullName(), cs.sha, cs.context),
		}},
	}
}

// bitbucketCreateBuildStatus creates a build status, overwriting an existing status with the same key.
func (s *Server) bitbucketCreateBuildStatus(w http.ResponseWriter, r *http.Request, _ principal) {
	rp, ok := s.bitbucketRepo(w, r)
	if !ok {
		return
	}
	var req struct {
		Key         string `json:"key"`
		State       string `json:"state"`
		URL         string `json:"url"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !decodeJSON(w, r, BitbucketCloud, &req) {
		return
	}
	switch {
	case req.Key == "":
		bitbucketError(w, http.StatusBadRequest, "key: This field is required.", map[string][]string{"key": {"This field is required."}})
		return
	case len(req.Key) > 40:
		bitbucketError(w, http.StatusBadRequest, "key: Ensure this value has at most 40 characters.", map[string][]string{"key": {"Ensure this value has at most 40 characters."}})
		return
	case req.URL == "":
		bitbucketError(w, http.StatusBadRequest, "url: This field is required.", map[string][]string{"url": {"This field is required."}})
		return
	case !slices.Contains(bitbucketBuildStates, req.State):
		bitbucketError(w, http.StatusBadRequest, "state: Invalid state", map[string][]string{"state": {"\"" + req.State + "\" is not a valid choice."}})
		return
	default:
	}
	sha, err := s.git.resolve(r.Context(), rp.dir, r.PathValue("revision"))
	if err != nil {
		bitbucketError(w, http.StatusNotFound, "Commit not found", nil)
		return
	}
	now := s.now()
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	cs := s.store.latestStatusLocked(rp, sha, req.Key)
	if cs == nil {
		cs = s.store.addStatusLocked(rp, &commitStatus{sha: sha, context: req.Key, createdAt: now})
	}
	cs.state = req.State
	cs.targetURL = req.URL
	cs.name = req.Name
	cs.description = req.Description
	cs.updatedAt = now
	writeJSON(w, http.StatusCreated, s.bitbucketStatusJSON(rp, cs, requestBaseURL(r)))
}

func (s *Server) bitbucketGetBuildStatus(w http.ResponseWriter, r *http.Request, _ principal) {
	rp, ok := s.bitbucketRepo(w, r)
	if !ok {
		return
	}
	sha, err := s.git.resolve(r.Context(), rp.dir, r.PathValue("revision"))
	if err != nil {
		bitbucketError(w, http.StatusNotFound, "Commit not found", nil)
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	cs := s.store.latestStatusLocked(rp, sha, r.PathValue("key"))
	if cs == nil {
		bitbucketError(w, http.StatusNotFound, "Build status not found", nil)
		return
	}
	writeJSON(w, http.StatusOK, s.bitbucketStatusJSON(rp, cs, requestBaseURL(r)))
}

func (s *Server) bitbucketListBuildStatuses(w http.ResponseWriter, r *http.Request, _ principal) {
	rp, ok := s.bitbucketRepo(w, r)
	if !ok {
		return
	}
	sha, err := s.git.resolve(r.Context(), rp.dir, r.PathValue("revision"))
	if err != nil {
		bitbucketError(w, http.StatusNotFound, "Commit not found", nil)
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	statuses := s.store.statusesLocked(rp, sha)
	values := make([]map[string]any, 0, len(statuses))
	for _, cs := range statuses {
		values = append(values, s.bitbucketStatusJSON(rp, cs, requestBaseURL(r)))
	}
	writeJSON(w, http.StatusOK, map[string]any{"pagelen": 10, "size": len(values), "page": 1, "values": values})
}
