package scmserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ProviderInfo describes how to reach one provider listener.
type ProviderInfo struct {
	// Address is the bound host:port.
	Address string `json:"address"`
	// Domain is the value for an ScmProvider "domain" field when the controller runs on this host.
	Domain string `json:"domain"`
	// BaseURL is the HTTPS base URL using the server's public host name.
	BaseURL string `json:"baseURL"`
}

// ServerInfo describes the running server.
type ServerInfo struct {
	Providers map[Provider]ProviderInfo `json:"providers"`
	DataDir   string                    `json:"dataDir"`
}

// CreateRepoRequest creates a hosted repository.
type CreateRepoRequest struct {
	// Provider hosting the repository.
	Provider Provider `json:"provider"`
	// Owner is the GitHub/Gitea/Forgejo owner, GitLab namespace (may contain "/"), Bitbucket workspace, or Azure
	// DevOps organization.
	Owner string `json:"owner"`
	// Project is the Azure DevOps project. Ignored for other providers.
	Project string `json:"project,omitempty"`
	// Name of the repository.
	Name string `json:"name"`
	// DefaultBranch defaults to "main".
	DefaultBranch string `json:"defaultBranch,omitempty"`
	// Init creates an initial commit with a README on the default branch.
	Init bool `json:"init,omitempty"`
}

// RepoInfo describes a hosted repository.
type RepoInfo struct {
	CreatedAt     time.Time         `json:"createdAt"`
	Branches      map[string]string `json:"branches,omitempty"`
	Provider      Provider          `json:"provider"`
	Owner         string            `json:"owner"`
	Project       string            `json:"project,omitempty"`
	Name          string            `json:"name"`
	FullName      string            `json:"fullName"`
	DefaultBranch string            `json:"defaultBranch"`
	// CloneURL uses the server's public host name.
	CloneURL string `json:"cloneURL"`
	// UUID is the Azure DevOps repository ID or the Bitbucket repository UUID.
	UUID string `json:"uuid,omitempty"`
	// ProjectUUID is the Azure DevOps project ID.
	ProjectUUID string `json:"projectUUID,omitempty"`
	// ID is the numeric repository ID; for GitLab it is the project ID.
	ID int64 `json:"id"`
}

// CommitRequest writes a commit to a branch through the admin API, as if it had been pushed.
type CommitRequest struct {
	// Files maps paths to their new content.
	Files map[string]string `json:"files,omitempty"`
	// Notes maps a notes ref name (as passed to `git notes --ref`) to a note attached to the new commit.
	Notes map[string]string `json:"notes,omitempty"`
	// Branch receiving the commit. It is created if it does not exist.
	Branch string `json:"branch"`
	// Parent is the parent commit. Defaults to the head of Branch, or to the head of the default branch when Branch
	// does not exist yet.
	Parent string `json:"parent,omitempty"`
	// Message defaults to "Update <branch>".
	Message string `json:"message,omitempty"`
	// AuthorName and AuthorEmail identify the author and the pusher in webhooks.
	AuthorName string `json:"authorName,omitempty"`
	// AuthorEmail of the commit.
	AuthorEmail string `json:"authorEmail,omitempty"`
	// Delete removes paths.
	Delete []string `json:"delete,omitempty"`
	// Orphan creates a commit without a parent.
	Orphan bool `json:"orphan,omitempty"`
	// Force allows moving the branch to a commit that does not descend from its current head.
	Force bool `json:"force,omitempty"`
}

// CommitResponse is the result of a CommitRequest.
type CommitResponse struct {
	// SHA of the new commit.
	SHA string `json:"sha"`
	// Before is the previous head of the branch, or the zero SHA for a new branch.
	Before string `json:"before"`
}

// BranchRequest points a branch at an existing commit, creating the branch if needed, as if it had been pushed.
type BranchRequest struct {
	// SHA is the commit (or any revision) the branch points to.
	SHA string `json:"sha"`
	// Force allows moving the branch to a commit that does not descend from its current head.
	Force bool `json:"force,omitempty"`
}

