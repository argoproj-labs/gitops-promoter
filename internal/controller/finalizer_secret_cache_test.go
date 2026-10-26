package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	promotercache "github.com/argoproj-labs/gitops-promoter/internal/cache"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// These specs read Secrets through a client backed by the production cache options (instance-id
// partition plus secretDataTransform, as in cmd/main.go), which strip every data key promoter does not
// use. Writing the finalizer must not send that truncated object back to the API server.
var _ = Describe("Secret finalizer writes through the key-filtered cache", func() {
	const finalizer = promoterv1alpha1.ScmProviderSecretFinalizer

	var (
		ctx          context.Context
		cancel       context.CancelFunc
		cachedClient client.Client
	)

	BeforeEach(func() {
		//nolint:fatcontext // ctx is intentionally reassigned in test setup
		ctx, cancel = context.WithCancel(context.Background())
		opts := promotercache.OptionsForInstanceID(nil, "default")
		c, err := cache.New(cfg, cache.Options{
			Scheme:               utils.GetScheme(),
			ByObject:             opts.ByObject,
			DefaultLabelSelector: opts.DefaultLabelSelector,
		})
		Expect(err).NotTo(HaveOccurred())
		go func() {
			defer GinkgoRecover()
			Expect(c.Start(ctx)).To(Succeed())
		}()
		Expect(c.WaitForCacheSync(ctx)).To(BeTrue())
		cachedClient, err = client.New(cfg, client.Options{Scheme: utils.GetScheme(), Cache: &client.CacheOptions{Reader: c}})
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		cancel()
	})

	createSecret := func(finalizers ...string) *v1.Secret {
		secret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "secret-finalizer-cache-" + utils.KubeSafeUniqueName(randomString(10)),
				Namespace:  "default",
				Finalizers: finalizers,
			},
			StringData: map[string]string{"token": "scm-token", "unrelated-key": "keep-me"},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		DeferCleanup(func() {
			var s v1.Secret
			if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(secret), &s); err == nil {
				s.Finalizers = nil
				_ = k8sClient.Update(context.Background(), &s)
			}
			_ = client.IgnoreNotFound(k8sClient.Delete(context.Background(), secret))
		})
		Eventually(func() error {
			var s v1.Secret
			return cachedClient.Get(ctx, client.ObjectKeyFromObject(secret), &s)
		}).Should(Succeed())
		return secret
	}

	It("adds the finalizer without dropping non-promoter data keys", func() {
		secret := createSecret()

		Expect(ensureSecretFinalizerForProvider(ctx, cachedClient, secret.Namespace, secret.Name, finalizer)).To(Succeed())

		var after v1.Secret
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(secret), &after)).To(Succeed())
		Expect(after.Finalizers).To(ContainElement(finalizer))
		Expect(after.Data).To(HaveKeyWithValue("token", []byte("scm-token")))
		Expect(after.Data).To(HaveKeyWithValue("unrelated-key", []byte("keep-me")))
	})

	It("removes the finalizer without dropping non-promoter data keys", func() {
		secret := createSecret(finalizer)

		Expect(removeSecretFinalizerForProvider(ctx, cachedClient, secret.Namespace, secret.Name, finalizer,
			func() (bool, error) { return false, nil })).To(Succeed())

		var after v1.Secret
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(secret), &after)).To(Succeed())
		Expect(after.Finalizers).ToNot(ContainElement(finalizer))
		Expect(after.Data).To(HaveKeyWithValue("token", []byte("scm-token")))
		Expect(after.Data).To(HaveKeyWithValue("unrelated-key", []byte("keep-me")))
	})
})
