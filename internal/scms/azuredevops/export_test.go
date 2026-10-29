package azuredevops

import (
	"context"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

func AzureDevOpsHTTPStatusCode(err error) (int, bool) {
	return azureDevOpsHTTPStatusCode(err)
}

func IsAzureDevOpsNotFound(err error) bool {
	return isAzureDevOpsNotFound(err)
}

// AzdoGitClient exposes azdoGitClient to package azuredevops_test.
type AzdoGitClient = azdoGitClient

// SetPullRequestStatusBestEffort exposes setPullRequestStatusBestEffort to package azuredevops_test.
func (cs CommitStatus) SetPullRequestStatusBestEffort(
	ctx context.Context,
	gitClient AzdoGitClient,
	gitRepo *v1alpha1.GitRepository,
	commitStatus *v1alpha1.CommitStatus,
) {
	cs.setPullRequestStatusBestEffort(ctx, gitClient, gitRepo, commitStatus)
}

func SetWorkloadIdentityTokenFunc(f func(context.Context, *v1alpha1.AzureWorkloadIdentity) (string, error)) (restore func()) {
	original := workloadIdentityTokenFunc
	workloadIdentityTokenFunc = f
	return func() { workloadIdentityTokenFunc = original }
}