// PullRequestInfo describes a pull request, including server-side state providers do not expose directly.
type PullRequestInfo struct {
	CreatedAt             time.Time  `json:"createdAt"`
	UpdatedAt             time.Time  `json:"updatedAt"`
	ClosedAt              *time.Time `json:"closedAt,omitempty"`
	MergedAt              *time.Time `json:"mergedAt,omitempty"`
	CompletionRequestedAt *time.Time `json:"completionRequestedAt,omitempty"`
	Provider              Provider   `json:"provider"`
	Repo                  string     `json:"repo"`
	State                 string     `json:"state"`
	Title                 string     `json:"title"`
	Body                  string     `json:"body"`
	Source                string     `json:"source"`
	Target                string     `json:"target"`
	Author                string     `json:"author"`
	HeadSHA               string     `json:"headSHA"`
	BaseSHA               string     `json:"baseSHA"`
	MergeCommitSHA        string     `json:"mergeCommitSHA,omitempty"`
	SquashCommitSHA       string     `json:"squashCommitSHA,omitempty"`
	PreviewMergeSHA       string     `json:"previewMergeSHA,omitempty"`
	CompletionFailure     string     `json:"completionFailure,omitempty"`
	Labels                []string   `json:"labels"`
	ID                    int64      `json:"id"`
	RepoID                int64      `json:"repoID"`
	Number                int        `json:"number"`
	Conflicts             bool       `json:"conflicts"`
	MergeabilityKnown     bool       `json:"mergeabilityKnown"`
	CompletionQueued      bool       `json:"completionQueued"`
	AsyncMergePending     bool       `json:"asyncMergePending"`
	HiddenFromLists       bool       `json:"hiddenFromLists"`
	Squashed              bool       `json:"squashed"`
}

// PullRequestAction changes a pull request out of band, as a person using the provider's UI would.
type PullRequestAction struct {
	// Action is one of: merge, close, reopen, set-labels, complete-now (Azure DevOps), fail-completion
	// (Azure DevOps), delete, hide, show.
	Action string `json:"action"`
	// Message is the merge commit message for merge, or the failure message for fail-completion.
	Message string `json:"message,omitempty"`
	// Labels for set-labels.
	Labels []string `json:"labels,omitempty"`
	// Squash makes merge squash the pull request into a single commit with the target branch as its only parent. On
	// every provider but GitLab that commit becomes the target branch tip. GitLab's default merge method then merges
	// the squash commit with a merge commit, which becomes the tip; the squash commit is reported as squashCommitSHA.
	Squash bool `json:"squash,omitempty"`
}

// CommitStatusInfo describes a commit status, build status or check run.
type CommitStatusInfo struct {
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	Repo        string    `json:"repo"`
	SHA         string    `json:"sha"`
	Context     string    `json:"context"`
	State       string    `json:"state"`
	Description string    `json:"description,omitempty"`
	TargetURL   string    `json:"targetURL,omitempty"`
	Status      string    `json:"status,omitempty"`
	Conclusion  string    `json:"conclusion,omitempty"`
	ID          int64     `json:"id"`
	RepoID      int64     `json:"repoID"`
}

// ProtectionRequest configures branch protection.
type ProtectionRequest struct {
	// RequiredStatuses are status contexts (check run names, Bitbucket keys) that must succeed on the head commit
	// before a pull request into the branch can merge.
	RequiredStatuses []string `json:"requiredStatuses"`
}

