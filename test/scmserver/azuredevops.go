package scmserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// azureLocation is an Azure DevOps API resource location, as returned by OPTIONS {organization}/_apis. The SDK
// resolves every request through these locations and dereferences every field.
type azureLocation struct {
	ID              string `json:"id"`
	Area            string `json:"area"`
	ResourceName    string `json:"resourceName"`
	RouteTemplate   string `json:"routeTemplate"`
	MinVersion      string `json:"minVersion"`
	MaxVersion      string `json:"maxVersion"`
	ReleasedVersion string `json:"releasedVersion"`
	ResourceVersion int    `json:"resourceVersion"`
}

const (
	azureGitAreaID       = "4e080c62-fa21-4fbc-8fef-2a10a2b38049"
	azureResourceAreasID = "e81700f7-3be2-46de-8624-2eb35882fcaa"
)

var azureLocations = []azureLocation{
	{ID: azureResourceAreasID, Area: "Location", ResourceName: "ResourceAreas", RouteTemplate: "_apis/{resource}/{areaId}"},
	{ID: "9946fd70-0d40-406e-b686-b4744cbbcc37", Area: "git", ResourceName: "pullRequests", RouteTemplate: "{project}/_apis/{area}/repositories/{repositoryId}/{resource}/{pullRequestId}"},
	{ID: "01a46dea-7d46-4d40-bc84-319e7c260d99", Area: "git", ResourceName: "pullRequests", RouteTemplate: "{project}/_apis/{area}/{resource}/{pullRequestId}"},
	{ID: "a5d28130-9cd2-40fa-9f08-902e7daa9efb", Area: "git", ResourceName: "pullRequests", RouteTemplate: "{project}/_apis/{area}/{resource}"},
	{ID: "428dd4fb-fda5-4722-af02-9313b80305da", Area: "git", ResourceName: "statuses", RouteTemplate: "{project}/_apis/{area}/repositories/{repositoryId}/commits/{commitId}/{resource}"},
	{ID: "f22387e3-984e-4c52-9c6d-fbb8f14c812d", Area: "git", ResourceName: "labels", RouteTemplate: "{project}/_apis/{area}/repositories/{repositoryId}/pullRequests/{pullRequestId}/{resource}/{labelIdOrName}"},
	{ID: "225f7195-f9c7-4d14-ab28-a83f7ff77e1f", Area: "git", ResourceName: "repositories", RouteTemplate: "{project}/_apis/{area}/{resource}/{repositoryId}"},
}

var azureStatusStates = []string{"notSet", "pending", "succeeded", "failed", "error", "notApplicable"}

func (s *Server) azureRoutes(mux *http.ServeMux) {
	api := func(pattern string, h func(http.ResponseWriter, *http.Request, principal)) {
		mux.HandleFunc(pattern, s.apiHandler(AzureDevOps, h))
	}
	api("OPTIONS /{org}/_apis", s.azureOptions)
	api("OPTIONS /{org}/_apis/", s.azureOptions)
	api("GET /{org}/_apis/ResourceAreas", s.azureResourceAreas)
	api("GET /{org}/_apis/ResourceAreas/{areaId}", s.azureResourceAreas)
	api("GET /{org}/{project}/_apis/git/repositories/{repo}", s.azureGetRepo)
	api("GET /{org}/{project}/_apis/git/repositories/{repo}/pullRequests", s.azureListPRs)
	api("POST /{org}/{project}/_apis/git/repositories/{repo}/pullRequests", s.azureCreatePR)
	api("GET /{org}/{project}/_apis/git/repositories/{repo}/pullRequests/{id}", s.azureGetRepoPR)
	api("PATCH /{org}/{project}/_apis/git/repositories/{repo}/pullRequests/{id}", s.azureUpdatePR)
	api("GET /{org}/{project}/_apis/git/pullRequests/{id}", s.azureGetPRByID)
	api("GET /{org}/{project}/_apis/git/repositories/{repo}/pullRequests/{id}/labels", s.azureListLabels)
	api("POST /{org}/{project}/_apis/git/repositories/{repo}/pullRequests/{id}/labels", s.azureAddLabel)
	api("DELETE /{org}/{project}/_apis/git/repositories/{repo}/pullRequests/{id}/labels/{label}", s.azureRemoveLabel)
	api("POST /{org}/{project}/_apis/git/repositories/{repo}/commits/{sha}/statuses", s.azureCreateStatus)
	api("GET /{org}/{project}/_apis/git/repositories/{repo}/commits/{sha}/statuses", s.azureListStatuses)
}

