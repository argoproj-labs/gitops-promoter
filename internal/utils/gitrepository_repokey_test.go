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

package utils_test

import (
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("RepoKey", func() {
	type testCase struct {
		provider string
		owner    string
		repo     string
		want     string
	}

	DescribeTable("returns a provider-scoped lowercased key",
		func(tc testCase) {
			Expect(utils.RepoKey(tc.provider, tc.owner, tc.repo)).To(Equal(tc.want))
		},
		Entry("lowercases", testCase{provider: "GitHub", owner: "Owner", repo: "Repo", want: "github\x00owner\x00repo"}),
		Entry("already lower", testCase{provider: "github", owner: "acme", repo: "app", want: "github\x00acme\x00app"}),
		Entry("empty provider", testCase{provider: "", owner: "owner", repo: "repo", want: ""}),
		Entry("empty owner", testCase{provider: "github", owner: "", repo: "repo", want: ""}),
		Entry("empty name", testCase{provider: "github", owner: "owner", repo: "", want: ""}),
		Entry("all empty", testCase{provider: "", owner: "", repo: "", want: ""}),
	)

	It("never collides across providers for the same owner/name", func() {
		github := utils.RepoKey(utils.ProviderGitHub, "acme", "app")
		gitlab := utils.RepoKey(utils.ProviderGitLab, "acme", "app")
		Expect(github).NotTo(Equal(gitlab))
	})

	It("never collides across GitLab subgroup boundaries", func() {
		// GitLab namespaces can themselves contain '/' for subgroups, so
		// namespace="group/sub", name="proj" must not collide with
		// namespace="group", name="sub/proj".
		a := utils.RepoKey(utils.ProviderGitLab, "group/sub", "proj")
		b := utils.RepoKey(utils.ProviderGitLab, "group", "sub/proj")
		Expect(a).NotTo(Equal(b))
	})
})

var _ = Describe("GitRepositoryRepoKey", func() {
	type testCase struct {
		repo *promoterv1alpha1.GitRepository
		want string
	}

	DescribeTable("returns the repo identity key from whichever provider block is set",
		func(tc testCase) {
			Expect(utils.GitRepositoryRepoKey(tc.repo)).To(Equal(tc.want))
		},
		Entry("nil", testCase{repo: nil, want: ""}),
		Entry("empty spec", testCase{repo: &promoterv1alpha1.GitRepository{}, want: ""}),
		Entry("github", testCase{
			repo: &promoterv1alpha1.GitRepository{
				Spec: promoterv1alpha1.GitRepositorySpec{
					GitHub: &promoterv1alpha1.GitHubRepo{Owner: "Org", Name: "App"},
				},
			},
			want: "github\x00org\x00app",
		}),
		Entry("gitlab", testCase{
			repo: &promoterv1alpha1.GitRepository{
				Spec: promoterv1alpha1.GitRepositorySpec{
					GitLab: &promoterv1alpha1.GitLabRepo{Namespace: "Group/Sub", Name: "Proj"},
				},
			},
			want: "gitlab\x00group/sub\x00proj",
		}),
		Entry("forgejo", testCase{
			repo: &promoterv1alpha1.GitRepository{
				Spec: promoterv1alpha1.GitRepositorySpec{
					Forgejo: &promoterv1alpha1.ForgejoRepo{Owner: "Owner", Name: "Repo"},
				},
			},
			want: "forgejo\x00owner\x00repo",
		}),
		Entry("gitea", testCase{
			repo: &promoterv1alpha1.GitRepository{
				Spec: promoterv1alpha1.GitRepositorySpec{
					Gitea: &promoterv1alpha1.GiteaRepo{Owner: "Owner", Name: "Repo"},
				},
			},
			want: "gitea\x00owner\x00repo",
		}),
		Entry("bitbucket cloud", testCase{
			repo: &promoterv1alpha1.GitRepository{
				Spec: promoterv1alpha1.GitRepositorySpec{
					BitbucketCloud: &promoterv1alpha1.BitbucketCloudRepo{Owner: "Workspace", Name: "Repo"},
				},
			},
			want: "bitbucketcloud\x00workspace\x00repo",
		}),
		Entry("azure devops", testCase{
			repo: &promoterv1alpha1.GitRepository{
				Spec: promoterv1alpha1.GitRepositorySpec{
					AzureDevOps: &promoterv1alpha1.AzureDevOpsRepo{Project: "Project", Name: "Repo"},
				},
			},
			want: "azuredevops\x00project\x00repo",
		}),
		Entry("fake keys as github", testCase{
			repo: &promoterv1alpha1.GitRepository{
				Spec: promoterv1alpha1.GitRepositorySpec{
					Fake: &promoterv1alpha1.FakeRepo{Owner: "Owner", Name: "Repo"},
				},
			},
			want: "github\x00owner\x00repo",
		}),
	)

	It("never collides across providers for GitRepositories with the same owner/name", func() {
		githubRepo := &promoterv1alpha1.GitRepository{
			Spec: promoterv1alpha1.GitRepositorySpec{
				GitHub: &promoterv1alpha1.GitHubRepo{Owner: "acme", Name: "app"},
			},
		}
		gitlabRepo := &promoterv1alpha1.GitRepository{
			Spec: promoterv1alpha1.GitRepositorySpec{
				GitLab: &promoterv1alpha1.GitLabRepo{Namespace: "acme", Name: "app"},
			},
		}
		Expect(utils.GitRepositoryRepoKey(githubRepo)).NotTo(Equal(utils.GitRepositoryRepoKey(gitlabRepo)))
	})
})
