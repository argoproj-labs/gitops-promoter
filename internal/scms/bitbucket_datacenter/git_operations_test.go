package bitbucket_datacenter_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	bitbucket_datacenter "github.com/argoproj-labs/gitops-promoter/internal/scms/bitbucket_datacenter"
)

var _ = Describe("GitAuthenticationProvider", func() {
	newProvider := func(data map[string][]byte) *bitbucket_datacenter.GitAuthenticationProvider {
		scmProvider := &v1alpha1.ScmProvider{Spec: v1alpha1.ScmProviderSpec{
			BitbucketDataCenter: &v1alpha1.BitbucketDataCenter{Domain: "bitbucket.example.com"},
		}}
		provider, err := bitbucket_datacenter.NewBitbucketDataCenterGitAuthenticationProvider(scmProvider, &corev1.Secret{Data: data})
		Expect(err).NotTo(HaveOccurred())
		return provider
	}

	Describe("GetGitHTTPHeader", func() {
		It("returns a Bearer header scoped to the domain when the secret has a token", func() {
			urlPrefix, header, err := newProvider(map[string][]byte{"token": []byte("s3cret")}).GetGitHTTPHeader(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(urlPrefix).To(Equal("https://bitbucket.example.com/"))
			Expect(header).To(Equal("Authorization: Bearer s3cret"))
		})

		It("returns no header for username/password credentials", func() {
			_, header, err := newProvider(map[string][]byte{
				"username": []byte("alice"),
				"password": []byte("pw"),
			}).GetGitHTTPHeader(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(header).To(BeEmpty())
		})
	})
})
