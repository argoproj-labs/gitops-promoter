package cache_test

import (
	"testing"

	promotercache "github.com/argoproj-labs/gitops-promoter/internal/cache"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCache(t *testing.T) {
	t.Parallel()

	RegisterFailHandler(Fail)
	RunSpecs(t, "Cache Suite")
}

var _ = Describe("OptionsForInstanceID", func() {
	It("scopes all partitioned types to resources without instance-id when nil", func() {
		opts := promotercache.OptionsForInstanceID(nil)
		Expect(opts.ByObject).To(HaveLen(len(promotercache.PartitionedObjects())))
		for _, obj := range promotercache.PartitionedObjects() {
			byObj, ok := opts.ByObject[obj]
			Expect(ok).To(BeTrue(), "missing ByObject for %T", obj)
			Expect(byObj.Label.String()).To(Equal("!promoter.argoproj.io/instance-id"))
		}
	})

	It("scopes all partitioned types to matching instance-id when set", func() {
		instanceID := "wave-0"
		opts := promotercache.OptionsForInstanceID(&instanceID)
		Expect(opts.ByObject).To(HaveLen(len(promotercache.PartitionedObjects())))
		for _, obj := range promotercache.PartitionedObjects() {
			byObj, ok := opts.ByObject[obj]
			Expect(ok).To(BeTrue(), "missing ByObject for %T", obj)
			Expect(byObj.Label.String()).To(Equal("promoter.argoproj.io/instance-id=wave-0"))
		}
	})

	It("includes Secret in the partition map", func() {
		opts := promotercache.OptionsForInstanceID(nil)
		_, ok := opts.ByObject[promotercache.PartitionedSecretObject()]
		Expect(ok).To(BeTrue())
	})
})
