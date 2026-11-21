package forgejo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v2"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// StatusSkipped is a constant for forgejo status skipped as it's listed on their API docs but not in the go client
const StatusSkipped = "skipped"

// CheckParser implements the scms.SCMCheckParser interface for Forgejo.
type CheckParser struct {
	client    *forgejo.Client
	k8sClient client.Client
}

// NewForgejoCheckParser creates a new CheckParser for Forgejo
func NewForgejoCheckParser(ctx context.Context, k8sClient client.Client, secret v1.Secret, domain string) (*CheckParser, error) {
	client, err := GetClient(domain, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to create Forgejo client: %w", err)
	}

	return &CheckParser{
		client:    client,
		k8sClient: k8sClient,
	}, nil
}

// Get returns a list of checks from a Forgejo PR based on the most recent commit in the PR
func (c *CheckParser) Get(ctx context.Context, ps v1alpha1.PromotionStrategy, branch string) ([]scms.SCMCheckResult, error) {
	logger := log.FromContext(ctx)
	logger.V(4).Info("Retrieving checks from SCM")

	branchID, err := utils.GetPullRequestIDForBranch(ps, branch)
	if err != nil {
		return nil, fmt.Errorf("failed to get pull request id: %w", err)
	}
	prNumber, err := strconv.ParseInt(branchID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to convert PR ID to int64: %w", err)
	}

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, c.k8sClient, client.ObjectKey{
		Namespace: ps.Namespace,
		Name:      ps.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get repo: %w", err)
	}

	start := time.Now()
	pr, response, err := c.client.GetPullRequest(repo.Spec.Forgejo.Owner, repo.Spec.Forgejo.Name, prNumber)
	if response != nil {
		metrics.RecordSCMCall(ctx, repo, metrics.SCMAPIPullRequest, metrics.SCMOperationGet, response.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		if response != nil && response.StatusCode == http.StatusNotFound {
			return nil, errors.New("forgejo pull request was not found")
		}
		return nil, fmt.Errorf("failed to get pull request: %w", err)
	}
	if pr == nil {
		return nil, errors.New("retrieved pull request is empty")
	}

	start = time.Now()
	commitCheckList, response, err := c.client.ListStatuses(repo.Spec.Forgejo.Owner, repo.Spec.Forgejo.Name, pr.Head.Sha, forgejo.ListStatusesOption{})
	if response != nil {
		metrics.RecordSCMCall(ctx, repo, metrics.SCMChecks, metrics.SCMOperationList, response.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list SCM checks: %w", err)
	}

	// Forgejo stores all attempts as a check so filter out newest
	filteredChecks := filterCommitCheckListToNewestCheck(commitCheckList)
	var checks []scms.SCMCheckResult
	for _, check := range filteredChecks {
		if check == nil {
			return nil, errors.New("received null check from list")
		}

		checks = append(checks, scms.SCMCheckResult{
			Name:      check.Context,
			Status:    determineCheckStatusFromForgejoStatus(*check),
			SCMStatus: string(check.State),
			URL:       check.TargetURL,
		})
	}

	return checks, nil
}

// filterCommitCheckListToNewestCheck filters out older versions of a check.
func filterCommitCheckListToNewestCheck(list []*forgejo.Status) []*forgejo.Status {
	ids := make(map[string]int64)
	statuses := make(map[string]*forgejo.Status)

	for _, check := range list {
		if check == nil {
			continue
		}

		storedID, ok := ids[check.Context]
		if !ok {
			ids[check.Context] = check.ID
			statuses[check.Context] = check
			continue
		}

		if check.ID > storedID {
			ids[check.Context] = check.ID
			statuses[check.Context] = check
			continue
		}
	}

	checks := make([]*forgejo.Status, 0, len(statuses))
	for _, status := range statuses {
		checks = append(checks, status)
	}
	return checks
}

// determineCheckStatusFromForgejoStatus is a helper function that determines the check status based on a merge
// check's status
func determineCheckStatusFromForgejoStatus(check forgejo.Status) scms.SCMCheckStatus {
	switch check.State {
	case forgejo.StatusSuccess, forgejo.StatusState(StatusSkipped):
		return scms.SCMCheckStatusSucceeded
	case forgejo.StatusFailure, forgejo.StatusError, forgejo.StatusWarning:
		return scms.SCMCheckStatusFailed
	default:
		return scms.SCMCheckStatusPending
	}
}