func (s *Server) adminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /ca.pem", func(w http.ResponseWriter, _ *http.Request) {
		if len(s.caPEM) == 0 {
			http.Error(w, "CA not known", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/x-pem-file")
		_, _ = w.Write(s.caPEM)
	})
	mux.HandleFunc("GET /api/info", s.adminInfo)
	mux.HandleFunc("POST /api/reset", s.adminReset)
	mux.HandleFunc("POST /api/credentials", s.adminAddCredential)
	mux.HandleFunc("POST /api/github/apps", s.adminRegisterGitHubApp)

	mux.HandleFunc("GET /api/repos", s.adminListRepos)
	mux.HandleFunc("POST /api/repos", s.adminCreateRepo)
	mux.HandleFunc("GET /api/repos/{id}", s.adminGetRepo)
	mux.HandleFunc("DELETE /api/repos/{id}", s.adminDeleteRepo)
	mux.HandleFunc("POST /api/repos/{id}/commits", s.adminCommit)
	mux.HandleFunc("PUT /api/repos/{id}/branches/{branch...}", s.adminSetBranch)
	mux.HandleFunc("PUT /api/repos/{id}/protection/{branch...}", s.adminSetProtection)
	mux.HandleFunc("GET /api/repos/{id}/pulls/{number}", s.adminGetPull)
	mux.HandleFunc("POST /api/repos/{id}/pulls/{number}/actions", s.adminPullAction)

	mux.HandleFunc("GET /api/pulls", s.adminListPulls)
	mux.HandleFunc("GET /api/statuses", s.adminListStatuses)

	mux.HandleFunc("GET /api/requests", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, s.requests.list())
	})
	mux.HandleFunc("DELETE /api/requests", func(w http.ResponseWriter, _ *http.Request) {
		s.requests.reset()
		writeNoContent(w, http.StatusNoContent)
	})

	mux.HandleFunc("GET /api/behavior", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, s.behavior.all())
	})
	mux.HandleFunc("PUT /api/behavior/{provider}", s.adminSetBehavior)
	mux.HandleFunc("DELETE /api/behavior", func(w http.ResponseWriter, _ *http.Request) {
		s.behavior.reset()
		writeNoContent(w, http.StatusNoContent)
	})

	mux.HandleFunc("GET /api/faults", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, s.faults.list())
	})
	mux.HandleFunc("POST /api/faults", s.adminAddFault)
	mux.HandleFunc("DELETE /api/faults", func(w http.ResponseWriter, _ *http.Request) {
		s.faults.reset()
		writeNoContent(w, http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /api/faults/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || !s.faults.remove(id) {
			http.Error(w, "fault not found", http.StatusNotFound)
			return
		}
		writeNoContent(w, http.StatusNoContent)
	})

	mux.HandleFunc("GET /api/webhooks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, s.webhooks.list())
	})
	mux.HandleFunc("POST /api/webhooks", s.adminAddWebhook)
	mux.HandleFunc("DELETE /api/webhooks/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || !s.webhooks.remove(id) {
			http.Error(w, "webhook not found", http.StatusNotFound)
			return
		}
		writeNoContent(w, http.StatusNoContent)
	})
	mux.HandleFunc("GET /api/webhooks/deliveries", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, s.webhooks.listDeliveries())
	})
	return mux
}

func adminError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func decodeAdmin(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		adminError(w, http.StatusBadRequest, fmt.Errorf("decode request: %w", err))
		return false
	}
	return true
}

// Info returns how to reach every provider listener.
func (s *Server) Info() ServerInfo {
	info := ServerInfo{Providers: map[Provider]ProviderInfo{}, DataDir: s.opts.DataDir}
	for _, p := range AllProviders {
		info.Providers[p] = ProviderInfo{Address: s.Addr(p), Domain: s.Domain(p), BaseURL: s.publicBaseURL(p)}
	}
	return info
}

func (s *Server) adminInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Info())
}

// Reset deletes all repositories, pull requests, statuses, credentials, apps, webhooks, faults and request records,
// and restores default behavior.
func (s *Server) Reset() error {
	s.store.mu.Lock()
	repos := s.store.sortedReposLocked()
	s.store.resetLocked()
	s.store.mu.Unlock()
	var errs []error
	for _, r := range repos {
		if err := os.RemoveAll(r.dir); err != nil {
			errs = append(errs, err)
		}
	}
	s.behavior.reset()
	s.faults.reset()
	s.requests.reset()
	s.webhooks.reset()
	return errors.Join(errs...)
}

func (s *Server) adminReset(w http.ResponseWriter, _ *http.Request) {
	if err := s.Reset(); err != nil {
		adminError(w, http.StatusInternalServerError, err)
		return
	}
	writeNoContent(w, http.StatusNoContent)
}

func (s *Server) adminAddCredential(w http.ResponseWriter, r *http.Request) {
	var c Credential
	if !decodeAdmin(w, r, &c) {
		return
	}
	if err := s.addCredential(c); err != nil {
		adminError(w, http.StatusBadRequest, err)
		return
	}
	writeNoContent(w, http.StatusNoContent)
}

func (s *Server) adminRegisterGitHubApp(w http.ResponseWriter, r *http.Request) {
	var req RegisterGitHubAppRequest
	if !decodeAdmin(w, r, &req) {
		return
	}
	if err := s.registerGitHubApp(req); err != nil {
		adminError(w, http.StatusBadRequest, err)
		return
	}
	writeNoContent(w, http.StatusNoContent)
}

