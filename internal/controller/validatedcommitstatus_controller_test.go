/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	_ "embed"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

//go:embed testdata/ValidatedCommitStatus.yaml
var testValidatedCommitStatusYAML string

var _ = Describe("ValidatedCommitStatus API", func() {
	Context("When unmarshalling the test data", func() {
		It("should unmarshal the ValidatedCommitStatus resource", func() {
			err := unmarshalYamlStrict(testValidatedCommitStatusYAML, &promoterv1alpha1.ValidatedCommitStatus{})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	// CRD validation only, so these run against the controller-free dev envtest cluster.
	Context("When validating the spec", func() {
		var ctx context.Context

		newGate := func(environments ...promoterv1alpha1.ValidatedCommitStatusEnvironment) *promoterv1alpha1.ValidatedCommitStatus {
			return &promoterv1alpha1.ValidatedCommitStatus{
				Name:      "vcs-validation-" + utils.KubeSafeUniqueName(randomString(10)),
				Namespace: "default",
				Spec: promoterv1alpha1.ValidatedCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "does-not-exist"},
					Key:                  promoterv1alpha1.ValidatedCommitStatusKey,
					DryBranch:            "main",
					Environments:         environments,
				},
			}
		}

		expectInvalid := func(gate *promoterv1alpha1.ValidatedCommitStatus, message string) {
			GinkgoHelper()
			err := k8sClientDev.Create(ctx, gate)
			if err == nil {
				_ = k8sClientDev.Delete(ctx, gate)
			}
			Expect(errors.IsInvalid(err)).To(BeTrue(), "expected an Invalid error, got %v", err)
			Expect(err).To(MatchError(ContainSubstring(message)))
		}

		BeforeEach(func() {
			ctx = context.Background()
		})

		It("defaults validation to Automatic and advance to Immediately", func() {
			gate := newGate(promoterv1alpha1.ValidatedCommitStatusEnvironment{Branch: testBranchStaging})
			Expect(k8sClientDev.Create(ctx, gate)).To(Succeed())
			DeferCleanup(func() { _ = k8sClientDev.Delete(ctx, gate) })

			Expect(k8sClientDev.Get(ctx, client.ObjectKeyFromObject(gate), gate)).To(Succeed())
			Expect(gate.Spec.Environments).To(HaveLen(1))
			Expect(gate.Spec.Environments[0].Validation).To(Equal(promoterv1alpha1.ValidationAutomatic))
			Expect(gate.Spec.Environments[0].Advance).To(Equal(promoterv1alpha1.AdvanceImmediately))
		})

		It("accepts a gate with no environments listed", func() {
			gate := newGate()
			Expect(k8sClientDev.Create(ctx, gate)).To(Succeed())
			DeferCleanup(func() { _ = k8sClientDev.Delete(ctx, gate) })
		})

		It("accepts Manual validation and WhenIdle advance on different environments", func() {
			gate := newGate(
				promoterv1alpha1.ValidatedCommitStatusEnvironment{Branch: testBranchStaging, Advance: promoterv1alpha1.AdvanceWhenIdle},
				promoterv1alpha1.ValidatedCommitStatusEnvironment{Branch: testBranchProduction, Validation: promoterv1alpha1.ValidationManual},
			)
			Expect(k8sClientDev.Create(ctx, gate)).To(Succeed())
			DeferCleanup(func() { _ = k8sClientDev.Delete(ctx, gate) })
		})

		It("rejects validation Manual together with advance WhenIdle", func() {
			expectInvalid(newGate(promoterv1alpha1.ValidatedCommitStatusEnvironment{
				Branch:     testBranchProduction,
				Validation: promoterv1alpha1.ValidationManual,
				Advance:    promoterv1alpha1.AdvanceWhenIdle,
			}), "validation Manual cannot be combined with advance WhenIdle")
		})

		It("rejects an unknown validation policy", func() {
			expectInvalid(newGate(promoterv1alpha1.ValidatedCommitStatusEnvironment{
				Branch:     testBranchProduction,
				Validation: promoterv1alpha1.ValidationPolicy("Sometimes"),
			}), "Unsupported value")
		})

		It("rejects an unknown advance policy", func() {
			expectInvalid(newGate(promoterv1alpha1.ValidatedCommitStatusEnvironment{
				Branch:  testBranchProduction,
				Advance: promoterv1alpha1.AdvancePolicy("Eventually"),
			}), "Unsupported value")
		})

		It("rejects a dry branch that is not a valid branch name", func() {
			gate := newGate()
			gate.Spec.DryBranch = "-main"
			expectInvalid(gate, "dryBranch must not start with '-'")
		})

		It("rejects the same environment listed twice", func() {
			gate := newGate(
				promoterv1alpha1.ValidatedCommitStatusEnvironment{Branch: testBranchStaging},
				promoterv1alpha1.ValidatedCommitStatusEnvironment{Branch: testBranchStaging, Advance: promoterv1alpha1.AdvanceWhenIdle},
			)
			expectInvalid(gate, "Duplicate value")
		})
	})
})
