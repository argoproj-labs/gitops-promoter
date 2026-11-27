package azuredevops

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGeneratePullRequestUrl(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		domain       string
		organization string
		project      string
		repoName     string
		expectedUrl  string
		prId         int
		// clusterScoped references a ClusterScmProvider, whose Secret lives in the controller's namespace.
		clusterScoped bool
	}{
		{
			name:         "default dev.azure.com domain",
			domain:       "",
			organization: "myorg",
			project:      "myproject",
			repoName:     "myrepo",
			prId:         123,
			expectedUrl:  "https://dev.azure.com/myorg/myproject/_git/myrepo/pullrequest/123",
		},
		{
			name:         "custom domain",
			domain:       "devops.example.com",
			organization: "myorg",
			project:      "myproject",
			repoName:     "myrepo",
			prId:         456,
			expectedUrl:  "https://devops.example.com/myorg/myproject/_git/myrepo/pullrequest/456",
		},
		{
			name:          "ClusterScmProvider",
			domain:        "devops.example.com",
			organization:  "myorg",
			project:       "myproject",
			repoName:      "myrepo",
			prId:          789,
			expectedUrl:   "https://devops.example.com/myorg/myproject/_git/myrepo/pullrequest/789",
			clusterScoped: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			namespace := "default"
			resourceName := "test-resource"

			// Setup scheme
			scheme := runtime.NewScheme()
			_ = v1alpha1.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)

			// Create test objects
			scmProvider := &v1alpha1.ScmProvider{
				Name:      resourceName,
				Namespace: namespace,
				Spec: v1alpha1.ScmProviderSpec{
					SecretRef: &corev1.LocalObjectReference{Name: resourceName},
					AzureDevOps: &v1alpha1.AzureDevOps{
						Organization: tc.organization,
						Domain:       tc.domain,
					},
				},
			}

			secret := &corev1.Secret{
				Name:      resourceName,
				Namespace: namespace,
				Data:      map[string][]byte{"token": []byte("fake-token")},
			}

			gitRepo := &v1alpha1.GitRepository{
				Name:      resourceName,
				Namespace: namespace,
				Spec: v1alpha1.GitRepositorySpec{
					AzureDevOps: &v1alpha1.AzureDevOpsRepo{
						Project: tc.project,
						Name:    tc.repoName,
					},
					ScmProviderRef: v1alpha1.ScmProviderObjectReference{
						Kind: v1alpha1.ScmProviderKind,
						Name: resourceName,
					},
				},
			}

			objects := []client.Object{scmProvider, secret, gitRepo}
			if tc.clusterScoped {
				clusterScmProvider := &v1alpha1.ClusterScmProvider{Name: resourceName, Spec: *scmProvider.GetSpec()}
				// The Secret is in the controller's namespace, not the GitRepository's.
				secret.Namespace = "promoter-system"
				gitRepo.Spec.ScmProviderRef.Kind = v1alpha1.ClusterScmProviderKind
				controllerConfiguration := &v1alpha1.ControllerConfiguration{
					Name: settings.ControllerConfigurationName, Namespace: "promoter-system",
				}
				controllerConfiguration.Spec.ClusterScmProvider.Mode = v1alpha1.FeatureModeEnabled
				objects = []client.Object{clusterScmProvider, secret, gitRepo, controllerConfiguration}
			}

			// Create fake client
			k8sClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			pr := &PullRequest{
				k8sClient:   k8sClient,
				settingsMgr: settings.NewManager(k8sClient, k8sClient, settings.ManagerConfig{ControllerNamespace: "promoter-system"}),
			}

			prObj := v1alpha1.PullRequest{
				Name:      "test-pr",
				Namespace: namespace,
				Spec: v1alpha1.PullRequestSpec{
					RepositoryReference: v1alpha1.ObjectReference{
						Name: resourceName,
					},
				},
			}

			url, err := pr.generatePullRequestUrl(ctx, prObj, tc.prId)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if url != tc.expectedUrl {
				t.Errorf("got %q, want %q", url, tc.expectedUrl)
			}
		})
	}
}

// TestFindOpenDoesNotNeedScmProvider checks that FindOpen only needs the GitRepository and the Azure DevOps API: the
// pull request URL it used to build (which resolves the ScmProvider) is not part of its result.
func TestFindOpenDoesNotNeedScmProvider(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodOptions:
			// Resource locations for the resource areas and pull requests endpoints.
			_, _ = io.WriteString(w, `{"count":2,"value":[
				{"id":"e81700f7-3be2-46de-8624-2eb35882fcaa","area":"Location","resourceName":"ResourceAreas",
				 "routeTemplate":"_apis/{resource}/{areaId}","resourceVersion":1,"minVersion":"1.0","maxVersion":"7.1","releasedVersion":"7.1"},
				{"id":"9946fd70-0d40-406e-b686-b4744cbbcc37","area":"git","resourceName":"pullRequests",
				 "routeTemplate":"{project}/_apis/git/repositories/{repositoryId}/pullrequests","resourceVersion":1,"minVersion":"1.0","maxVersion":"7.1","releasedVersion":"7.1"}]}`)
		case strings.HasSuffix(r.URL.Path, "/pullrequests"):
			_, _ = io.WriteString(w, `{"count":1,"value":[{"pullRequestId":42,"status":"active","creationDate":"2024-01-01T00:00:00Z"}]}`)
		default:
			// Resource areas: an empty list, as on-premises servers return.
			_, _ = io.WriteString(w, `{"count":0,"value":[]}`)
		}
	}))
	t.Cleanup(server.Close)

	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	// The referenced ScmProvider does not exist, so resolving it fails.
	gitRepo := &v1alpha1.GitRepository{
		Name:      "repo",
		Namespace: "default",
		Spec: v1alpha1.GitRepositorySpec{
			AzureDevOps:    &v1alpha1.AzureDevOpsRepo{Project: "project", Name: "repo"},
			ScmProviderRef: v1alpha1.ScmProviderObjectReference{Kind: v1alpha1.ScmProviderKind, Name: "missing"},
		},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(gitRepo).Build()
	pr := &PullRequest{
		client:      azuredevops.NewPatConnection(server.URL, "pat"),
		k8sClient:   k8sClient,
		settingsMgr: settings.NewManager(k8sClient, k8sClient, settings.ManagerConfig{ControllerNamespace: "promoter-system"}),
	}

	result, err := pr.FindOpen(context.Background(), v1alpha1.PullRequest{
		Name:      "pr",
		Namespace: "default",
		Spec: v1alpha1.PullRequestSpec{
			RepositoryReference: v1alpha1.ObjectReference{Name: "repo"},
			SourceBranch:        "env-next",
			TargetBranch:        "env",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Found || result.ID != "42" {
		t.Errorf("got %+v, want found pull request 42", result)
	}
}