// azureErrorResponse writes an Azure DevOps error with a TF error code in the message.
func azureErrorResponse(w http.ResponseWriter, status int, message, typeKey string) {
	writeJSON(w, status, azureError(message, typeKey, 0))
}

func azureCollection[T any](w http.ResponseWriter, values []T) {
	if values == nil {
		values = []T{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(values), "value": values})
}

func (s *Server) azureOptions(w http.ResponseWriter, _ *http.Request, _ principal) {
	locations := make([]azureLocation, 0, len(azureLocations))
	for _, l := range azureLocations {
		l.MinVersion, l.MaxVersion, l.ReleasedVersion, l.ResourceVersion = "1.0", "7.1", "7.1", 1
		locations = append(locations, l)
	}
	azureCollection(w, locations)
}

// azureResourceAreas reports the git area at the organization URL, as Azure DevOps Services does.
func (s *Server) azureResourceAreas(w http.ResponseWriter, r *http.Request, _ principal) {
	orgURL := requestBaseURL(r) + "/" + r.PathValue("org") + "/"
	areas := []map[string]string{
		{"id": azureGitAreaID, "name": "git", "locationUrl": orgURL},
		{"id": "79134c72-4a58-4b42-976c-04e7115f32bf", "name": "core", "locationUrl": orgURL},
	}
	if id := r.PathValue("areaId"); id != "" {
		for _, a := range areas {
			if strings.EqualFold(a["id"], id) {
				writeJSON(w, http.StatusOK, a)
				return
			}
		}
		azureErrorResponse(w, http.StatusNotFound, "The resource area "+id+" was not found.", "ResourceAreaNotFoundException")
		return
	}
	azureCollection(w, areas)
}

func (s *Server) azureRepo(w http.ResponseWriter, r *http.Request) (*repo, bool) {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	rp, err := s.store.findAzureRepoLocked(r.PathValue("org"), r.PathValue("project"), r.PathValue("repo"))
	if err != nil {
		azureErrorResponse(w, http.StatusNotFound,
			fmt.Sprintf("TF401019: The Git repository with name or identifier %s does not exist or you do not have permissions for the operation you are attempting.", r.PathValue("repo")),
			"GitRepositoryNotFoundException")
		return nil, false
	}
	return rp, true
}

func azurePRNotFound(w http.ResponseWriter, id string) {
	azureErrorResponse(w, http.StatusNotFound,
		fmt.Sprintf("TF401180: The requested pull request was not found. Pull request ID: %s.", id),
		"GitPullRequestNotFoundException")
}

func (s *Server) azurePR(w http.ResponseWriter, r *http.Request) (*pullRequest, bool) {
	rp, ok := s.azureRepo(w, r)
	if !ok {
		return nil, false
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		azurePRNotFound(w, r.PathValue("id"))
		return nil, false
	}
	s.store.mu.Lock()
	pr, err := s.store.findPRLocked(rp, id)
	s.store.mu.Unlock()
	if err != nil {
		azurePRNotFound(w, r.PathValue("id"))
		return nil, false
	}
	return pr, true
}

func azureIdentity(name string) map[string]any {
	return map[string]any{
		"displayName": name, "uniqueName": name, "id": "00000000-0000-0000-0000-000000000001",
		"url": "", "imageUrl": "", "descriptor": "aad." + name,
	}
}

func (s *Server) azureRepoJSON(r *repo, base string) map[string]any {
	return map[string]any{
		"id":   r.uuid,
		"name": r.name,
		"url":  fmt.Sprintf("%s/%s/%s/_apis/git/repositories/%s", base, r.owner, r.projectUUID, r.uuid),
		"project": map[string]any{
			"id": r.projectUUID, "name": r.project, "state": "wellFormed", "visibility": "private",
			"url": fmt.Sprintf("%s/%s/_apis/projects/%s", base, r.owner, r.projectUUID),
		},
		"defaultBranch": "refs/heads/" + r.defaultBranch,
		"remoteUrl":     fmt.Sprintf("%s/%s/%s/_git/%s", base, r.owner, r.project, r.name),
		"webUrl":        fmt.Sprintf("%s/%s/%s/_git/%s", base, r.owner, r.project, r.name),
		"isDisabled":    false,
	}
}

