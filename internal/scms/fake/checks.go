package fake

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var _ scms.CheckParser = &CheckParser{}

// Below constants are a list of fake statuses
const (
	CheckStatusSuccessful = "success"
	CheckStatusFailed     = "failure"
	CheckStatusInProgress = "inProgress"
	CheckStatusPending    = "pending"
	CheckStatusCancelled  = "cancelled"
	CheckStatusSkipped    = "skipped"
)

// CheckParser implements the scms.CheckParser interface for the fake scm.
type CheckParser struct {
	k8sClient client.Client
}

// Check represents a fake check
type Check struct {
	Name   string
	Status string
	URL    string
}

var (
	checks = map[string][]Check{}
	mu     sync.RWMutex
)

// NewFakeCheckParser creates a new fake scm check parser
func NewFakeCheckParser(k8sClient client.Client) *CheckParser {
	return &CheckParser{k8sClient: k8sClient}
}

// Get returns the check stored for an environment in that repo.
func (c *CheckParser) Get(ctx context.Context, ps v1alpha1.PromotionStrategy, env string) ([]scms.SCMCheckResult, error) {
	logger := log.FromContext(ctx)
	logger.V(4).Info("Retrieving checks from SCM")

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, c.k8sClient, client.ObjectKey{Namespace: ps.Namespace, Name: ps.Spec.RepositoryReference.Name})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	mu.RLock()
	defer mu.RUnlock()

	key := getCheckMapKey(*gitRepo, env)
	toParse, ok := checks[key]
	if !ok {
		return []scms.SCMCheckResult{}, nil
	}

	determineSCMCheckStatus := func(s string) scms.SCMCheckStatus {
		switch s {
		case CheckStatusSuccessful, CheckStatusSkipped:
			return scms.SCMCheckStatusSucceeded
		case CheckStatusFailed, CheckStatusCancelled:
			return scms.SCMCheckStatusFailed
		default:
			return scms.SCMCheckStatusPending
		}
	}

	results := []scms.SCMCheckResult{}
	for _, check := range toParse {
		results = append(results, scms.SCMCheckResult{
			Name:      check.Name,
			Status:    determineSCMCheckStatus(check.Status),
			SCMStatus: check.Status,
			URL:       check.URL,
		})
	}

	return results, nil
}

// SetChecks writes the provided checks to the map of fake checks. It overwrites what is previously there for a repo.
func SetChecks(ctx context.Context, k8sClient client.Client, ps v1alpha1.PromotionStrategy, branch string, fakeChecks []Check) error {
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, k8sClient, client.ObjectKey{Namespace: ps.Namespace, Name: ps.Spec.RepositoryReference.Name})
	if err != nil {
		return fmt.Errorf("failed to get GitRepository: %w", err)
	}

	mu.Lock()
	defer mu.Unlock()

	key := getCheckMapKey(*gitRepo, branch)
	checks[key] = slices.Clone(fakeChecks)
	return nil
}

// getCheckMapKey returns a key to use for the repo in the map of checks
func getCheckMapKey(gitRepo v1alpha1.GitRepository, branch string) string {
	return fmt.Sprintf("%s/%s/%s", gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, branch)
}
