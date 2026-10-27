package azuredevops_test

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/azuredevops"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("Azure workload identity", func() {
	var (
		requested *v1alpha1.AzureWorkloadIdentity
		tokenErr  error
		restore   func()
	)

	workloadIdentity := &v1alpha1.AzureWorkloadIdentity{
		ClientID: "00000000-0000-0000-0000-000000000001",
		TenantID: "00000000-0000-0000-0000-000000000002",
	}
	wiProvider := &v1alpha1.ClusterScmProvider{Spec: v1alpha1.ClusterScmProviderSpec{
		AzureDevOps: &v1alpha1.ClusterAzureDevOps{AzureDevOps: v1alpha1.AzureDevOps{Organization: "myorg"}, WorkloadIdentity: workloadIdentity},
	}}
	patProvider := &v1alpha1.ScmProvider{Spec: v1alpha1.ScmProviderSpec{
		AzureDevOps: &v1alpha1.AzureDevOps{Organization: "myorg"},
	}}
	patSecret := corev1.Secret{Data: map[string][]byte{"token": []byte("my-pat")}}

	BeforeEach(func() {
		requested = nil
		tokenErr = nil
		restore = azuredevops.SetWorkloadIdentityTokenFunc(func(_ context.Context, wi *v1alpha1.AzureWorkloadIdentity) (string, error) {
			requested = wi
			if tokenErr != nil {
				return "", tokenErr
			}
			return "entra-token", nil
		})
	})

	AfterEach(func() {
		restore()
	})

	Describe("GitAuthenticationProvider", func() {
		It("returns the workload identity token without needing a secret", func() {
			provider, err := azuredevops.NewAzdoGitAuthenticationProvider(wiProvider, &corev1.Secret{})
			Expect(err).NotTo(HaveOccurred())

			token, err := provider.GetToken(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(token).To(Equal("entra-token"))
			Expect(requested).To(Equal(workloadIdentity))
		})

		It("returns the error when the token cannot be obtained", func() {
			tokenErr = errors.New("federated token file missing")
			provider, err := azuredevops.NewAzdoGitAuthenticationProvider(wiProvider, &corev1.Secret{})
			Expect(err).NotTo(HaveOccurred())

			_, err = provider.GetToken(context.Background())
			Expect(err).To(MatchError(tokenErr))
		})

		It("keeps using the PAT when workload identity is not configured", func() {
			provider, err := azuredevops.NewAzdoGitAuthenticationProvider(patProvider, &patSecret)
			Expect(err).NotTo(HaveOccurred())

			token, err := provider.GetToken(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(token).To(Equal("my-pat"))
			Expect(requested).To(BeNil())
		})
	})

	Describe("GetClient", func() {
		It("creates a Bearer authenticated connection with workload identity", func() {
			connection, _, err := azuredevops.GetClient(context.Background(), wiProvider, corev1.Secret{}, "myorg")
			Expect(err).NotTo(HaveOccurred())
			Expect(connection.AuthorizationString).To(Equal("Bearer entra-token"))
			Expect(connection.BaseUrl).To(Equal("https://dev.azure.com/myorg"))
		})

		It("creates a Basic authenticated connection with a PAT", func() {
			connection, _, err := azuredevops.GetClient(context.Background(), patProvider, patSecret, "myorg")
			Expect(err).NotTo(HaveOccurred())
			Expect(connection.AuthorizationString).To(Equal("Basic " + base64.StdEncoding.EncodeToString([]byte(":my-pat"))))
			Expect(requested).To(BeNil())
		})
	})

	Describe("ApplyHTTPAuth", func() {
		It("sets a Bearer token with workload identity", func() {
			req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
			Expect(azuredevops.ApplyHTTPAuth(context.Background(), wiProvider, corev1.Secret{}, req)).To(Succeed())
			Expect(req.Header.Get("Authorization")).To(Equal("Bearer entra-token"))
		})

		It("sets Basic auth with a PAT", func() {
			req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
			Expect(azuredevops.ApplyHTTPAuth(context.Background(), patProvider, patSecret, req)).To(Succeed())
			Expect(req.Header.Get("Authorization")).To(Equal("Basic " + base64.StdEncoding.EncodeToString([]byte(":my-pat"))))
		})
	})
})

var _ = Describe("GetGitHTTPHeader", func() {
	It("returns a Bearer header scoped to the Azure DevOps domain with workload identity", func() {
		restore := azuredevops.SetWorkloadIdentityTokenFunc(func(context.Context, *v1alpha1.AzureWorkloadIdentity) (string, error) {
			return "entra-token", nil
		})
		defer restore()
		wi := &v1alpha1.ClusterScmProvider{Spec: v1alpha1.ClusterScmProviderSpec{AzureDevOps: &v1alpha1.ClusterAzureDevOps{
			AzureDevOps:      v1alpha1.AzureDevOps{Organization: "o"},
			WorkloadIdentity: &v1alpha1.AzureWorkloadIdentity{},
		}}}
		provider, err := azuredevops.NewAzdoGitAuthenticationProvider(wi, &corev1.Secret{})
		Expect(err).NotTo(HaveOccurred())

		urlPrefix, header, err := provider.GetGitHTTPHeader(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(urlPrefix).To(Equal("https://dev.azure.com/"))
		Expect(header).To(Equal("Authorization: Bearer entra-token"))
	})

	It("returns no header with a PAT", func() {
		pat := &v1alpha1.ScmProvider{Spec: v1alpha1.ScmProviderSpec{AzureDevOps: &v1alpha1.AzureDevOps{Organization: "o"}}}

		provider, err := azuredevops.NewAzdoGitAuthenticationProvider(pat, &corev1.Secret{})
		Expect(err).NotTo(HaveOccurred())

		_, header, err := provider.GetGitHTTPHeader(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(header).To(BeEmpty())
	})
})
