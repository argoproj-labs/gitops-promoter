package fake

import (
	"context"
	"fmt"
	"sync"

	"github.com/zachaller/promoter/api/v1alpha1"
)

var pullRequests map[string]*v1alpha1.PullRequest
var mutexPR sync.RWMutex

type PullRequest struct {
}

func NewFakePullRequestProvider() *PullRequest {
	return &PullRequest{}
}

func (pr *PullRequest) Create(ctx context.Context, title, head, base, description string, pullRequest *v1alpha1.PullRequest) error {
	pullRequest.Spec.Title = title
	pullRequest.Spec.SourceBranch = head
	pullRequest.Spec.TargetBranch = base
	pullRequest.Spec.Description = description
	return pr.savePointer(ctx, pullRequest)
}

func (pr *PullRequest) Update(ctx context.Context, title, description string, pullRequest *v1alpha1.PullRequest) error {
	pullRequest.Spec.Title = title
	pullRequest.Spec.Description = description
	return pr.savePointer(ctx, pullRequest)
}

func (pr *PullRequest) Close(ctx context.Context, pullRequest *v1alpha1.PullRequest) error {
	return pr.savePointer(ctx, pullRequest)
}

func (pr *PullRequest) Merge(ctx context.Context, commitMessage string, pullRequest *v1alpha1.PullRequest) error {
	return pr.savePointer(ctx, pullRequest)
}

func (pr *PullRequest) FindOpen(ctx context.Context, pullRequest *v1alpha1.PullRequest) (bool, error) {
	return pr.findOpen(ctx, pullRequest), nil
}

func (pr *PullRequest) savePointer(ctx context.Context, pullRequest *v1alpha1.PullRequest) error {
	mutexPR.Lock()
	defer mutexPR.Unlock()
	if pullRequests == nil {
		pullRequests = make(map[string]*v1alpha1.PullRequest)
	}
	if _, ok := pullRequests[pr.getMapKey(pullRequest)]; !ok {
		pullRequest.Status.ID = fmt.Sprintf("%d", len(pullRequests)+1)
		pullRequests[pr.getMapKey(pullRequest)] = pullRequest
	}
	return nil
}

func (pr *PullRequest) findOpen(ctx context.Context, pullRequest *v1alpha1.PullRequest) bool {
	mutexPR.RLock()
	defer mutexPR.RUnlock()
	if _, ok := pullRequests[pr.getMapKey(pullRequest)]; ok {
		if pullRequests[pr.getMapKey(pullRequest)].Status.State == "open" {
			return true
		}
	}
	return false
}

func (pr *PullRequest) getMapKey(pullRequest *v1alpha1.PullRequest) string {
	return fmt.Sprintf("%s/%s/%s", pullRequest.Spec.SourceBranch, pullRequest.Spec.TargetBranch, pullRequest.Status.ID)
}
