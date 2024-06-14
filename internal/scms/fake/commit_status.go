package fake

import (
	"context"
	"fmt"
	promoterv1alpha1 "github.com/zachaller/promoter/api/v1alpha1"
	"github.com/zachaller/promoter/internal/scms"
	v1 "k8s.io/api/core/v1"
	"sync"
)

var commitStatuses map[string]*promoterv1alpha1.CommitStatus
var mutexCS sync.RWMutex

type CommitStatus struct {
}

var _ scms.CommitStatusProvider = &CommitStatus{}

func NewFakeCommitStatusProvider(secret v1.Secret) (*CommitStatus, error) {
	return &CommitStatus{}, nil
}

func (cs CommitStatus) Set(ctx context.Context, commitStatus *promoterv1alpha1.CommitStatus) (*promoterv1alpha1.CommitStatus, error) {
	err := cs.savePointer(ctx, commitStatus)
	if err != nil {
		return nil, err
	}
	return commitStatus, nil
}

func (cs *CommitStatus) savePointer(ctx context.Context, commitStatus *promoterv1alpha1.CommitStatus) error {
	mutexPR.Lock()
	defer mutexPR.Unlock()
	if commitStatuses == nil {
		commitStatuses = make(map[string]*promoterv1alpha1.CommitStatus)
	}
	if _, ok := commitStatuses[cs.getMapKey(commitStatus)]; !ok {
		commitStatus.Status.Id = fmt.Sprintf("%d", len(commitStatuses)+1)
		commitStatuses[cs.getMapKey(commitStatus)] = commitStatus
	}
	return nil
}

func (cs *CommitStatus) getMapKey(commitStatus *promoterv1alpha1.CommitStatus) string {
	return fmt.Sprintf("%s/%s", commitStatus.Spec.Sha, commitStatus.Status.Id)
}
