package scmserver

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// prState is the provider-neutral lifecycle state of a pull request.
type prState string

const (
	prOpen   prState = "open"
	prClosed prState = "closed"
	prMerged prState = "merged"
)

var errNotFound = errors.New("not found")

// repo is a repository hosted by the server. Fields other than gitMu are guarded by store.mu.
type repo struct {
	createdAt     time.Time
	protection    map[string][]string
	provider      Provider
	owner         string
	project       string
	name          string
	uuid          string
	projectUUID   string
	defaultBranch string
	dir           string
	labels        []*label
	// gitMu serializes git writes (pushes and merges) so ref snapshots taken around a write are attributable to it.
	gitMu      sync.Mutex
	id         int64
	nextNumber int
}

// fullName is the provider-visible path of the repository, e.g. "owner/name" or "org/project/name".
func (r *repo) fullName() string {
	if r.provider == AzureDevOps {
		return r.owner + "/" + r.project + "/" + r.name
	}
	return r.owner + "/" + r.name
}

type label struct {
	name        string
	color       string
	description string
	id          int64
}

// pullRequest is a pull request (merge request) on a hosted repository. All fields are guarded by store.mu.
type pullRequest struct {
	createdAt             time.Time
	updatedAt             time.Time
	closedAt              time.Time
	mergedAt              time.Time
	mergeableAt           time.Time
	hiddenUntil           time.Time
	completionRequestedAt time.Time
	completionDueAt       time.Time
	asyncMergeDueAt       time.Time
	repo                  *repo
	state                 prState
	title                 string
	body                  string
	source                string
	target                string
	author                string
	headSHA               string
	baseSHA               string
	mergeCommitSHA        string
	previewMergeSHA       string
	completionMessage     string
	completionSourceSHA   string
	completionFailure     string
	asyncMergeTaskID      string
	asyncMergeMessage     string
	labels                []string
	globalID              int64
	number                int
	conflicts             bool
	// completionQueued is true while an Azure DevOps completion request is waiting to be processed.
	completionQueued bool
	// asyncMergePending is true while a Bitbucket asynchronous merge task is running.
	asyncMergePending bool
}

// mergeabilityKnown reports whether the provider has finished computing mergeability for the current head.
func (pr *pullRequest) mergeabilityKnown(now time.Time) bool {
	return !now.Before(pr.mergeableAt)
}

// visibleInLists reports whether list/search endpoints return the pull request yet.
func (pr *pullRequest) visibleInLists(now time.Time) bool {
	return !now.Before(pr.hiddenUntil)
}

// commitStatus is a commit status, build status or check run. All fields are guarded by store.mu.
type commitStatus struct {
	createdAt   time.Time
	updatedAt   time.Time
	startedAt   time.Time
	completedAt time.Time
	sha         string
	context     string
	state       string
	description string
	targetURL   string
	status      string
	conclusion  string
	title       string
	summary     string
	genre       string
	name        string
	id          int64
	repoID      int64
}

// githubApp is a registered GitHub App whose JWTs are verified with publicKeyPEM.
type githubApp struct {
	publicKeyPEM  []byte
	installations []githubInstallation
	id            int64
}

type githubInstallation struct {
	account string
	id      int64
}

type installationToken struct {
	expiresAt      time.Time
	installationID int64
}

// store holds all server state.
type store struct {
	repos         map[int64]*repo
	prs           map[int64][]*pullRequest
	statuses      map[int64][]*commitStatus
	azureNextPR   map[string]int
	tokens        map[Provider]map[string]bool
	basicAuth     map[Provider]map[string]string
	apps          map[int64]*githubApp
	installTokens map[string]installationToken
	mu            sync.Mutex
	nextID        int64
}

func newStore() *store {
	s := &store{}
	s.resetLocked()
	return s
}

func (s *store) resetLocked() {
	s.repos = map[int64]*repo{}
	s.prs = map[int64][]*pullRequest{}
	s.statuses = map[int64][]*commitStatus{}
	s.azureNextPR = map[string]int{}
	s.tokens = map[Provider]map[string]bool{}
	s.basicAuth = map[Provider]map[string]string{}
	s.apps = map[int64]*githubApp{}
	s.installTokens = map[string]installationToken{}
	s.nextID = 1000
}

// allocIDLocked returns a new globally unique numeric identifier.
func (s *store) allocIDLocked() int64 {
	s.nextID++
	return s.nextID
}

// findRepoLocked finds a repository by provider-native identity. Names are matched case-insensitively, as on the
// real providers.
func (s *store) findRepoLocked(p Provider, owner, project, name string) (*repo, error) {
	name = strings.TrimSuffix(name, ".git")
	for _, r := range s.repos {
		if r.provider != p || !strings.EqualFold(r.owner, owner) || !strings.EqualFold(r.name, name) {
			continue
		}
		if p == AzureDevOps && !strings.EqualFold(r.project, project) && !strings.EqualFold(r.projectUUID, project) {
			continue
		}
		return r, nil
	}
	return nil, errNotFound
}

