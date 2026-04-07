package forgejo_test

import (
	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/forgejo"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("NewForgejoGitAuthenticationProvider", func() {
	var scmProvider *v1alpha1.ScmProvider

	BeforeEach(func() {
		scmProvider = &v1alpha1.ScmProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-provider",
				Namespace: "default",
			},
			Spec: v1alpha1.ScmProviderSpec{
				Forgejo: &v1alpha1.Forgejo{
					Domain: "forgejo.example.com",
				},
			},
		}
	})

	Describe("with an empty secret", func() {
		It("should return an error instead of panicking", func() {
			secret := &corev1.Secret{
				Data: map[string][]byte{},
			}

			_, err := forgejo.NewForgejoGitAuthenticationProvider(scmProvider, secret)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to create Forgejo client"))
		})
	})

	Describe("with conflicting authentication methods", func() {
		It("should return an error instead of panicking", func() {
			secret := &corev1.Secret{
				Data: map[string][]byte{
					"token":    []byte("mytoken"),
					"username": []byte("myuser"),
					"password": []byte("mypassword"),
				},
			}

			_, err := forgejo.NewForgejoGitAuthenticationProvider(scmProvider, secret)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to create Forgejo client"))
		})
	})
})
