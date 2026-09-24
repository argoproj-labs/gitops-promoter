package azuredevops

import (
	"context"
	"errors"
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

type fakeAzdoGitClient struct {
	pullRequests           []git.GitPullRequest
	pullRequestErr         error
	iterations             []git.GitPullRequestIteration
	iterationErr           error
	pullRequestStatusErr   error
	pullRequestCalls       int
	iterationCalls         int
	pullRequestStatusCalls int
	lastPullRequestStatus  git.CreatePullRequestStatusArgs
}

func (f *fakeAzdoGitClient) CreateCommitStatus(context.Context, git.CreateCommitStatusArgs) (*git.GitStatus, error) {
	return nil, errors.New("unexpected CreateCommitStatus call")
}

func (f *fakeAzdoGitClient) GetPullRequests(context.Context, git.GetPullRequestsArgs) (*[]git.GitPullRequest, error) {
	f.pullRequestCalls++
	if f.pullRequestErr != nil {
		return nil, f.pullRequestErr
	}
	return &f.pullRequests, nil
}

func (f *fakeAzdoGitClient) GetPullRequestIterations(context.Context, git.GetPullRequestIterationsArgs) (*[]git.GitPullRequestIteration, error) {
	f.iterationCalls++
	if f.iterationErr != nil {
		return nil, f.iterationErr
	}
	return &f.iterations, nil
}

func (f *fakeAzdoGitClient) CreatePullRequestStatus(_ context.Context, args git.CreatePullRequestStatusArgs) (*git.GitPullRequestStatus, error) {
	f.pullRequestStatusCalls++
	f.lastPullRequestStatus = args
	if f.pullRequestStatusErr != nil {
		return nil, f.pullRequestStatusErr
	}
	id := 99
	return &git.GitPullRequestStatus{Id: &id}, nil
}

const testSHA = "abcdef1234567890abcdef1234567890abcdef12"

func testGitRepository() *v1alpha1.GitRepository {
	return &v1alpha1.GitRepository{
		Spec: v1alpha1.GitRepositorySpec{
			AzureDevOps: &v1alpha1.AzureDevOpsRepo{Name: "repo", Project: "project"},
		},
	}
}

func testCommitStatus(phase v1alpha1.CommitStatusPhase) *v1alpha1.CommitStatus {
	return &v1alpha1.CommitStatus{
		Namespace: "default",
		Spec: v1alpha1.CommitStatusSpec{
			RepositoryReference: v1alpha1.ObjectReference{Name: "repo"},
			Sha:                 testSHA,
			Name:                "health/dev",
			Description:         "healthy",
			Phase:               phase,
			Url:                 "https://example.com/details",
		},
	}
}

func openPullRequest(id int, headSHA string) git.GitPullRequest {
	return git.GitPullRequest{
		PullRequestId:         &id,
		LastMergeSourceCommit: &git.GitCommitRef{CommitId: &headSHA},
	}
}

func TestSetPullRequestStatusBestEffort(t *testing.T) {
	t.Parallel()

	iterationID := 7
	iterationSHA := testSHA
	otherSHA := "0000000000000000000000000000000000000000"
	gitClient := &fakeAzdoGitClient{
		pullRequests: []git.GitPullRequest{
			openPullRequest(41, otherSHA),
			openPullRequest(42, testSHA),
		},
		iterations: []git.GitPullRequestIteration{{
			Id:              &iterationID,
			SourceRefCommit: &git.GitCommitRef{CommitId: &iterationSHA},
		}},
	}

	commitStatus := testCommitStatus(v1alpha1.CommitPhaseSuccess)
	CommitStatus{}.setPullRequestStatusBestEffort(context.Background(), gitClient, testGitRepository(), commitStatus)

	if gitClient.iterationCalls != 1 {
		t.Fatalf("expected one iteration lookup, got %d", gitClient.iterationCalls)
	}
	if gitClient.pullRequestStatusCalls != 1 {
		t.Fatalf("expected one PR status create, got %d", gitClient.pullRequestStatusCalls)
	}
	args := gitClient.lastPullRequestStatus
	if args.PullRequestId == nil || *args.PullRequestId != 42 {
		t.Fatalf("expected PR 42, got %v", args.PullRequestId)
	}
	if args.Status == nil || args.Status.IterationId == nil || *args.Status.IterationId != iterationID {
		t.Fatalf("expected iteration %d, got %#v", iterationID, args.Status)
	}
	if args.Status.Context == nil || args.Status.Context.Name == nil || *args.Status.Context.Name != commitStatus.Spec.Name {
		t.Fatalf("unexpected status context: %#v", args.Status.Context)
	}
	if args.Status.State == nil || *args.Status.State != git.GitStatusStateValues.Succeeded {
		t.Fatalf("unexpected status state: %#v", args.Status.State)
	}
}

func TestSetPullRequestStatusBestEffortIgnoresFailuresAndMissingPRs(t *testing.T) {
	t.Parallel()

	commitStatus := testCommitStatus(v1alpha1.CommitPhasePending)
	provider := CommitStatus{}
	repo := testGitRepository()

	// No active PR has this SHA at its head, so nothing else is queried.
	noMatch := &fakeAzdoGitClient{pullRequests: []git.GitPullRequest{openPullRequest(41, "0000000000000000000000000000000000000000")}}
	provider.setPullRequestStatusBestEffort(context.Background(), noMatch, repo, commitStatus)
	if noMatch.iterationCalls != 0 || noMatch.pullRequestStatusCalls != 0 {
		t.Fatalf("unexpected Azure DevOps calls without a matching PR")
	}

	// A failed PR listing is swallowed.
	listFailure := &fakeAzdoGitClient{pullRequestErr: errors.New("temporary failure")}
	provider.setPullRequestStatusBestEffort(context.Background(), listFailure, repo, commitStatus)
	if listFailure.iterationCalls != 0 || listFailure.pullRequestStatusCalls != 0 {
		t.Fatalf("unexpected Azure DevOps calls after a failed listing")
	}

	// A failed iteration lookup is swallowed too.
	iterationFailure := &fakeAzdoGitClient{
		pullRequests: []git.GitPullRequest{openPullRequest(42, testSHA)},
		iterationErr: errors.New("temporary failure"),
	}
	provider.setPullRequestStatusBestEffort(context.Background(), iterationFailure, repo, commitStatus)
	if iterationFailure.iterationCalls != 1 || iterationFailure.pullRequestStatusCalls != 0 {
		t.Fatalf("expected failed lookup to be swallowed; iterations=%d creates=%d", iterationFailure.iterationCalls, iterationFailure.pullRequestStatusCalls)
	}
}