func (s *Server) azureGetRepo(w http.ResponseWriter, r *http.Request, _ principal) {
	rp, ok := s.azureRepo(w, r)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.azureRepoJSON(rp, requestBaseURL(r)))
}

// azureMergeStatusLocked is the PR's mergeStatus: queued while the merge preview or a completion is pending.
func (s *Server) azureMergeStatusLocked(pr *pullRequest) string {
	switch {
	case pr.completionFailure != "":
		if strings.HasPrefix(pr.completionFailure, "rejectedByPolicy") {
			return "rejectedByPolicy"
		}
		if pr.conflicts {
			return "conflicts"
		}
		return "failure"
	case pr.state == prMerged:
		return "succeeded"
	case pr.completionQueued, !pr.mergeabilityKnown(s.now()):
		return "queued"
	case pr.conflicts:
		return "conflicts"
	default:
		return "succeeded"
	}
}

func azureCommitRef(sha, url string) any {
	if sha == "" {
		return nil
	}
	return map[string]any{"commitId": sha, "url": url}
}

// azurePRJSONLocked renders a pull request. withLabels adds the labels collection, which the single-pull-request
// endpoints return and the list endpoint omits.
func (s *Server) azurePRJSONLocked(pr *pullRequest, base string, withLabels bool) map[string]any {
	r := pr.repo
	status := map[prState]string{prOpen: "active", prClosed: "abandoned", prMerged: "completed"}[pr.state]
	commitURL := func(sha string) string {
		return fmt.Sprintf("%s/%s/%s/_apis/git/repositories/%s/commits/%s", base, r.owner, r.projectUUID, r.uuid, sha)
	}
	lastMerge := pr.previewMergeSHA
	if pr.state == prMerged {
		lastMerge = pr.mergeCommitSHA
	} else if !pr.mergeabilityKnown(s.now()) {
		lastMerge = ""
	}
	out := map[string]any{
		"repository":            s.azureRepoJSON(r, base),
		"pullRequestId":         pr.number,
		"codeReviewId":          pr.number,
		"status":                status,
		"createdBy":             azureIdentity(pr.author),
		"creationDate":          formatTime(pr.createdAt, azureTimeFormat),
		"title":                 pr.title,
		"description":           pr.body,
		"sourceRefName":         "refs/heads/" + pr.source,
		"targetRefName":         "refs/heads/" + pr.target,
		"mergeStatus":           s.azureMergeStatusLocked(pr),
		"isDraft":               false,
		"mergeId":               newStableGUID(pr.globalID),
		"lastMergeSourceCommit": azureCommitRef(pr.headSHA, commitURL(pr.headSHA)),
		"lastMergeTargetCommit": azureCommitRef(pr.baseSHA, commitURL(pr.baseSHA)),
		"lastMergeCommit":       azureCommitRef(lastMerge, commitURL(lastMerge)),
		"reviewers":             []any{},
		"url":                   fmt.Sprintf("%s/%s/%s/_apis/git/repositories/%s/pullRequests/%d", base, r.owner, r.projectUUID, r.uuid, pr.number),
		"supportsIterations":    true,
		"artifactId":            fmt.Sprintf("vstfs:///Git/PullRequestId/%s%%2f%s%%2f%d", r.projectUUID, r.uuid, pr.number),
	}
	if withLabels {
		labels := make([]map[string]any, 0, len(pr.labels))
		for _, name := range pr.labels {
			if l := r.labelLocked(name); l != nil {
				labels = append(labels, s.azureLabelJSON(l))
			}
		}
		out["labels"] = labels
	}
	if !pr.closedAt.IsZero() {
		out["closedDate"] = formatTime(pr.closedAt, azureTimeFormat)
		out["closedBy"] = azureIdentity(pr.author)
	}
	if !pr.completionRequestedAt.IsZero() {
		strategy := "noFastForward"
		if pr.completionSquash {
			strategy = "squash"
		}
		out["completionQueueTime"] = formatTime(pr.completionRequestedAt, azureTimeFormat)
		out["completionOptions"] = map[string]any{"mergeCommitMessage": pr.completionMessage, "deleteSourceBranch": false, "mergeStrategy": strategy}
	}
	if pr.completionFailure != "" {
		out["mergeFailureType"] = "unknown"
		out["mergeFailureMessage"] = pr.completionFailure
	}
	return out
}

