package fake

import (
	"context"
	"errors"
	"fmt"
	"sync"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	v1 "k8s.io/api/core/v1"
)

var (
	commitStatuses map[string]*promoterv1alpha1.CommitStatus
	mutexCS        sync.RWMutex
)

type CommitStatus struct{}

var _ scms.CommitStatusProvider = &CommitStatus{}

func NewFakeCommitStatusProvider(secret v1.Secret) (*CommitStatus, error) {
	return &CommitStatus{}, nil
}

func (cs CommitStatus) Set(ctx context.Context, commitStatus *promoterv1alpha1.CommitStatus) (*promoterv1alpha1.CommitStatus, error) {
	if commitStatus.Spec.Sha == "" {
		return nil, errors.New("sha is required")
	}
	commitStatus.Status.Phase = commitStatus.Spec.Phase
	cs.savePointer(commitStatus)
	return commitStatus, nil
}

func (cs *CommitStatus) savePointer(commitStatus *promoterv1alpha1.CommitStatus) {
	mutexCS.Lock()
	defer mutexCS.Unlock()
	if commitStatuses == nil {
		commitStatuses = make(map[string]*promoterv1alpha1.CommitStatus)
	}
	if _, ok := commitStatuses[cs.getMapKey(commitStatus)]; !ok {
		commitStatus.Status.Id = fmt.Sprintf("%d", len(commitStatuses)+1)
		commitStatus.Status.Sha = commitStatus.Spec.Sha
		commitStatuses[cs.getMapKey(commitStatus)] = commitStatus
	}
}

func (cs *CommitStatus) getMapKey(commitStatus *promoterv1alpha1.CommitStatus) string {
	return fmt.Sprintf("%s/%s", commitStatus.Spec.Sha, commitStatus.Status.Id)
}