// findRepoByIDLocked finds a repository by its numeric ID, restricted to provider p.
func (s *store) findRepoByIDLocked(p Provider, id int64) (*repo, error) {
	r, ok := s.repos[id]
	if !ok || r.provider != p {
		return nil, errNotFound
	}
	return r, nil
}

// findAzureRepoLocked finds an Azure DevOps repository by project (name or ID) and repository (name or ID).
func (s *store) findAzureRepoLocked(org, project, repository string) (*repo, error) {
	for _, r := range s.repos {
		if r.provider != AzureDevOps || !strings.EqualFold(r.owner, org) {
			continue
		}
		if !strings.EqualFold(r.project, project) && !strings.EqualFold(r.projectUUID, project) {
			continue
		}
		if strings.EqualFold(r.name, repository) || strings.EqualFold(r.uuid, repository) {
			return r, nil
		}
	}
	return nil, errNotFound
}

func (s *store) sortedReposLocked() []*repo {
	out := make([]*repo, 0, len(s.repos))
	for _, r := range s.repos {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b *repo) int { return int(a.id - b.id) })
	return out
}

// findPRLocked finds a pull request by its provider-visible number.
func (s *store) findPRLocked(r *repo, number int) (*pullRequest, error) {
	for _, pr := range s.prs[r.id] {
		if pr.number == number {
			return pr, nil
		}
	}
	return nil, errNotFound
}

// findAzurePRLocked finds an Azure DevOps pull request by its organization-wide ID within a project.
func (s *store) findAzurePRLocked(org, project string, id int) (*pullRequest, error) {
	for _, r := range s.repos {
		if r.provider != AzureDevOps || !strings.EqualFold(r.owner, org) {
			continue
		}
		if !strings.EqualFold(r.project, project) && !strings.EqualFold(r.projectUUID, project) {
			continue
		}
		if pr, err := s.findPRLocked(r, id); err == nil {
			return pr, nil
		}
	}
	return nil, errNotFound
}

// openPRForBranchesLocked returns the open pull request from source into target, if any.
func (s *store) openPRForBranchesLocked(r *repo, source, target string) *pullRequest {
	for _, pr := range s.prs[r.id] {
		if pr.state == prOpen && pr.source == source && pr.target == target {
			return pr
		}
	}
	return nil
}

// newPRLocked creates a pull request and assigns its provider-visible number.
func (s *store) newPRLocked(r *repo, title, body, source, target, author string, now time.Time) *pullRequest {
	var number int
	if r.provider == AzureDevOps {
		// Azure DevOps pull request IDs are unique across the organization.
		key := strings.ToLower(r.owner)
		s.azureNextPR[key]++
		number = s.azureNextPR[key]
	} else {
		r.nextNumber++
		number = r.nextNumber
	}
	pr := &pullRequest{
		repo:      r,
		number:    number,
		globalID:  s.allocIDLocked(),
		title:     title,
		body:      body,
		source:    source,
		target:    target,
		author:    author,
		state:     prOpen,
		createdAt: now,
		updatedAt: now,
	}
	s.prs[r.id] = append(s.prs[r.id], pr)
	return pr
}

// labelLocked returns the repository label with the given name (case-insensitive), or nil.
func (r *repo) labelLocked(name string) *label {
	for _, l := range r.labels {
		if strings.EqualFold(l.name, name) {
			return l
		}
	}
	return nil
}

// ensureLabelLocked returns the repository label with the given name, creating it if needed.
func (s *store) ensureLabelLocked(r *repo, name, color string) *label {
	if l := r.labelLocked(name); l != nil {
		return l
	}
	l := &label{id: s.allocIDLocked(), name: name, color: color}
	r.labels = append(r.labels, l)
	return l
}

func addLabel(labels []string, name string) []string {
	for _, l := range labels {
		if strings.EqualFold(l, name) {
			return labels
		}
	}
	return append(labels, name)
}

func removeLabel(labels []string, name string) ([]string, bool) {
	for i, l := range labels {
		if strings.EqualFold(l, name) {
			return slices.Delete(labels, i, i+1), true
		}
	}
	return labels, false
}

// statusesLocked returns the statuses recorded for sha in r, oldest first.
func (s *store) statusesLocked(r *repo, sha string) []*commitStatus {
	out := []*commitStatus{}
	for _, cs := range s.statuses[r.id] {
		if cs.sha == sha {
			out = append(out, cs)
		}
	}
	return out
}

// latestStatusLocked returns the most recent status for context on sha, or nil.
func (s *store) latestStatusLocked(r *repo, sha, context string) *commitStatus {
	var latest *commitStatus
	for _, cs := range s.statuses[r.id] {
		if cs.sha == sha && cs.context == context {
			latest = cs
		}
	}
	return latest
}

func (s *store) addStatusLocked(r *repo, cs *commitStatus) *commitStatus {
	cs.id = s.allocIDLocked()
	cs.repoID = r.id
	s.statuses[r.id] = append(s.statuses[r.id], cs)
	return cs
}

func (s *store) findStatusByIDLocked(r *repo, id int64) *commitStatus {
	for _, cs := range s.statuses[r.id] {
		if cs.id == id {
			return cs
		}
	}
	return nil
}

func newGUID() string {
	return uuid.NewString()
}