// CreateRepo creates a hosted repository.
func (s *Server) CreateRepo(ctx context.Context, req CreateRepoRequest) (RepoInfo, error) {
	if !req.Provider.Valid() {
		return RepoInfo{}, fmt.Errorf("unknown provider %q", req.Provider)
	}
	if req.Owner == "" || req.Name == "" {
		return RepoInfo{}, errors.New("owner and name are required")
	}
	if req.Provider == AzureDevOps && req.Project == "" {
		return RepoInfo{}, errors.New("project is required for Azure DevOps repositories")
	}
	// Names become directories under the data directory, so they must not traverse out of it.
	if strings.Contains(req.Owner, "..") || strings.Contains(req.Project, "..") || strings.Contains(req.Name, "..") {
		return RepoInfo{}, errors.New("owner, project and name must not contain \"..\"")
	}
	if err := validateRepoNames(req); err != nil {
		return RepoInfo{}, err
	}
	if req.DefaultBranch == "" {
		req.DefaultBranch = "main"
	}

	s.store.mu.Lock()
	if _, err := s.store.findRepoLocked(req.Provider, req.Owner, req.Project, req.Name); err == nil {
		s.store.mu.Unlock()
		return RepoInfo{}, fmt.Errorf("repository %s/%s already exists", req.Owner, req.Name)
	}
	r := &repo{
		id:            s.store.allocIDLocked(),
		provider:      req.Provider,
		owner:         req.Owner,
		project:       req.Project,
		name:          req.Name,
		defaultBranch: req.DefaultBranch,
		createdAt:     s.now(),
		protection:    map[string][]string{},
	}
	switch req.Provider {
	case AzureDevOps:
		r.uuid = newGUID()
		r.projectUUID = s.azureProjectUUIDLocked(req.Owner, req.Project)
	case BitbucketCloud:
		r.uuid = "{" + newGUID() + "}"
	default:
	}
	parts := []string{req.Provider.dirName(), req.Owner}
	if req.Project != "" {
		parts = append(parts, req.Project)
	}
	parts = append(parts, req.Name+".git")
	r.dir = filepath.Join(append([]string{s.opts.DataDir}, parts...)...)
	s.store.repos[r.id] = r
	s.store.mu.Unlock()

	if err := s.git.initBare(ctx, r.dir, req.DefaultBranch); err != nil {
		s.store.mu.Lock()
		delete(s.store.repos, r.id)
		s.store.mu.Unlock()
		return RepoInfo{}, err
	}
	if req.Init {
		if _, err := s.Commit(ctx, r.id, CommitRequest{
			Branch:  req.DefaultBranch,
			Files:   map[string]string{"README.md": "# " + req.Name + "\n"},
			Message: "Initial commit",
		}); err != nil {
			return RepoInfo{}, err
		}
	}
	return s.repoInfo(ctx, r)
}

// validRepoSegment matches one segment of an owner, project or repository name.
var validRepoSegment = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)

// validateRepoNames checks that owner, project and name are made of safe path segments. Only GitLab namespaces may
// contain "/", for subgroups.
func validateRepoNames(req CreateRepoRequest) error {
	values := []string{req.Owner, req.Name}
	if req.Provider == GitLab {
		values = append(strings.Split(req.Owner, "/"), req.Name)
	}
	if req.Project != "" {
		values = append(values, req.Project)
	}
	for _, v := range values {
		if !validRepoSegment.MatchString(v) {
			return fmt.Errorf("invalid name %q: use letters, digits, '.', '-' and '_'", v)
		}
	}
	return nil
}

// azureProjectUUIDLocked returns the ID of an Azure DevOps project, reusing the ID of existing repositories in it.
func (s *Server) azureProjectUUIDLocked(org, project string) string {
	for _, r := range s.store.repos {
		if r.provider == AzureDevOps && strings.EqualFold(r.owner, org) && strings.EqualFold(r.project, project) {
			return r.projectUUID
		}
	}
	return newGUID()
}

func (s *Server) cloneURL(r *repo) string {
	base := s.publicBaseURL(r.provider)
	switch r.provider {
	case AzureDevOps:
		return fmt.Sprintf("%s/%s/%s/_git/%s", base, r.owner, r.project, r.name)
	default:
		return fmt.Sprintf("%s/%s/%s.git", base, r.owner, r.name)
	}
}

