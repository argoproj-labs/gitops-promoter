package webhookreceiver

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("ParsePullRequestEvent", func() {
	DescribeTable("extracts the pull request identity from each provider's pull request event",
		func(provider string, headers map[string]string, body string, want PullRequestEvent) {
			header := http.Header{}
			for k, v := range headers {
				header.Set(k, v)
			}
			ev, ok := ParsePullRequestEvent(provider, header, []byte(body))
			Expect(ok).To(BeTrue())
			Expect(ev).To(Equal(want))
		},
		Entry("GitHub", ProviderGitHub, map[string]string{"X-Github-Event": "pull_request"},
			`{"action":"closed","number":7,"pull_request":{"number":7,"merged":true},"repository":{"full_name":"acme/deploy"}}`,
			PullRequestEvent{ID: "7", Action: "closed", Repo: RepoIdentity{FullName: "acme/deploy"}}),
		Entry("Gitea, detected as GitHub", ProviderGitHub, map[string]string{"X-Github-Event": "pull_request", "X-Gitea-Event": "pull_request"},
			`{"action":"reopened","number":3,"pull_request":{"number":3},"repository":{"full_name":"acme/deploy"}}`,
			PullRequestEvent{ID: "3", Action: "reopened", Repo: RepoIdentity{FullName: "acme/deploy"}}),
		Entry("Forgejo", ProviderForgejo, map[string]string{"X-Forgejo-Event": "pull_request"},
			`{"action":"closed","number":4,"pull_request":{"number":4},"repository":{"full_name":"acme/deploy"}}`,
			PullRequestEvent{ID: "4", Action: "closed", Repo: RepoIdentity{FullName: "acme/deploy"}}),
		Entry("GitLab", ProviderGitLab, map[string]string{"X-Gitlab-Event": "Merge Request Hook"},
			`{"object_kind":"merge_request","project":{"id":42,"path_with_namespace":"group/deploy"},"object_attributes":{"iid":12,"id":9001,"action":"close"}}`,
			PullRequestEvent{ID: "12", Action: "close", Repo: RepoIdentity{FullName: "group/deploy", ProjectID: 42}}),
		Entry("Azure DevOps", ProviderAzureDevops, nil,
			`{"eventType":"git.pullrequest.updated","publisherId":"tfs","resource":{"pullRequestId":15,"status":"abandoned","repository":{"name":"deploy","project":{"name":"Platform"}}}}`,
			PullRequestEvent{ID: "15", Action: "git.pullrequest.updated", Repo: RepoIdentity{Name: "deploy", Project: "Platform"}}),
		Entry("Bitbucket Cloud", ProviderBitbucketCloud, map[string]string{"X-Event-Key": "pullrequest:rejected", "X-Hook-Uuid": "{abc}"},
			`{"pullrequest":{"id":21,"state":"DECLINED"},"repository":{"full_name":"acme/deploy"}}`,
			PullRequestEvent{ID: "21", Action: "pullrequest:rejected", Repo: RepoIdentity{FullName: "acme/deploy"}}),
	)

	DescribeTable("ignores events that are not about a pull request",
		func(provider string, headers map[string]string, body string) {
			header := http.Header{}
			for k, v := range headers {
				header.Set(k, v)
			}
			_, ok := ParsePullRequestEvent(provider, header, []byte(body))
			Expect(ok).To(BeFalse())
		},
		Entry("GitHub push", ProviderGitHub, map[string]string{"X-Github-Event": "push"}, `{"ref":"refs/heads/main","before":"a"}`),
		Entry("GitHub pull request without a number", ProviderGitHub, map[string]string{"X-Github-Event": "pull_request"}, `{"action":"closed"}`),
		Entry("GitLab push", ProviderGitLab, map[string]string{"X-Gitlab-Event": "Push Hook"}, `{"object_kind":"push"}`),
		Entry("Azure DevOps push", ProviderAzureDevops, nil, `{"eventType":"git.push","publisherId":"tfs","resource":{}}`),
		Entry("Bitbucket Cloud push", ProviderBitbucketCloud, map[string]string{"X-Event-Key": "repo:push"}, `{"push":{}}`),
		Entry("unknown provider", ProviderUnknown, nil, `{}`),
	)

	DescribeTable("matches the GitRepository that describes the webhook's repository",
		func(provider string, ev PullRequestEvent, spec promoterv1alpha1.GitRepositorySpec, want bool) {
			Expect(ev.MatchesGitRepository(provider, spec)).To(Equal(want))
		},
		Entry("GitHub, same repository", ProviderGitHub, PullRequestEvent{Repo: RepoIdentity{FullName: "Acme/Deploy"}},
			promoterv1alpha1.GitRepositorySpec{GitHub: &promoterv1alpha1.GitHubRepo{Owner: "acme", Name: "deploy"}}, true),
		Entry("GitHub, other repository", ProviderGitHub, PullRequestEvent{Repo: RepoIdentity{FullName: "acme/deploy"}},
			promoterv1alpha1.GitRepositorySpec{GitHub: &promoterv1alpha1.GitHubRepo{Owner: "acme", Name: "other"}}, false),
		Entry("Gitea repository behind GitHub headers", ProviderGitHub, PullRequestEvent{Repo: RepoIdentity{FullName: "acme/deploy"}},
			promoterv1alpha1.GitRepositorySpec{Gitea: &promoterv1alpha1.GiteaRepo{Owner: "acme", Name: "deploy"}}, true),
		Entry("GitLab by project ID", ProviderGitLab, PullRequestEvent{Repo: RepoIdentity{FullName: "renamed/deploy", ProjectID: 42}},
			promoterv1alpha1.GitRepositorySpec{GitLab: &promoterv1alpha1.GitLabRepo{Namespace: "group", Name: "deploy", ProjectID: 42}}, true),
		Entry("GitLab by path when no project ID is configured", ProviderGitLab, PullRequestEvent{Repo: RepoIdentity{FullName: "group/deploy", ProjectID: 42}},
			promoterv1alpha1.GitRepositorySpec{GitLab: &promoterv1alpha1.GitLabRepo{Namespace: "group", Name: "deploy"}}, true),
		Entry("GitLab, other project", ProviderGitLab, PullRequestEvent{Repo: RepoIdentity{FullName: "group/deploy", ProjectID: 42}},
			promoterv1alpha1.GitRepositorySpec{GitLab: &promoterv1alpha1.GitLabRepo{Namespace: "group", Name: "deploy", ProjectID: 43}}, false),
		Entry("Azure DevOps", ProviderAzureDevops, PullRequestEvent{Repo: RepoIdentity{Project: "platform", Name: "deploy"}},
			promoterv1alpha1.GitRepositorySpec{AzureDevOps: &promoterv1alpha1.AzureDevOpsRepo{Project: "Platform", Name: "Deploy"}}, true),
		Entry("Bitbucket Cloud", ProviderBitbucketCloud, PullRequestEvent{Repo: RepoIdentity{FullName: "acme/deploy"}},
			promoterv1alpha1.GitRepositorySpec{BitbucketCloud: &promoterv1alpha1.BitbucketCloudRepo{Owner: "acme", Name: "deploy"}}, true),
		Entry("provider block missing", ProviderBitbucketCloud, PullRequestEvent{Repo: RepoIdentity{FullName: "acme/deploy"}},
			promoterv1alpha1.GitRepositorySpec{GitHub: &promoterv1alpha1.GitHubRepo{Owner: "acme", Name: "deploy"}}, false),
	)
})

