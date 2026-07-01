package cache_test

import (
	"testing"

	promotercache "github.com/argoproj-labs/gitops-promoter/internal/cache"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCache(t *testing.T) {
	t.Parallel()

	RegisterFailHandler(Fail)
	RunSpecs(t, "Cache Suite")
}

const testControllerNamespace = "gitops-promoter"

var _ = Describe("OptionsForInstanceID", func() {
	It("scopes all partitioned types to resources without instance-id when nil", func() {
		opts := promotercache.OptionsForInstanceID(nil, testControllerNamespace)
		Expect(opts.ByObject).To(HaveLen(len(promotercache.PartitionedObjects()) + 1))
		for _, obj := range promotercache.PartitionedObjects() {
			byObj, ok := opts.ByObject[obj]
			Expect(ok).To(BeTrue(), "missing ByObject for %T", obj)
			Expect(byObj.Label.String()).To(Equal("!promoter.argoproj.io/instance-id"))
		}
	})

	It("scopes all partitioned types to matching instance-id when set", func() {
		instanceID := "wave-0"
		opts := promotercache.OptionsForInstanceID(&instanceID, testControllerNamespace)
		Expect(opts.ByObject).To(HaveLen(len(promotercache.PartitionedObjects()) + 1))
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

	It("scopes ControllerConfiguration to the install namespace and shipped name", func() {
		opts := promotercache.OptionsForInstanceID(nil, testControllerNamespace)
		byObj, ok := opts.ByObject[promotercache.PartitionedControllerConfigurationObject()]
		Expect(ok).To(BeTrue())
		Expect(byObj.Namespaces).To(HaveLen(1))
		Expect(byObj.Namespaces).To(HaveKey(testControllerNamespace))
		Expect(byObj.Label).To(BeNil())
		Expect(byObj.Field.String()).To(Equal("metadata.name=" + settings.ControllerConfigurationName))
	})
})
