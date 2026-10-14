package azuredevops_test

import (
	"context"
	"errors"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/azuredevops"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ azuredevops.AzdoGitClient = &fakeAzdoGitClient{}

type fakeAzdoGitClient struct {
	lastPullRequestStatus  git.CreatePullRequestStatusArgs
	pullRequestErr         error
	iterationErr           error
	pullRequestStatusErr   error
	pullRequests           []git.GitPullRequest
	iterations             []git.GitPullRequestIteration
	pullRequestCalls       int
	iterationCalls         int
	pullRequestStatusCalls int
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

func mirrorPullRequestStatus(gitClient azuredevops.AzdoGitClient, commitStatus *v1alpha1.CommitStatus) {
	azuredevops.CommitStatus{}.SetPullRequestStatusBestEffort(context.Background(), gitClient, testGitRepository(), commitStatus)
}

var _ = Describe("CommitStatus", func() {
	Describe("SetPullRequestStatusBestEffort", func() {
		It("mirrors the status onto the latest matching pull request iteration", func() {
			iterationID := 7
			staleIterationID := 1
			iterationSHA := testSHA
			otherSHA := "0000000000000000000000000000000000000000"
			gitClient := &fakeAzdoGitClient{
				pullRequests: []git.GitPullRequest{
					openPullRequest(41, otherSHA),
					openPullRequest(42, testSHA),
				},
				iterations: []git.GitPullRequestIteration{
					{
						Id:              &staleIterationID,
						SourceRefCommit: &git.GitCommitRef{CommitId: &iterationSHA},
					},
					{
						Id:              &iterationID,
						SourceRefCommit: &git.GitCommitRef{CommitId: &iterationSHA},
					},
				},
			}

			commitStatus := testCommitStatus(v1alpha1.CommitPhaseSuccess)
			mirrorPullRequestStatus(gitClient, commitStatus)

			Expect(gitClient.iterationCalls).To(Equal(1))
			Expect(gitClient.pullRequestStatusCalls).To(Equal(1))

			args := gitClient.lastPullRequestStatus
			Expect(args.PullRequestId).To(HaveValue(Equal(42)))
			Expect(args.Status).NotTo(BeNil())
			Expect(args.Status.IterationId).To(HaveValue(Equal(iterationID)))
			Expect(args.Status.Context).NotTo(BeNil())
			Expect(args.Status.Context.Name).To(HaveValue(Equal(commitStatus.Spec.Name)))
			Expect(args.Status.State).To(HaveValue(Equal(git.GitStatusStateValues.Succeeded)))
		})

		It("does nothing when no active pull request matches the SHA", func() {
			gitClient := &fakeAzdoGitClient{
				pullRequests: []git.GitPullRequest{openPullRequest(41, "0000000000000000000000000000000000000000")},
			}

			mirrorPullRequestStatus(gitClient, testCommitStatus(v1alpha1.CommitPhasePending))

			Expect(gitClient.iterationCalls).To(Equal(0))
			Expect(gitClient.pullRequestStatusCalls).To(Equal(0))
		})

		It("swallows a failed pull request listing", func() {
			gitClient := &fakeAzdoGitClient{pullRequestErr: errors.New("temporary failure")}

			mirrorPullRequestStatus(gitClient, testCommitStatus(v1alpha1.CommitPhasePending))

			Expect(gitClient.iterationCalls).To(Equal(0))
			Expect(gitClient.pullRequestStatusCalls).To(Equal(0))
		})

		It("swallows a failed iteration lookup", func() {
			gitClient := &fakeAzdoGitClient{
				pullRequests: []git.GitPullRequest{openPullRequest(42, testSHA)},
				iterationErr: errors.New("temporary failure"),
			}

			mirrorPullRequestStatus(gitClient, testCommitStatus(v1alpha1.CommitPhasePending))

			Expect(gitClient.iterationCalls).To(Equal(1))
			Expect(gitClient.pullRequestStatusCalls).To(Equal(0))
		})
	})
})
