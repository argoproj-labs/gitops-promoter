package v1alpha1

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
)

var _ = Describe("ScmProvider GetSpec", func() {
	// The ClusterScmProviderSpec is a superset of the ScmProviderSpec, so the conversion must lose nothing: both
	// serialize to the same JSON.
	It("converts every field", func() {
		provider := &ScmProvider{Spec: ScmProviderSpec{
			SecretRef:           &v1.LocalObjectReference{Name: "secret"},
			GitHub:              &GitHub{Domain: "github.example.com", AppID: 1, InstallationID: 2},
			GitLab:              &GitLab{Domain: "gitlab.example.com"},
			Forgejo:             &Forgejo{Domain: "forgejo.example.com"},
			Gitea:               &Gitea{Domain: "gitea.example.com"},
			BitbucketCloud:      &BitbucketCloud{Domain: "bitbucket.example.com"},
			BitbucketDataCenter: &BitbucketDataCenter{Domain: "bitbucket-dc.example.com"},
			Fake:                &Fake{},
			AzureDevOps:         &AzureDevOps{Organization: "org", Domain: "azdo.example.com"},
		}}

		want, err := json.Marshal(provider.Spec)
		Expect(err).NotTo(HaveOccurred())
		got, err := json.Marshal(provider.GetSpec())
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(MatchJSON(want))
	})

	It("leaves azureDevOps nil when it is not set", func() {
		provider := &ScmProvider{Spec: ScmProviderSpec{Fake: &Fake{}}}
		Expect(provider.GetSpec().AzureDevOps).To(BeNil())
	})
})
