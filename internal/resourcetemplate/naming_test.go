package resourcetemplate

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/util/validation"
)

var _ = Describe("resource names", func() {
	const sha = "0123456789abcdef0123456789abcdef01234567"

	It("fits a DNS-1123 label and stays unique per SHA", func() {
		long := "a-very-long-gate-name-that-goes-on-and-on-forever-and-ever"
		a := PerShaResourceName(long, "environment/development", sha)
		b := PerShaResourceName(long, "environment/development", "fedcba9876543210fedcba9876543210fedcba98")
		Expect(validation.IsDNS1123Label(a)).To(BeEmpty(), a)
		Expect(validation.IsDNS1123Label(b)).To(BeEmpty(), b)
		Expect(a).NotTo(Equal(b))
		Expect(len(a)).To(BeNumerically("<=", ResourceNameMaxLength))
		Expect(PerShaResourceName(long, "environment/development", sha)).To(Equal(a), "deterministic")
	})

	It("uses one name per environment in InPlace mode", func() {
		name := InPlaceResourceName("gate", "environment/development")
		Expect(validation.IsDNS1123Label(name)).To(BeEmpty())
		Expect(name).To(HavePrefix("gate-environment-development-"))
		Expect(InPlaceResourceName("gate", "environment/staging")).NotTo(Equal(name))
	})
})