// newStableGUID derives a GUID-shaped identifier from a numeric ID.
func newStableGUID(id int64) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", id)
}

func trimRefsHeads(ref string) string {
	return strings.TrimPrefix(ref, "refs/heads/")
}

func (s *Server) azureListPRs(w http.ResponseWriter, r *http.Request, _ principal) {
	rp, ok := s.azureRepo(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	status := q.Get("searchCriteria.status")
	if status == "" || status == "notSet" {
		status = "active"
	}
	source, target := q.Get("searchCriteria.sourceRefName"), q.Get("searchCriteria.targetRefName")
	skip, _ := strconv.Atoi(q.Get("$skip"))
	top, err := strconv.Atoi(q.Get("$top"))
	if err != nil || top <= 0 {
		top = 101
	}
	now := s.now()
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	prs := s.store.prs[rp.id]
	out := make([]map[string]any, 0, len(prs))
	for _, pr := range slices.Backward(prs) { // newest first
		native := map[prState]string{prOpen: "active", prClosed: "abandoned", prMerged: "completed"}[pr.state]
		if !pr.visibleInLists(now) || (status != "all" && status != native) {
			continue
		}
		if source != "" && source != "refs/heads/"+pr.source {
			continue
		}
		if target != "" && target != "refs/heads/"+pr.target {
			continue
		}
		out = append(out, s.azurePRJSONLocked(pr, requestBaseURL(r), false))
	}
	start := min(max(skip, 0), len(out))
	end := min(start+top, len(out))
	azureCollection(w, out[start:end])
}

func (s *Server) azureCreatePR(w http.ResponseWriter, r *http.Request, who principal) {
	rp, ok := s.azureRepo(w, r)
	if !ok {
		return
	}
	var req struct {
		SourceRefName string              `json:"sourceRefName"`
		TargetRefName string              `json:"targetRefName"`
		Title         string              `json:"title"`
		Description   string              `json:"description"`
		Labels        []azureLabelRequest `json:"labels"`
	}
	if !decodeJSON(w, r, AzureDevOps, &req) {
		return
	}
	source, target := trimRefsHeads(req.SourceRefName), trimRefsHeads(req.TargetRefName)
	ctx := r.Context()
	_, headErr := s.git.branchSHA(ctx, rp.dir, source)
	_, baseErr := s.git.branchSHA(ctx, rp.dir, target)
	if headErr != nil || baseErr != nil {
		azureErrorResponse(w, http.StatusBadRequest,
			"TF401398: The pull request cannot be activated because the source and/or the target branch no longer exists, or the requested refs are not branches",
			"GitPullRequestCannotBeActivated")
		return
	}
	if source == target {
		azureErrorResponse(w, http.StatusBadRequest, "TF401196: The source and target refs are the same.", "InvalidArgumentValueException")
		return
	}
	s.store.mu.Lock()
	if existing := s.store.openPRForBranchesLocked(rp, source, target); existing != nil {
		s.store.mu.Unlock()
		azureErrorResponse(w, http.StatusConflict,
			fmt.Sprintf("TF401179: An active pull request for the source and target branch already exists. Pull request ID: %d.", existing.number),
			"GitPullRequestExistsException")
		return
	}
	pr := s.store.newPRLocked(rp, req.Title, req.Description, source, target, who.name, s.now())
	pr.hiddenUntil = pr.createdAt.Add(s.behavior.get(AzureDevOps).ListLag.Duration)
	for _, l := range req.Labels {
		pr.labels = addLabel(pr.labels, s.store.ensureLabelLocked(rp, l.Name, "").name)
	}
	s.store.mu.Unlock()
	if err := s.refreshPR(ctx, pr); err != nil {
		writeProviderError(w, AzureDevOps, http.StatusInternalServerError, err.Error())
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusCreated, s.azurePRJSONLocked(pr, requestBaseURL(r), true))
}

func (s *Server) azureGetRepoPR(w http.ResponseWriter, r *http.Request, _ principal) {
	pr, ok := s.azurePR(w, r)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.azurePRJSONLocked(pr, requestBaseURL(r), true))
}

func (s *Server) azureGetPRByID(w http.ResponseWriter, r *http.Request, _ principal) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		azurePRNotFound(w, r.PathValue("id"))
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	pr, err := s.store.findAzurePRLocked(r.PathValue("org"), r.PathValue("project"), id)
	if err != nil {
		azurePRNotFound(w, r.PathValue("id"))
		return
	}
	writeJSON(w, http.StatusOK, s.azurePRJSONLocked(pr, requestBaseURL(r), true))
}

