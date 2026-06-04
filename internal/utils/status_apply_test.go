package utils

import (
	"encoding/json"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	acv1alpha1 "github.com/argoproj-labs/gitops-promoter/applyconfiguration/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/utils/ptr"
)

var _ = Describe("jsonRoundTrip", func() {
	It("preserves empty pull request state in apply configuration", func() {
		src := promoterv1alpha1.ChangeTransferPolicyStatus{
			PullRequest: &promoterv1alpha1.PullRequestCommonStatus{
				ID:                       "42",
				State:                    "",
				ExternallyMergedOrClosed: ptr.To(true),
			},
		}
		dst := acv1alpha1.ChangeTransferPolicyStatus()
		Expect(jsonRoundTrip(&src, dst)).To(Succeed())
		Expect(dst.PullRequest).NotTo(BeNil())
		Expect(dst.PullRequest.State).NotTo(BeNil())
		Expect(*dst.PullRequest.State).To(BeEmpty())

		data, err := json.Marshal(dst.PullRequest)
		Expect(err).NotTo(HaveOccurred())
		Expect(json.Valid(data)).To(BeTrue())

		var parsed map[string]any
		Expect(json.Unmarshal(data, &parsed)).To(Succeed())
		state, ok := parsed["state"]
		Expect(ok).To(BeTrue(), "expected state key in JSON %s", data)
		Expect(state).To(Equal(""))
	})
})
