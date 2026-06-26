/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package apicallmetrics

import (
	"context"
	"net/http"
	"testing"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSCMCallSnapshotAndDelta(t *testing.T) {
	t.Parallel()

	repo := &promoterv1alpha1.GitRepository{
		ObjectMeta: metav1.ObjectMeta{Name: "scm-metrics-test-gr", Namespace: "default"},
		Spec: promoterv1alpha1.GitRepositorySpec{
			ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{Name: "scm-test"},
		},
	}
	other := &promoterv1alpha1.GitRepository{
		ObjectMeta: metav1.ObjectMeta{Name: "other-gr", Namespace: "default"},
		Spec: promoterv1alpha1.GitRepositorySpec{
			ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{Name: "scm-other"},
		},
	}

	before := scmCallSnapshot(repo)
	ctx := context.Background()
	metrics.RecordSCMCall(ctx, repo, metrics.SCMAPIPullRequest, metrics.SCMOperationCreate, http.StatusOK, time.Millisecond, nil)
	metrics.RecordSCMCall(ctx, repo, metrics.SCMAPIPullRequest, metrics.SCMOperationList, http.StatusOK, time.Millisecond, nil)
	metrics.RecordSCMCall(ctx, repo, metrics.SCMAPICommitStatus, metrics.SCMOperationSet, http.StatusOK, time.Millisecond, nil)
	metrics.RecordSCMCall(ctx, other, metrics.SCMAPIPullRequest, metrics.SCMOperationCreate, http.StatusOK, time.Millisecond, nil)

	after := scmCallSnapshot(repo)
	delta := scmCallDelta(before, after)

	require.Equal(t, float64(1), delta["PullRequest/create"])
	require.Equal(t, float64(1), delta["PullRequest/list"])
	require.Equal(t, float64(1), delta["CommitStatus/set"])
	require.Equal(t, float64(3), scmCallTotal(delta))
}