type azureUpdatePRRequest struct {
	Title                 *string                 `json:"title"`
	Description           *string                 `json:"description"`
	Status                *string                 `json:"status"`
	TargetRefName         *string                 `json:"targetRefName"`
	LastMergeSourceCommit *azureCommitRequest     `json:"lastMergeSourceCommit"`
	CompletionOptions     *azureCompletionOptions `json:"completionOptions"`
}

type azureCommitRequest struct {
	CommitID string `json:"commitId"`
}

type azureCompletionOptions struct {
	MergeCommitMessage string `json:"mergeCommitMessage"`
	MergeStrategy      string `json:"mergeStrategy"`
}

type azureLabelRequest struct {
	Name string `json:"name"`
}

type azureStatusContext struct {
	Name  string `json:"name"`
	Genre string `json:"genre"`
}

// azureUpdatePR implements PATCH .../pullRequests/{id}. Edits and abandonment apply immediately. A completion request
// is refused with 400 when the pull request has conflicts or its required statuses have not passed (unless
// Behavior.AzureAsyncCompletionFailures is set), and with 409 when lastMergeSourceCommit is stale. An accepted
// completion is asynchronous: the response still shows the pull request active with mergeStatus "queued" and the
// preview merge commit, and the merge lands CompletionDelay later.
func (s *Server) azureUpdatePR(w http.ResponseWriter, r *http.Request, who principal) {
	pr, ok := s.azurePR(w, r)
	if !ok {
		return
	}
	var req azureUpdatePRRequest
	if !decodeJSON(w, r, AzureDevOps, &req) {
		return
	}
	behavior := s.behavior.get(AzureDevOps)
	s.store.mu.Lock()
	now := s.now()
	retarget := false
	if req.Title != nil {
		pr.title = *req.Title
	}
	if req.Description != nil {
		pr.body = *req.Description
	}
	if req.TargetRefName != nil && trimRefsHeads(*req.TargetRefName) != pr.target {
		pr.target = trimRefsHeads(*req.TargetRefName)
		retarget = true
	}
	if req.Status != nil {
		if problem, status := s.azureApplyStatusLocked(pr, req, who, behavior, now); problem != "" {
			s.store.mu.Unlock()
			azureErrorResponse(w, status, problem, "InvalidArgumentValueException")
			return
		}
	}
	pr.updatedAt = now
	s.store.mu.Unlock()
	if retarget {
		if err := s.refreshPR(r.Context(), pr); err != nil {
			writeProviderError(w, AzureDevOps, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, s.azurePRJSONLocked(pr, requestBaseURL(r), true))
}

// azureApplyStatusLocked applies a status change. It returns an error message and HTTP status on failure.
func (s *Server) azureApplyStatusLocked(pr *pullRequest, req azureUpdatePRRequest, who principal, behavior Behavior, now time.Time) (string, int) {
	switch *req.Status {
	case "abandoned":
		switch pr.state {
		case prMerged:
			return "The pull request cannot be abandoned because it is already completed.", http.StatusBadRequest
		case prOpen:
			pr.state = prClosed
			pr.closedAt = now
			pr.completionQueued = false
		default:
		}
	case "active":
		if pr.state == prClosed {
			pr.state = prOpen
			pr.closedAt = time.Time{}
		}
	case "completed":
		// Azure DevOps does not document the error responses of a completion request. The messages below carry no
		// TF error codes because the real codes are unknown; clients should branch on the HTTP status only.
		if pr.state != prOpen {
			return fmt.Sprintf("The pull request %d is not active and cannot be completed.", pr.number), http.StatusBadRequest
		}
		if req.LastMergeSourceCommit == nil || req.LastMergeSourceCommit.CommitID == "" {
			return "lastMergeSourceCommit is required to complete a pull request.", http.StatusBadRequest
		}
		if !strings.EqualFold(req.LastMergeSourceCommit.CommitID, pr.headSHA) {
			return fmt.Sprintf("The pull request could not be completed because the source branch has been updated. The last merge source commit is %s.", pr.headSHA), http.StatusConflict
		}
		squash := false
		if req.CompletionOptions != nil {
			switch req.CompletionOptions.MergeStrategy {
			case "", "noFastForward":
			case "squash":
				squash = true
			case "rebase", "rebaseMerge":
				// Allowed by Azure DevOps by default, but the server does not replay commits.
				return "The merge strategy " + req.CompletionOptions.MergeStrategy + " is not supported by the test SCM server.", http.StatusBadRequest
			default:
				return "Invalid merge strategy: " + req.CompletionOptions.MergeStrategy, http.StatusBadRequest
			}
		}
		if !behavior.AzureAsyncCompletionFailures {
			// Policies are evaluated when completion is requested (completionOptions.bypassPolicy exists to skip
			// them), and a pull request with conflicts cannot be completed. Refuse now rather than after queueing.
			if pr.mergeabilityKnown(now) && pr.conflicts {
				return "The pull request cannot be completed because it has merge conflicts.", http.StatusBadRequest
			}
			if missing := s.missingRequiredStatusesLocked(pr); len(missing) > 0 {
				return "The pull request cannot be completed because the following required policies are not met: status " + strings.Join(missing, ", ") + ".", http.StatusBadRequest
			}
		}
		pr.completionQueued = true
		pr.completionRequestedAt = now
		pr.completionDueAt = now.Add(behavior.CompletionDelay.Duration)
		pr.completionSourceSHA = pr.headSHA
		pr.completionFailure = ""
		pr.completionSquash = squash
		pr.author = who.name
		pr.completionMessage = ""
		if req.CompletionOptions != nil {
			pr.completionMessage = req.CompletionOptions.MergeCommitMessage
		}
	default:
		return "Invalid pull request status: " + *req.Status, http.StatusBadRequest
	}
	return "", 0
}

// processAzureCompletions lands queued Azure DevOps completions whose delay elapsed.
func (s *Server) processAzureCompletions(ctx context.Context) {
	now := s.now()
	s.store.mu.Lock()
	var due []*pullRequest
	for _, r := range s.store.repos {
		if r.provider != AzureDevOps {
			continue
		}
		for _, pr := range s.store.prs[r.id] {
			if pr.completionQueued && pr.state == prOpen && !now.Before(pr.completionDueAt) {
				due = append(due, pr)
			}
		}
	}
	s.store.mu.Unlock()
	for _, pr := range due {
		s.completeAzurePR(ctx, pr)
	}
}

func (s *Server) completeAzurePR(ctx context.Context, pr *pullRequest) {
	s.store.mu.Lock()
	missing := s.missingRequiredStatusesLocked(pr)
	merge := mergeRequest{
		pr: pr, expectedHead: pr.completionSourceSHA, message: defaultMergeMessage(pr, pr.completionMessage),
		squashMessage: defaultSquashMessage(pr, pr.completionMessage), author: actor(AzureDevOps, pr.author),
		squash: pr.completionSquash,
	}
	if len(missing) > 0 {
		// Only reachable with AzureAsyncCompletionFailures: the policy failed after the request was accepted.
		pr.completionQueued = false
		pr.completionFailure = "rejectedByPolicy: required statuses " + strings.Join(missing, ", ") + " have not succeeded"
		s.store.mu.Unlock()
		return
	}
	s.store.mu.Unlock()

	_, err := s.landMerge(ctx, merge)
	if err == nil {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	pr.completionQueued = false
	switch {
	case errors.Is(err, errMergeConflict):
		pr.conflicts = true
		pr.completionFailure = "The pull request cannot be completed because it has merge conflicts."
	case errors.Is(err, errHeadMismatch):
		pr.completionFailure = "The source branch was updated after completion was requested."
	default:
		pr.completionFailure = err.Error()
	}
}

func (s *Server) azureLabelJSON(l *label) map[string]any {
	return map[string]any{"id": newStableGUID(l.id), "name": l.name, "active": true, "url": ""}
}

func (s *Server) azureListLabels(w http.ResponseWriter, r *http.Request, _ principal) {
	pr, ok := s.azurePR(w, r)
	if !ok {
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	var out []map[string]any
	for _, name := range pr.labels {
		if l := pr.repo.labelLocked(name); l != nil {
			out = append(out, s.azureLabelJSON(l))
		}
	}
	azureCollection(w, out)
}

func (s *Server) azureAddLabel(w http.ResponseWriter, r *http.Request, _ principal) {
	pr, ok := s.azurePR(w, r)
	if !ok {
		return
	}
	var req azureLabelRequest
	if !decodeJSON(w, r, AzureDevOps, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		azureErrorResponse(w, http.StatusBadRequest, "The label name cannot be empty.", "InvalidArgumentValueException")
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	l := s.store.ensureLabelLocked(pr.repo, req.Name, "")
	pr.labels = addLabel(pr.labels, l.name)
	pr.updatedAt = s.now()
	writeJSON(w, http.StatusOK, s.azureLabelJSON(l))
}

func (s *Server) azureRemoveLabel(w http.ResponseWriter, r *http.Request, _ principal) {
	pr, ok := s.azurePR(w, r)
	if !ok {
		return
	}
	name := r.PathValue("label")
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	for _, l := range pr.repo.labels {
		if newStableGUID(l.id) == name {
			name = l.name
		}
	}
	labels, removed := removeLabel(pr.labels, name)
	if !removed {
		azureErrorResponse(w, http.StatusNotFound, "The label "+name+" was not found on the pull request.", "WebApiTagNotFoundException")
		return
	}
	pr.labels = labels
	pr.updatedAt = s.now()
	writeNoContent(w, http.StatusNoContent)
}

func (s *Server) azureStatusJSON(r *repo, cs *commitStatus, base string) map[string]any {
	return map[string]any{
		"id":           cs.id,
		"state":        cs.state,
		"description":  cs.description,
		"targetUrl":    cs.targetURL,
		"context":      map[string]any{"name": cs.context, "genre": cs.genre},
		"creationDate": formatTime(cs.createdAt, azureTimeFormat),
		"updatedDate":  formatTime(cs.updatedAt, azureTimeFormat),
		"createdBy":    azureIdentity("azuredevops-user"),
		"url": fmt.Sprintf("%s/%s/%s/_apis/git/repositories/%s/commits/%s/statuses/%d",
			base, r.owner, r.projectUUID, r.uuid, cs.sha, cs.id),
	}
}

func (s *Server) azureCreateStatus(w http.ResponseWriter, r *http.Request, _ principal) {
	rp, ok := s.azureRepo(w, r)
	if !ok {
		return
	}
	var req struct {
		Context     *azureStatusContext `json:"context"`
		State       string              `json:"state"`
		Description string              `json:"description"`
		TargetURL   string              `json:"targetUrl"`
	}
	if !decodeJSON(w, r, AzureDevOps, &req) {
		return
	}
	if req.Context == nil || req.Context.Name == "" {
		azureErrorResponse(w, http.StatusBadRequest, "The status context name cannot be empty.", "InvalidArgumentValueException")
		return
	}
	if req.State == "" {
		req.State = "notSet"
	}
	if !slices.Contains(azureStatusStates, req.State) {
		azureErrorResponse(w, http.StatusBadRequest, "Invalid status state: "+req.State, "InvalidArgumentValueException")
		return
	}
	sha, err := s.git.resolve(r.Context(), rp.dir, r.PathValue("sha"))
	if err != nil {
		azureErrorResponse(w, http.StatusNotFound, "TF401175: The commit "+r.PathValue("sha")+" does not exist.", "GitCommitDoesNotExistException")
		return
	}
	now := s.now()
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	cs := s.store.addStatusLocked(rp, &commitStatus{
		sha: sha, context: req.Context.Name, genre: req.Context.Genre, state: req.State,
		description: req.Description, targetURL: req.TargetURL, createdAt: now, updatedAt: now,
	})
	writeJSON(w, http.StatusCreated, s.azureStatusJSON(rp, cs, requestBaseURL(r)))
}

func (s *Server) azureListStatuses(w http.ResponseWriter, r *http.Request, _ principal) {
	rp, ok := s.azureRepo(w, r)
	if !ok {
		return
	}
	sha, err := s.git.resolve(r.Context(), rp.dir, r.PathValue("sha"))
	if err != nil {
		azureErrorResponse(w, http.StatusNotFound, "TF401175: The commit "+r.PathValue("sha")+" does not exist.", "GitCommitDoesNotExistException")
		return
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	statuses := s.store.statusesLocked(rp, sha)
	out := make([]map[string]any, 0, len(statuses))
	for i := len(statuses) - 1; i >= 0; i-- {
		out = append(out, s.azureStatusJSON(rp, statuses[i], requestBaseURL(r)))
	}
	azureCollection(w, out)
}
