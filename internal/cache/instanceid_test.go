package cache_test

import (
	"testing"

	promotercache "github.com/argoproj-labs/gitops-promoter/internal/cache"
	"github.com/argoproj-labs/gitops-promoter/internal/kinds"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

func TestCache(t *testing.T) {
	t.Parallel()

	RegisterFailHandler(Fail)
	RunSpecs(t, "Cache Suite")
}

const testControllerNamespace = "gitops-promoter"

var _ = Describe("ClientOptions", func() {
	It("caches unstructured Gets so out-of-tree orderCommitStatusRef gates are instance-id filtered", func() {
		opts := promotercache.ClientOptions()
		Expect(opts.Cache).NotTo(BeNil())
		Expect(opts.Cache.Unstructured).To(BeTrue())
		Expect(opts.Cache.EnableReadYourWritesConsistency).NotTo(BeNil())
		Expect(*opts.Cache.EnableReadYourWritesConsistency).To(BeTrue())
	})
})

var _ = Describe("OptionsForInstanceID", func() {
	It("scopes all partitioned types to resources without instance-id when nil", func() {
		opts := promotercache.OptionsForInstanceID(nil, testControllerNamespace)
		Expect(opts.ByObject).To(HaveLen(len(promotercache.PartitionedObjects()) + len(promotercache.UnpartitionedObjects()) + 1))
		Expect(opts.DefaultLabelSelector.String()).To(Equal("!promoter.argoproj.io/instance-id"))
		for _, obj := range promotercache.PartitionedObjects() {
			byObj, ok := opts.ByObject[obj]
			Expect(ok).To(BeTrue(), "missing ByObject for %T", obj)
			Expect(byObj.Label.String()).To(Equal("!promoter.argoproj.io/instance-id"))
		}
	})

	It("scopes all partitioned types to matching instance-id when set", func() {
		instanceID := "wave-0"
		opts := promotercache.OptionsForInstanceID(&instanceID, testControllerNamespace)
		Expect(opts.ByObject).To(HaveLen(len(promotercache.PartitionedObjects()) + len(promotercache.UnpartitionedObjects()) + 1))
		Expect(opts.DefaultLabelSelector.String()).To(Equal("promoter.argoproj.io/instance-id=wave-0"))
		for _, obj := range promotercache.PartitionedObjects() {
			byObj, ok := opts.ByObject[obj]
			Expect(ok).To(BeTrue(), "missing ByObject for %T", obj)
			Expect(byObj.Label.String()).To(Equal("promoter.argoproj.io/instance-id=wave-0"))
		}
	})

	It("includes Secret in the partition map", func() {
		opts := promotercache.OptionsForInstanceID(nil, testControllerNamespace)
		_, ok := opts.ByObject[promotercache.PartitionedSecretObject()]
		Expect(ok).To(BeTrue())
	})

	It("applies a data-trimming transform only to Secret informers", func() {
		opts := promotercache.OptionsForInstanceID(nil, testControllerNamespace)
		secretByObj, ok := opts.ByObject[promotercache.PartitionedSecretObject()]
		Expect(ok).To(BeTrue())
		Expect(secretByObj.Transform).NotTo(BeNil())

		for _, obj := range promotercache.PartitionedObjects() {
			if obj == promotercache.PartitionedSecretObject() {
				continue
			}
			byObj, objOK := opts.ByObject[obj]
			Expect(objOK).To(BeTrue(), "missing ByObject for %T", obj)
			Expect(byObj.Transform).To(BeNil(), "unexpected transform on %T", obj)
		}
	})

	It("scopes ControllerConfiguration to the install namespace and shipped name", func() {
		opts := promotercache.OptionsForInstanceID(nil, testControllerNamespace)
		byObj, ok := opts.ByObject[promotercache.PartitionedControllerConfigurationObject()]
		Expect(ok).To(BeTrue())
		Expect(byObj.Namespaces).To(HaveLen(1))
		Expect(byObj.Namespaces).To(HaveKey(testControllerNamespace))
		Expect(byObj.Label.String()).To(Equal(labels.Everything().String()))
		Expect(byObj.Field.String()).To(Equal("metadata.name=" + settings.ControllerConfigurationName))
	})

	It("keeps unlabeled types unfiltered so DefaultLabelSelector does not hide them", func() {
		opts := promotercache.OptionsForInstanceID(nil, testControllerNamespace)
		for _, obj := range promotercache.UnpartitionedObjects() {
			byObj, ok := opts.ByObject[obj]
			Expect(ok).To(BeTrue(), "missing ByObject opt-out for %T", obj)
			Expect(byObj.Label.String()).To(Equal(labels.Everything().String()), "%T must not be instance-id filtered", obj)
		}
	})
})

var _ = Describe("Partitioned promoter CRDs", func() {
	It("includes every scheme promoter kind except ControllerConfiguration", func() {
		scheme := utils.GetScheme()

		gotKinds := map[string]struct{}{}
		for _, obj := range promotercache.PartitionedObjects() {
			if _, isSecret := obj.(*corev1.Secret); isSecret {
				continue
			}
			gvk, err := apiutil.GVKForObject(obj, scheme)
			Expect(err).NotTo(HaveOccurred(), "resolve GVK for %T", obj)
			gotKinds[gvk.Kind] = struct{}{}
		}

		wantKinds := map[string]struct{}{}
		for _, obj := range kinds.All(scheme) {
			kind := kinds.Kind(scheme, obj)
			if kind == kinds.ControllerConfigurationKind {
				continue
			}
			wantKinds[kind] = struct{}{}
		}
		Expect(gotKinds).To(Equal(wantKinds),
			"PartitionedObjects kinds drifted from kinds.All (minus ControllerConfiguration). "+
				"PartitionedObjects in internal/cache/instanceid.go must stay derived from kinds.All; "+
				"do not reintroduce a hardcoded CRD list. Register new CRDs with SchemeBuilder only")
		Expect(gotKinds).NotTo(HaveKey(kinds.ControllerConfigurationKind),
			"ControllerConfiguration must not be instance-id partitioned; it is scoped by install namespace. "+
				"Keep excluding it in PartitionedObjects")
	})
})
