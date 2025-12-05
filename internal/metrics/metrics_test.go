package metrics

import (
	"testing"
	"time"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRecordSCMCall(t *testing.T) {
	t.Parallel()
	repo := &v1alpha1.GitRepository{
		ObjectMeta: metav1.ObjectMeta{Name: "repo1"},
		Spec: v1alpha1.GitRepositorySpec{
			ScmProviderRef: v1alpha1.ScmProviderObjectReference{Name: "github"},
		},
	}

	labels := prometheus.Labels{
		"git_repository": "repo1",
		"scm_provider":   "github",
		"api":            string(SCMAPICommitStatus),
		"operation":      string(SCMOperationCreate),
		"response_code":  "200",
	}

	rateLimitLabels := prometheus.Labels{
		"scm_provider": "github",
	}

	tests := []struct {
		rateLimit               *RateLimit
		name                    string
		countTotal              float64
		rateLimitLimit          float64
		rateLimitRemaining      float64
		rateLimitResetRemaining float64
	}{
		{
			name: "increments metrics",
			rateLimit: &RateLimit{
				Limit:          10,
				Remaining:      5,
				ResetRemaining: 30 * time.Second,
			},
			countTotal:              1.0,
			rateLimitLimit:          10,
			rateLimitRemaining:      5,
			rateLimitResetRemaining: 30,
		},
		{
			name:       "increments metrics",
			rateLimit:  nil,
			countTotal: 2.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			RecordSCMCall(repo, SCMAPICommitStatus, SCMOperationCreate, 200, 1*time.Second, tt.rateLimit)
			if got := testutil.ToFloat64(scmCallsTotal.With(labels)); got != tt.countTotal {
				t.Errorf("scmCallsTotal = %v, want %v", got, tt.countTotal)
			}
			if tt.rateLimit != nil {
				if got := testutil.ToFloat64(scmCallsRateLimitLimit.With(rateLimitLabels)); got != tt.rateLimitLimit {
					t.Errorf("scmCallsRateLimitLimit = %v, want %v", got, tt.rateLimitLimit)
				}
				if got := testutil.ToFloat64(scmCallsRateLimitRemaining.With(rateLimitLabels)); got != tt.rateLimitRemaining {
					t.Errorf("scmCallsRateLimitRemaining = %v, want %v", got, tt.rateLimitRemaining)
				}
				if got := testutil.ToFloat64(scmCallsRateLimitResetRemainingSeconds.With(rateLimitLabels)); got != tt.rateLimitResetRemaining {
					t.Errorf("scmCallsRateLimitResetRemainingSeconds = %v, want %v", got, tt.rateLimitResetRemaining)
				}
			}
		})
	}
}
