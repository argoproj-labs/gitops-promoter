package fake

import (
	"context"
	"net/http"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
)

func recordFakeSCMCall(ctx context.Context, gitRepo *promoterv1alpha1.GitRepository, api metrics.SCMAPI, operation metrics.SCMOperation, start time.Time, statusCode int) {
	if gitRepo == nil {
		return
	}
	metrics.RecordSCMCall(ctx, gitRepo, api, operation, statusCode, time.Since(start), nil)
}

func scmStatusFromError(err error) int {
	if err == nil {
		return http.StatusOK
	}
	return http.StatusInternalServerError
}