var _ = Describe("WebhookReceiver pull request events", func() {
	newPullRequest := func(name, repoName, id string) *promoterv1alpha1.PullRequest {
		return &promoterv1alpha1.PullRequest{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
			Spec: promoterv1alpha1.PullRequestSpec{
				RepositoryReference: promoterv1alpha1.ObjectReference{Name: repoName},
				Title:               "promote",
				SourceBranch:        "environment/development-next",
				TargetBranch:        "environment/development",
			},
			Status: promoterv1alpha1.PullRequestStatus{ID: id},
		}
	}
	newGitHubRepo := func(name, owner, repo string) *promoterv1alpha1.GitRepository {
		return &promoterv1alpha1.GitRepository{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
			Spec:       promoterv1alpha1.GitRepositorySpec{GitHub: &promoterv1alpha1.GitHubRepo{Owner: owner, Name: repo}},
		}
	}
	postGitHubPullRequest := func(wr *WebhookReceiver, action string, number int, fullName string) *httptest.ResponseRecorder {
		body := []byte(`{"action":"` + action + `","number":` + strconv.Itoa(number) + `,"pull_request":{"number":` + strconv.Itoa(number) + `},"repository":{"full_name":"` + fullName + `"}}`)
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Github-Event", "pull_request")
		req.Header.Set("X-Github-Delivery", "test-delivery")
		rec := httptest.NewRecorder()
		wr.postRoot(rec, req)
		return rec
	}

	It("wakes the PullRequest that tracks a closed pull request, and only that one", func() {
		ctpEnqueues := &enqueueRecorder{}
		prEnqueues := &enqueueRecorder{}
		wr := &WebhookReceiver{
			baseCtx: context.Background(),
			k8sClient: newFakeClient(
				newGitHubRepo("deploy", "acme", "deploy"),
				newGitHubRepo("other", "acme", "other"),
				newPullRequest("deploy-pr", "deploy", "7"),
				newPullRequest("other-pr-same-id", "other", "7"),
				newPullRequest("deploy-pr-other-id", "deploy", "8"),
			),
			enqueueCTP: ctpEnqueues.enqueue,
			enqueuePR:  prEnqueues.enqueue,
		}

		rec := postGitHubPullRequest(wr, "closed", 7, "acme/deploy")
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(prEnqueues.count()).To(Equal(1))
		ns, name := prEnqueues.last()
		Expect(ns).To(Equal(testNamespace))
		Expect(name).To(Equal("deploy-pr"))
		Expect(ctpEnqueues.count()).To(Equal(0), "a pull request event is not a push")
	})

	It("answers 204 and wakes nothing for a pull request the controller does not track", func() {
		prEnqueues := &enqueueRecorder{}
		wr := &WebhookReceiver{
			baseCtx:    context.Background(),
			k8sClient:  newFakeClient(newGitHubRepo("deploy", "acme", "deploy"), newPullRequest("deploy-pr", "deploy", "7")),
			enqueueCTP: (&enqueueRecorder{}).enqueue,
			enqueuePR:  prEnqueues.enqueue,
		}

		rec := postGitHubPullRequest(wr, "closed", 99, "acme/deploy")
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(prEnqueues.count()).To(Equal(0))
	})

	It("leaves push handling alone", func() {
		ctp := newCTP("ctp-match")
		ctpEnqueues := &enqueueRecorder{}
		prEnqueues := &enqueueRecorder{}
		wr := &WebhookReceiver{
			baseCtx:    context.Background(),
			k8sClient:  newFakeClient(ctp),
			enqueueCTP: ctpEnqueues.enqueue,
			enqueuePR:  prEnqueues.enqueue,
		}

		rec := postGitHubPush(wr, testShaA)
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(ctpEnqueues.count()).To(Equal(1))
		Expect(prEnqueues.count()).To(Equal(0))
	})
})