func (s *Server) repoInfo(ctx context.Context, r *repo) (RepoInfo, error) {
	refs, err := s.git.refs(ctx, r.dir)
	if err != nil {
		return RepoInfo{}, err
	}
	branches := map[string]string{}
	for ref, sha := range refs {
		if name, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
			branches[name] = sha
		}
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	return RepoInfo{
		ID: r.id, Provider: r.provider, Owner: r.owner, Project: r.project, Name: r.name, FullName: r.fullName(),
		DefaultBranch: r.defaultBranch, CloneURL: s.cloneURL(r), UUID: r.uuid, ProjectUUID: r.projectUUID,
		CreatedAt: r.createdAt, Branches: branches,
	}, nil
}

func (s *Server) adminCreateRepo(w http.ResponseWriter, r *http.Request) {
	var req CreateRepoRequest
	if !decodeAdmin(w, r, &req) {
		return
	}
	info, err := s.CreateRepo(r.Context(), req)
	if err != nil {
		adminError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, info)
}

func (s *Server) adminRepo(w http.ResponseWriter, r *http.Request) (*repo, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		adminError(w, http.StatusBadRequest, fmt.Errorf("invalid repository ID: %w", err))
		return nil, false
	}
	s.store.mu.Lock()
	found, ok := s.store.repos[id]
	s.store.mu.Unlock()
	if !ok {
		adminError(w, http.StatusNotFound, errNotFound)
		return nil, false
	}
	return found, true
}

