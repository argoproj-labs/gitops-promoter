package utils_test

import (
	"testing"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

func TestRepoKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		owner string
		repo  string
		want  string
	}{
		{name: "lowercases", owner: "Owner", repo: "Repo", want: "owner/repo"},
		{name: "already lower", owner: "acme", repo: "app", want: "acme/app"},
		{name: "empty owner", owner: "", repo: "repo", want: ""},
		{name: "empty name", owner: "owner", repo: "", want: ""},
		{name: "both empty", owner: "", repo: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := utils.RepoKey(tt.owner, tt.repo); got != tt.want {
				t.Fatalf("RepoKey(%q, %q) = %q, want %q", tt.owner, tt.repo, got, tt.want)
			}
		})
	}
}

func TestGitRepositoryRepoKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		repo *promoterv1alpha1.GitRepository
		want string
	}{
		{name: "nil", repo: nil, want: ""},
		{name: "empty spec", repo: &promoterv1alpha1.GitRepository{}, want: ""},
		{
			name: "github",
			repo: &promoterv1alpha1.GitRepository{
				Spec: promoterv1alpha1.GitRepositorySpec{
					GitHub: &promoterv1alpha1.GitHubRepo{Owner: "Org", Name: "App"},
				},
			},
			want: "org/app",
		},
		{
			name: "gitlab",
			repo: &promoterv1alpha1.GitRepository{
				Spec: promoterv1alpha1.GitRepositorySpec{
					GitLab: &promoterv1alpha1.GitLabRepo{Namespace: "Group/Sub", Name: "Proj"},
				},
			},
			want: "group/sub/proj",
		},
		{
			name: "forgejo",
			repo: &promoterv1alpha1.GitRepository{
				Spec: promoterv1alpha1.GitRepositorySpec{
					Forgejo: &promoterv1alpha1.ForgejoRepo{Owner: "Owner", Name: "Repo"},
				},
			},
			want: "owner/repo",
		},
		{
			name: "gitea",
			repo: &promoterv1alpha1.GitRepository{
				Spec: promoterv1alpha1.GitRepositorySpec{
					Gitea: &promoterv1alpha1.GiteaRepo{Owner: "Owner", Name: "Repo"},
				},
			},
			want: "owner/repo",
		},
		{
			name: "bitbucket cloud",
			repo: &promoterv1alpha1.GitRepository{
				Spec: promoterv1alpha1.GitRepositorySpec{
					BitbucketCloud: &promoterv1alpha1.BitbucketCloudRepo{Owner: "Workspace", Name: "Repo"},
				},
			},
			want: "workspace/repo",
		},
		{
			name: "azure devops",
			repo: &promoterv1alpha1.GitRepository{
				Spec: promoterv1alpha1.GitRepositorySpec{
					AzureDevOps: &promoterv1alpha1.AzureDevOpsRepo{Project: "Project", Name: "Repo"},
				},
			},
			want: "project/repo",
		},
		{
			name: "fake",
			repo: &promoterv1alpha1.GitRepository{
				Spec: promoterv1alpha1.GitRepositorySpec{
					Fake: &promoterv1alpha1.FakeRepo{Owner: "Owner", Name: "Repo"},
				},
			},
			want: "owner/repo",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := utils.GitRepositoryRepoKey(tt.repo); got != tt.want {
				t.Fatalf("GitRepositoryRepoKey() = %q, want %q", got, tt.want)
			}
		})
	}
}
