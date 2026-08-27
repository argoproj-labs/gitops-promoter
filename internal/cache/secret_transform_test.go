package cache_test

import (
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	promotercache "github.com/argoproj-labs/gitops-promoter/internal/cache"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils/httpauth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("secretDataTransform", func() {
	transform := promotercache.SecretDataTransformForTest()

	It("retains only promoter credential keys", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "scm-creds"},
			Data: map[string][]byte{
				httpauth.TokenKey:                                           []byte("pat"),
				"unrelated-config":                                          []byte("large-payload"),
				constants.KubeconfigSecretKey:                               []byte("kubeconfig-bytes"),
				"githubAppPrivateKey":                                       []byte("private-key"),
				promoterv1alpha1.ScmProviderSecretKeyWebhookSecret:          []byte("whsec"),
				promoterv1alpha1.ScmProviderSecretKeyWebhookSignatureHeader: []byte("X-Hub-Signature-256"),
			},
			StringData: map[string]string{"ignored": "value"},
		}

		out, err := transform(secret)
		Expect(err).NotTo(HaveOccurred())

		got, ok := out.(*corev1.Secret)
		Expect(ok).To(BeTrue())
		Expect(got.Data).To(Equal(map[string][]byte{
			httpauth.TokenKey:                                           []byte("pat"),
			constants.KubeconfigSecretKey:                               []byte("kubeconfig-bytes"),
			"githubAppPrivateKey":                                       []byte("private-key"),
			promoterv1alpha1.ScmProviderSecretKeyWebhookSecret:          []byte("whsec"),
			promoterv1alpha1.ScmProviderSecretKeyWebhookSignatureHeader: []byte("X-Hub-Signature-256"),
		}))
		Expect(got.StringData).To(BeNil())
		Expect(got.Name).To(Equal("scm-creds"))
	})

	It("stores metadata only when no credential keys are present", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "app-config", Namespace: "default"},
			Data: map[string][]byte{
				"database-url": []byte("postgres://..."),
				"api-key":      []byte("secret"),
			},
		}

		out, err := transform(secret)
		Expect(err).NotTo(HaveOccurred())

		got, ok := out.(*corev1.Secret)
		Expect(ok).To(BeTrue())
		Expect(got.Data).To(BeNil())
		Expect(got.Namespace).To(Equal("default"))
	})

	It("passes through non-Secret objects unchanged", func() {
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cfg"}}
		out, err := transform(cm)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(BeIdenticalTo(cm))
	})
})