func (s *Server) adminListRepos(w http.ResponseWriter, r *http.Request) {
	s.store.mu.Lock()
	repos := s.store.sortedReposLocked()
	s.store.mu.Unlock()
	out := make([]RepoInfo, 0, len(repos))
	for _, rp := range repos {
		info, err := s.repoInfo(r.Context(), rp)
		if err != nil {
			adminError(w, http.StatusInternalServerError, err)
			return
		}
		out = append(out, info)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminGetRepo(w http.ResponseWriter, r *http.Request) {
	rp, ok := s.adminRepo(w, r)
	if !ok {
		return
	}
	info, err := s.repoInfo(r.Context(), rp)
	if err != nil {
		adminError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) adminDeleteRepo(w http.ResponseWriter, r *http.Request) {
	rp, ok := s.adminRepo(w, r)
	if !ok {
		return
	}
	s.store.mu.Lock()
	delete(s.store.repos, rp.id)
	delete(s.store.prs, rp.id)
	delete(s.store.statuses, rp.id)
	s.store.mu.Unlock()
	if err := os.RemoveAll(rp.dir); err != nil {
		adminError(w, http.StatusInternalServerError, err)
		return
	}
	writeNoContent(w, http.StatusNoContent)
}

// Commit writes a commit to a branch of repository repoID and notifies webhook subscribers as for a push.
func (s *Server) Commit(ctx context.Context, repoID int64, req CommitRequest) (CommitResponse, error) {
	s.store.mu.Lock()
	r, ok := s.store.repos[repoID]
	s.store.mu.Unlock()
	if !ok {
		return CommitResponse{}, errNotFound
	}
	if req.Branch == "" {
		return CommitResponse{}, errors.New("branch is required")
	}
	if req.Message == "" {
		req.Message = "Update " + req.Branch
	}
	author := signature{name: req.AuthorName, email: req.AuthorEmail, when: s.now()}
	if author.name == "" {
		author.name = "scmserver"
	}
	if author.email == "" {
		author.email = "scmserver@example.com"
	}

	r.gitMu.Lock()
	before, err := s.git.branchSHA(ctx, r.dir, req.Branch)
	if err != nil && !errors.Is(err, errNotFound) {
		r.gitMu.Unlock()
		return CommitResponse{}, err
	}
	parent := req.Parent
	if parent == "" && !req.Orphan {
		parent = before
		if parent == "" {
			// A new branch starts from the default branch, if it has commits.
			parent, _ = s.git.branchSHA(ctx, r.dir, r.defaultBranch)
		}
	}
	if parent != "" {
		if parent, err = s.git.resolve(ctx, r.dir, parent); err != nil {
			r.gitMu.Unlock()
			return CommitResponse{}, fmt.Errorf("resolve parent %q: %w", req.Parent, err)
		}
	}
	sha, err := s.git.writeCommit(ctx, r.dir, commitFiles{
		files: req.Files, delete: req.Delete, message: req.Message, parent: parent, orphan: req.Orphan, author: author,
	})
	if err != nil {
		r.gitMu.Unlock()
		return CommitResponse{}, err
	}
	if before != "" && !req.Force {
		ff, err := s.git.isAncestor(ctx, r.dir, before, sha)
		if err != nil || !ff {
			r.gitMu.Unlock()
			return CommitResponse{}, fmt.Errorf("commit %s does not descend from %s (set force to rewrite the branch)", sha, before)
		}
	}
	if err := s.git.updateRef(ctx, r.dir, "refs/heads/"+req.Branch, sha, before); err != nil {
		r.gitMu.Unlock()
		return CommitResponse{}, err
	}
	noteRefs := make([]string, 0, len(req.Notes))
	for ref := range req.Notes {
		noteRefs = append(noteRefs, ref)
	}
	slices.Sort(noteRefs)
	for _, ref := range noteRefs {
		if err := s.git.addNote(ctx, r.dir, ref, sha, req.Notes[ref], author); err != nil {
			r.gitMu.Unlock()
			return CommitResponse{}, err
		}
	}
	r.gitMu.Unlock()

	before = orZero(before)
	s.afterRefChanges(ctx, r, []refChange{{ref: "refs/heads/" + req.Branch, before: before, after: sha}}, author.name)
	return CommitResponse{SHA: sha, Before: before}, nil
}

func (s *Server) adminCommit(w http.ResponseWriter, r *http.Request) {
	rp, ok := s.adminRepo(w, r)
	if !ok {
		return
	}
	var req CommitRequest
	if !decodeAdmin(w, r, &req) {
		return
	}
	resp, err := s.Commit(r.Context(), rp.id, req)
	if err != nil {
		adminError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

// SetBranch points branch of repository repoID at req.SHA and notifies webhook subscribers as for a push.
func (s *Server) SetBranch(ctx context.Context, repoID int64, branch string, req BranchRequest) (CommitResponse, error) {
	s.store.mu.Lock()
	r, ok := s.store.repos[repoID]
	s.store.mu.Unlock()
	if !ok {
		return CommitResponse{}, errNotFound
	}
	r.gitMu.Lock()
	sha, err := s.git.resolve(ctx, r.dir, req.SHA)
	if err != nil {
		r.gitMu.Unlock()
		return CommitResponse{}, fmt.Errorf("resolve %q: %w", req.SHA, err)
	}
	before, err := s.git.branchSHA(ctx, r.dir, branch)
	if err != nil && !errors.Is(err, errNotFound) {
		r.gitMu.Unlock()
		return CommitResponse{}, err
	}
	if before != "" && !req.Force {
		ff, err := s.git.isAncestor(ctx, r.dir, before, sha)
		if err != nil || !ff {
			r.gitMu.Unlock()
			return CommitResponse{}, fmt.Errorf("%s does not descend from %s (set force to rewrite the branch)", sha, before)
		}
	}
	if err := s.git.updateRef(ctx, r.dir, "refs/heads/"+branch, sha, before); err != nil {
		r.gitMu.Unlock()
		return CommitResponse{}, err
	}
	r.gitMu.Unlock()
	before = orZero(before)
	if before != sha {
		s.afterRefChanges(ctx, r, []refChange{{ref: "refs/heads/" + branch, before: before, after: sha}}, "scmserver")
	}
	return CommitResponse{SHA: sha, Before: before}, nil
}

func (s *Server) adminSetBranch(w http.ResponseWriter, r *http.Request) {
	rp, ok := s.adminRepo(w, r)
	if !ok {
		return
	}
	var req BranchRequest
	if !decodeAdmin(w, r, &req) {
		return
	}
	resp, err := s.SetBranch(r.Context(), rp.id, r.PathValue("branch"), req)
	if err != nil {
		adminError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) adminSetProtection(w http.ResponseWriter, r *http.Request) {
	rp, ok := s.adminRepo(w, r)
	if !ok {
		return
	}
	var req ProtectionRequest
	if !decodeAdmin(w, r, &req) {
		return
	}
	s.store.mu.Lock()
	if len(req.RequiredStatuses) == 0 {
		delete(rp.protection, r.PathValue("branch"))
	} else {
		rp.protection[r.PathValue("branch")] = req.RequiredStatuses
	}
	s.store.mu.Unlock()
	writeNoContent(w, http.StatusNoContent)
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (s *Server) pullInfoLocked(pr *pullRequest) PullRequestInfo {
	now := s.now()
	return PullRequestInfo{
		Provider: pr.repo.provider, Repo: pr.repo.fullName(), RepoID: pr.repo.id, Number: pr.number,
		ID: pr.globalID, State: string(pr.state), Title: pr.title, Body: pr.body, Source: pr.source,
		Target: pr.target, Author: pr.author, HeadSHA: pr.headSHA, BaseSHA: pr.baseSHA,
		MergeCommitSHA: pr.mergeCommitSHA, SquashCommitSHA: pr.squashCommitSHA, PreviewMergeSHA: pr.previewMergeSHA,
		Labels:    slices.Clone(pr.labels),
		CreatedAt: pr.createdAt, UpdatedAt: pr.updatedAt, ClosedAt: timePtr(pr.closedAt),
		MergedAt: timePtr(pr.mergedAt), CompletionRequestedAt: timePtr(pr.completionRequestedAt),
		CompletionFailure: pr.completionFailure, Conflicts: pr.conflicts, Squashed: pr.squashed,
		MergeabilityKnown: pr.mergeabilityKnown(now), CompletionQueued: pr.completionQueued,
		AsyncMergePending: pr.asyncMergePending, HiddenFromLists: !pr.visibleInLists(now),
	}
}

// PullRequests returns the pull requests of repoID (all repositories when repoID is 0), oldest first.
func (s *Server) PullRequests(repoID int64) []PullRequestInfo {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	var out []PullRequestInfo
	for _, r := range s.store.sortedReposLocked() {
		if repoID != 0 && r.id != repoID {
			continue
		}
		for _, pr := range s.store.prs[r.id] {
			out = append(out, s.pullInfoLocked(pr))
		}
	}
	return out
}

func (s *Server) adminListPulls(w http.ResponseWriter, r *http.Request) {
	var repoID int64
	if v := r.URL.Query().Get("repo"); v != "" {
		var err error
		if repoID, err = strconv.ParseInt(v, 10, 64); err != nil {
			adminError(w, http.StatusBadRequest, fmt.Errorf("invalid repo: %w", err))
			return
		}
	}
	state := r.URL.Query().Get("state")
	out := slices.DeleteFunc(s.PullRequests(repoID), func(pr PullRequestInfo) bool {
		return state != "" && pr.State != state
	})
	if out == nil {
		out = []PullRequestInfo{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminPull(w http.ResponseWriter, r *http.Request) (*pullRequest, bool) {
	rp, ok := s.adminRepo(w, r)
	if !ok {
		return nil, false
	}
	number, err := strconv.Atoi(r.PathValue("number"))
	if err != nil {
		adminError(w, http.StatusBadRequest, fmt.Errorf("invalid number: %w", err))
		return nil, false
	}
	s.store.mu.Lock()
	pr, err := s.store.findPRLocked(rp, number)
	s.store.mu.Unlock()
	if err != nil {
		adminError(w, http.StatusNotFound, err)
		return nil, false
	}
	return pr, true
}

func (s *Server) adminGetPull(w http.ResponseWriter, r *http.Request) {
	pr, ok := s.adminPull(w, r)
	if !ok {
		return
	}
	s.store.mu.Lock()
	info := s.pullInfoLocked(pr)
	s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, info)
}

// farFuture hides a pull request from list endpoints until it is shown again.
var farFuture = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

func (s *Server) adminPullAction(w http.ResponseWriter, r *http.Request) {
	pr, ok := s.adminPull(w, r)
	if !ok {
		return
	}
	var req PullRequestAction
	if !decodeAdmin(w, r, &req) {
		return
	}
	ctx := r.Context()
	now := s.now()
	switch req.Action {
	case "merge":
		s.store.mu.Lock()
		merge := mergeRequest{
			pr: pr, message: defaultMergeMessage(pr, req.Message), squashMessage: defaultSquashMessage(pr, req.Message),
			author: actor(pr.repo.provider, "admin"), squash: req.Squash,
		}
		s.store.mu.Unlock()
		if _, err := s.landMerge(ctx, merge); err != nil {
			adminError(w, http.StatusConflict, err)
			return
		}
	case "close", "reopen", "set-labels", "hide", "show", "complete-now", "fail-completion":
		s.store.mu.Lock()
		before := pr.state
		err := applySimplePRAction(pr, req, now)
		after := pr.state
		s.store.mu.Unlock()
		if err != nil {
			adminError(w, http.StatusConflict, err)
			return
		}
		s.notifyPullRequestTransition(ctx, pr, before, after)
		if req.Action == "reopen" {
			if err := s.refreshPR(ctx, pr); err != nil {
				adminError(w, http.StatusInternalServerError, err)
				return
			}
		}
	case "delete":
		s.store.mu.Lock()
		s.store.prs[pr.repo.id] = slices.DeleteFunc(s.store.prs[pr.repo.id], func(p *pullRequest) bool { return p == pr })
		s.store.mu.Unlock()
		writeNoContent(w, http.StatusNoContent)
		return
	default:
		adminError(w, http.StatusBadRequest, fmt.Errorf("unknown action %q", req.Action))
		return
	}
	s.store.mu.Lock()
	info := s.pullInfoLocked(pr)
	s.store.mu.Unlock()
	writeJSON(w, http.StatusOK, info)
}

// applySimplePRAction applies an admin action that only changes stored state. The caller holds store.mu.
func applySimplePRAction(pr *pullRequest, req PullRequestAction, now time.Time) error {
	switch req.Action {
	case "close":
		if pr.state != prOpen {
			return errNotOpen
		}
		pr.state = prClosed
		pr.closedAt = now
		pr.completionQueued = false
	case "reopen":
		if pr.state != prClosed {
			return errors.New("only closed pull requests can be reopened")
		}
		pr.state = prOpen
		pr.closedAt = time.Time{}
	case "set-labels":
		pr.labels = slices.Clone(req.Labels)
	case "hide":
		pr.hiddenUntil = farFuture
	case "show":
		pr.hiddenUntil = time.Time{}
	case "complete-now":
		if !pr.completionQueued {
			return errors.New("no completion is queued")
		}
		pr.completionDueAt = now
	case "fail-completion":
		if !pr.completionQueued {
			return errors.New("no completion is queued")
		}
		pr.completionQueued = false
		pr.completionFailure = req.Message
		if pr.completionFailure == "" {
			pr.completionFailure = "The merge failed."
		}
	default:
		return fmt.Errorf("unknown action %q", req.Action)
	}
	pr.updatedAt = now
	return nil
}

// CommitStatuses returns the statuses of repoID, optionally filtered to sha, oldest first.
func (s *Server) CommitStatuses(repoID int64, sha string) []CommitStatusInfo {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	var out []CommitStatusInfo
	for _, r := range s.store.sortedReposLocked() {
		if repoID != 0 && r.id != repoID {
			continue
		}
		for _, cs := range s.store.statuses[r.id] {
			if sha != "" && cs.sha != sha {
				continue
			}
			out = append(out, CommitStatusInfo{
				ID: cs.id, RepoID: r.id, Repo: r.fullName(), SHA: cs.sha, Context: cs.context, State: cs.state,
				Description: cs.description, TargetURL: cs.targetURL, Status: cs.status, Conclusion: cs.conclusion,
				CreatedAt: cs.createdAt, UpdatedAt: cs.updatedAt,
			})
		}
	}
	return out
}

func (s *Server) adminListStatuses(w http.ResponseWriter, r *http.Request) {
	var repoID int64
	if v := r.URL.Query().Get("repo"); v != "" {
		var err error
		if repoID, err = strconv.ParseInt(v, 10, 64); err != nil {
			adminError(w, http.StatusBadRequest, fmt.Errorf("invalid repo: %w", err))
			return
		}
	}
	out := s.CommitStatuses(repoID, r.URL.Query().Get("sha"))
	if out == nil {
		out = []CommitStatusInfo{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminSetBehavior(w http.ResponseWriter, r *http.Request) {
	p := Provider(r.PathValue("provider"))
	if !p.Valid() {
		adminError(w, http.StatusNotFound, fmt.Errorf("unknown provider %q", p))
		return
	}
	behavior := s.behavior.get(p)
	if !decodeAdmin(w, r, &behavior) {
		return
	}
	s.behavior.set(p, behavior)
	writeJSON(w, http.StatusOK, behavior)
}

func (s *Server) adminAddFault(w http.ResponseWriter, r *http.Request) {
	var rule FaultRule
	if !decodeAdmin(w, r, &rule) {
		return
	}
	added, err := s.faults.add(rule)
	if err != nil {
		adminError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, added)
}

func (s *Server) adminAddWebhook(w http.ResponseWriter, r *http.Request) {
	var sub WebhookSubscription
	if !decodeAdmin(w, r, &sub) {
		return
	}
	if !sub.Provider.Valid() || sub.URL == "" {
		adminError(w, http.StatusBadRequest, errors.New("a valid provider and url are required"))
		return
	}
	writeJSON(w, http.StatusCreated, s.webhooks.add(sub))
}
