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
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/argoproj-labs/gitops-promoter/internal/validations"
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

// validatedPipeline is a PromotionStrategy ordered by a ValidatedCommitStatus, with a clone of its
// repository that plays the people and the hydrators pushing to it.
type validatedPipeline struct {
	ctx               context.Context //nolint:containedctx // one spec's context, carried so the helpers read like steps
	scmSecret         *v1.Secret
	scmProvider       *promoterv1alpha1.ScmProvider
	gitRepo           *promoterv1alpha1.GitRepository
	promotionStrategy *promoterv1alpha1.PromotionStrategy
	gate              *promoterv1alpha1.ValidatedCommitStatus
	name              string
	gitPath           string
	dryBranch         string
}

// newValidatedPipeline prepares a repository and the resources of a pipeline, without creating the
// resources: a spec adjusts the PromotionStrategy and the gate first, then calls create.
//
// The repository starts with two dry commits, and with every environment live on the first of them:
// its active branch carries hydrator.metadata for that commit, and nothing is proposed.
func newValidatedPipeline(ctx context.Context, stem string) *validatedPipeline {
	GinkgoHelper()
	return newValidatedPipelineOn(ctx, stem, setupInitialTestGitRepoOnServer)
}

// newValidatedPipelineOn is newValidatedPipeline with a repository prepared by setup.
func newValidatedPipelineOn(ctx context.Context, stem string, setup func(context.Context, *promoterv1alpha1.GitRepository)) *validatedPipeline {
	GinkgoHelper()
	p := &validatedPipeline{ctx: ctx}
	p.name, p.scmSecret, p.scmProvider, p.gitRepo, _, _, p.promotionStrategy = promotionStrategyResource(ctx, stem, "default")
	p.promotionStrategy.Spec.OrderCommitStatusRef = promoterv1alpha1.OrderCommitStatusRef{
		Group: promoterv1alpha1.GroupVersion.Group,
		Kind:  promoterv1alpha1.ValidatedOrderCommitStatusKind,
		Name:  p.name,
	}
	setup(ctx, p.gitRepo)

	var err error
	p.gitPath, err = cloneTestRepo(ctx, p.gitRepo)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(p.gitPath) })
	p.dryBranch = strings.TrimSpace(p.git("rev-parse", "--abbrev-ref", "HEAD"))

	p.gate = &promoterv1alpha1.ValidatedCommitStatus{
		Name:      p.name,
		Namespace: "default",
		Spec: promoterv1alpha1.ValidatedCommitStatusSpec{
			PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: p.name},
			Key:                  promoterv1alpha1.ValidatedCommitStatusKey,
			DryBranch:            p.dryBranch,
		},
	}
	return p
}

// create creates the pipeline's resources, the gate before the PromotionStrategy that references it.
func (p *validatedPipeline) create() {
	GinkgoHelper()
	Expect(k8sClient.Create(p.ctx, p.scmSecret)).To(Succeed())
	Expect(k8sClient.Create(p.ctx, p.scmProvider)).To(Succeed())
	Expect(k8sClient.Create(p.ctx, p.gitRepo)).To(Succeed())
	Expect(k8sClient.Create(p.ctx, p.gate)).To(Succeed())
	Expect(k8sClient.Create(p.ctx, p.promotionStrategy)).To(Succeed())
	DeferCleanup(func() {
		_ = k8sClient.Delete(p.ctx, p.promotionStrategy)
		_ = k8sClient.Delete(p.ctx, p.gate)
		_ = k8sClient.Delete(p.ctx, p.gitRepo)
		_ = k8sClient.Delete(p.ctx, p.scmProvider)
		_ = k8sClient.Delete(p.ctx, p.scmSecret)
	})
}

func (p *validatedPipeline) git(args ...string) string {
	GinkgoHelper()
	out, err := runGitCmd(p.ctx, p.gitPath, args...)
	Expect(err).NotTo(HaveOccurred())
	return out
}

// dryHead returns the head of the dry branch on the remote.
func (p *validatedPipeline) dryHead() string {
	GinkgoHelper()
	return p.remoteRef("refs/heads/" + p.dryBranch)
}

// dryParent returns the dry commit before the given one.
func (p *validatedPipeline) dryParent(drySha string) string {
	GinkgoHelper()
	p.git("fetch", "origin", p.dryBranch)
	return strings.TrimSpace(p.git("rev-parse", drySha+"^"))
}

// expectTagSource waits for the gate to report what last moved an environment's tag.
func (p *validatedPipeline) expectTagSource(branch string, source promoterv1alpha1.ValidatedTagSource) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		status := p.environmentStatus(g, branch)
		g.Expect(status.Tag).NotTo(BeNil())
		g.Expect(status.Tag.Name).To(Equal(validations.TagName(branch, "")))
		g.Expect(status.Tag.Source).To(Equal(source))
	}, constants.EventuallyTimeout).Should(Succeed())
}

// remoteRef returns the commit a ref points at on the remote, "" when the ref does not exist.
func (p *validatedPipeline) remoteRef(ref string) string {
	GinkgoHelper()
	sha, _, _ := strings.Cut(strings.TrimSpace(p.git("ls-remote", "origin", ref)), "\t")
	return sha
}

// tag returns the dry commit an environment's tag points at, "" when the tag does not exist.
func (p *validatedPipeline) tag(branch string) string {
	GinkgoHelper()
	return p.remoteRef("refs/tags/" + validations.TagName(branch, ""))
}

// commitDry pushes a new commit to the dry branch and returns its SHA.
func (p *validatedPipeline) commitDry(message string) string {
	GinkgoHelper()
	sha, err := makeDryCommit(p.ctx, p.gitPath, message)
	Expect(err).NotTo(HaveOccurred())
	return sha
}

// hydrate plays the environment's hydrator: it reads the environment's tag and pushes a hydrated commit
// for that dry commit to the environment's proposed branch. It returns the dry commit it hydrated.
func (p *validatedPipeline) hydrate(branch string) string {
	GinkgoHelper()
	drySha := p.tag(branch)
	Expect(drySha).NotTo(BeEmpty(), "environment %s has no tag to hydrate from", branch)
	Expect(hydrateEnvironment(p.ctx, p.gitPath, branch+"-next", drySha, "hydrate "+branch+" from its tag")).To(Succeed())
	return drySha
}

// validationsOn returns the validation lines written on a dry commit.
func (p *validatedPipeline) validationsOn(drySha string) []validations.Line {
	GinkgoHelper()
	if _, err := runGitCmd(p.ctx, p.gitPath, "fetch", "origin", "+"+git.PromoterValidationsNotesRef+":"+git.PromoterValidationsNotesRef); err != nil {
		return nil
	}
	note, err := runGitCmd(p.ctx, p.gitPath, "notes", "--ref="+git.PromoterValidationsNotesRef, "show", drySha)
	if err != nil {
		return nil
	}
	return validations.ParseNote(note)
}

// environmentStatus returns the gate's status for one environment.
func (p *validatedPipeline) environmentStatus(g Gomega, branch string) promoterv1alpha1.ValidatedCommitStatusEnvironmentStatus {
	GinkgoHelper()
	var gate promoterv1alpha1.ValidatedCommitStatus
	g.Expect(k8sClient.Get(p.ctx, client.ObjectKeyFromObject(p.gate), &gate)).To(Succeed())
	for _, environment := range gate.Status.Environments {
		if environment.Branch == branch {
			return environment
		}
	}
	g.Expect(branch).To(BeEmpty(), "the gate reports no status for environment %s yet", branch)
	return promoterv1alpha1.ValidatedCommitStatusEnvironmentStatus{}
}

// observed returns what the PromotionStrategy reports for one environment.
func (p *validatedPipeline) observed(g Gomega, branch string) promoterv1alpha1.EnvironmentStatus {
	GinkgoHelper()
	var ps promoterv1alpha1.PromotionStrategy
	g.Expect(k8sClient.Get(p.ctx, client.ObjectKeyFromObject(p.promotionStrategy), &ps)).To(Succeed())
	for _, environment := range ps.Status.Environments {
		if environment.Branch == branch {
			return environment
		}
	}
	g.Expect(branch).To(BeEmpty(), "the PromotionStrategy reports no status for environment %s yet", branch)
	return promoterv1alpha1.EnvironmentStatus{}
}

// expectTag waits for an environment's tag to point at the dry commit.
func (p *validatedPipeline) expectTag(branch, drySha string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		g.Expect(p.tag(branch)).To(Equal(drySha))
		// The tag is pushed before status is written. Wait for both, so that what follows sees them agree.
		status := p.environmentStatus(g, branch)
		g.Expect(status.Tag).NotTo(BeNil())
		g.Expect(status.Tag.Sha).To(Equal(drySha))
	}, constants.EventuallyTimeout).Should(Succeed())
}

// expectLive waits for the dry commit to be live in an environment.
func (p *validatedPipeline) expectLive(branch, drySha string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		g.Expect(p.observed(g, branch).Active.Dry.Sha).To(Equal(drySha))
	}, constants.EventuallyTimeout).Should(Succeed())
}

// promote hydrates an environment from its tag and waits for the commit to be live in it.
func (p *validatedPipeline) promote(branch string) {
	GinkgoHelper()
	p.expectLive(branch, p.hydrate(branch))
}

// expectGate waits for the gate to report the phase on the environment's proposed hydrated commit.
func (p *validatedPipeline) expectGate(branch string, phase promoterv1alpha1.CommitStatusPhase, description string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		proposed := p.observed(g, branch).Proposed.Hydrated.Sha
		g.Expect(proposed).NotTo(BeEmpty())

		var commitStatus promoterv1alpha1.CommitStatus
		name := utils.CommitStatusResourceName(p.ctx, p.gate, branch)
		g.Expect(k8sClient.Get(p.ctx, client.ObjectKey{Namespace: "default", Name: name}, &commitStatus)).To(Succeed())
		g.Expect(commitStatus.Spec.Sha).To(Equal(proposed))
		g.Expect(commitStatus.Spec.Name).To(Equal(p.gate.Spec.Key))
		g.Expect(commitStatus.Spec.Phase).To(Equal(phase))
		g.Expect(commitStatus.Spec.Description).To(Equal(description))

		status := p.environmentStatus(g, branch)
		g.Expect(status.Phase).To(Equal(phase))
		g.Expect(status.ReportedSha).To(Equal(proposed))
	}, constants.EventuallyTimeout).Should(Succeed())
}

func short(sha string) string {
	return utils.TruncateString(sha, shortShaLength)
}

var _ = Describe("ValidatedCommitStatus Controller", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Context("When the PromotionStrategy is missing", func() {
		It("should set Ready=False", func() {
			gate := &promoterv1alpha1.ValidatedCommitStatus{
				Name:      "vcs-missing-ps-" + utils.KubeSafeUniqueName(randomString(10)),
				Namespace: "default",
				Spec: promoterv1alpha1.ValidatedCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "non-existent"},
					Key:                  promoterv1alpha1.ValidatedCommitStatusKey,
					DryBranch:            "main",
				},
			}
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gate) })

			Eventually(func(g Gomega) {
				updated := &promoterv1alpha1.ValidatedCommitStatus{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(gate), updated)).To(Succeed())
				ready := meta.FindStatusCondition(updated.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(ready).ToNot(BeNil())
				g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(ready.Message).To(ContainSubstring("not found"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When every environment is validated automatically", func() {
		It("moves each tag once the commit succeeded upstream, and leaves a later proposal alone when a new dry commit arrives", func() {
			p := newValidatedPipeline(ctx, "validated-linear")
			// Production keeps its proposal open, so that there is something for a new dry commit to disturb.
			p.promotionStrategy.Spec.Environments[2].AutoMerge = new(false)
			p.create()
			first := p.dryHead()
			live := p.dryParent(first)

			By("Starting every tag at what is live, and moving the root environment's on to the head of the dry branch")
			p.expectTag(testBranchDevelopment, first)
			p.expectTagSource(testBranchDevelopment, promoterv1alpha1.ValidatedTagSourceAutomatic)
			p.expectTag(testBranchStaging, live)
			p.expectTagSource(testBranchStaging, promoterv1alpha1.ValidatedTagSourceSeeded)
			p.expectTag(testBranchProduction, live)
			p.expectTagSource(testBranchProduction, promoterv1alpha1.ValidatedTagSourceSeeded)
			Eventually(func(g Gomega) {
				var gate promoterv1alpha1.ValidatedCommitStatus
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(p.gate), &gate)).To(Succeed())
				g.Expect(gate.Status.DryBranch).NotTo(BeNil())
				g.Expect(gate.Status.DryBranch.Sha).To(Equal(first))
				ready := meta.FindStatusCondition(gate.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Status).To(Equal(metav1.ConditionTrue), ready.Message)

				root := p.environmentStatus(g, testBranchDevelopment)
				g.Expect(root.UpstreamCleared).NotTo(BeNil())
				g.Expect(root.UpstreamCleared.Sha).To(Equal(first), "an environment with no upstreams is cleared for the head of the dry branch")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Promoting to development, which validates the commit for staging")
			p.promote(testBranchDevelopment)
			p.expectTag(testBranchStaging, first)
			p.expectTagSource(testBranchStaging, promoterv1alpha1.ValidatedTagSourceAutomatic)
			Eventually(func(g Gomega) {
				staging := p.environmentStatus(g, testBranchStaging)
				g.Expect(staging.UpstreamCleared).NotTo(BeNil())
				g.Expect(staging.UpstreamCleared.Sha).To(Equal(first))
				g.Expect(staging.Tag.MovedAt).NotTo(BeNil())

				successes := p.environmentStatus(g, testBranchDevelopment).SuccessfulShas
				g.Expect(successes).To(HaveLen(2))
				g.Expect(successes[0].Sha).To(Equal(first))
				g.Expect(successes[1].Sha).To(Equal(live))
			}, constants.EventuallyTimeout).Should(Succeed())
			Expect(p.tag(testBranchProduction)).To(Equal(live), "staging has not succeeded on the commit yet")

			By("Recording the validation in git, on the dry commit")
			Expect(p.validationsOn(first)).To(ConsistOf(
				And(
					HaveField("Target", "environment/staging-tag"),
					HaveField("Source", validations.SourceAutomatic),
					HaveField("By", "environment/development successful"),
				),
			))

			By("Promoting to staging, which validates the commit for production")
			p.promote(testBranchStaging)
			p.expectTag(testBranchProduction, first)

			By("Proposing the commit to production")
			p.hydrate(testBranchProduction)
			p.expectGate(testBranchProduction, promoterv1alpha1.CommitPhaseSuccess, "Proposed commit matches environment/production-tag")
			proposal := ""
			Eventually(func(g Gomega) {
				observed := p.observed(g, testBranchProduction)
				g.Expect(observed.Proposed.Dry.Sha).To(Equal(first))
				g.Expect(observed.PullRequest).NotTo(BeNil())
				g.Expect(observed.PullRequest.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				proposal = observed.Proposed.Hydrated.Sha
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Pushing a new dry commit: only the root environment's tag follows it")
			second := p.commitDry("second change")
			p.expectTag(testBranchDevelopment, second)
			p.promote(testBranchDevelopment)

			By("Leaving production's proposal as it was while the new commit is still on its way through staging")
			p.expectTag(testBranchStaging, second)
			Consistently(func(g Gomega) {
				g.Expect(p.tag(testBranchProduction)).To(Equal(first))
				observed := p.observed(g, testBranchProduction)
				g.Expect(observed.Proposed.Hydrated.Sha).To(Equal(proposal))
				g.Expect(observed.Proposed.Dry.Sha).To(Equal(first))
				g.Expect(p.environmentStatus(g, testBranchProduction).Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			}, 5*time.Second, 500*time.Millisecond).Should(Succeed())

			By("Moving production's tag once staging succeeded on the new commit")
			p.promote(testBranchStaging)
			p.expectTag(testBranchProduction, second)
			p.expectGate(testBranchProduction, promoterv1alpha1.CommitPhasePending,
				"Waiting for hydrator to process environment/production-tag at "+short(second)+" (currently "+short(first)+")")

			By("Passing the gate again when production's hydrator has caught up with its tag")
			p.hydrate(testBranchProduction)
			p.expectGate(testBranchProduction, promoterv1alpha1.CommitPhaseSuccess, "Proposed commit matches environment/production-tag")
		})
	})

	Context("When nothing is live yet", func() {
		It("starts the root environment at the head of the dry branch and creates the other tags as commits are validated", func() {
			// Every environment has a proposal for the head of the dry branch and nothing live.
			p := newValidatedPipelineOn(ctx, "validated-fresh", func(_ context.Context, repo *promoterv1alpha1.GitRepository) {
				setupInitialTestGitRepoWithoutActiveMetadata(repo)
			})
			p.create()
			head := p.dryHead()

			By("Promoting the proposal each environment already has, in order, with nobody hydrating anything")
			p.expectTag(testBranchDevelopment, head)
			p.expectLive(testBranchDevelopment, head)
			p.expectTag(testBranchStaging, head)
			p.expectLive(testBranchStaging, head)
			p.expectTag(testBranchProduction, head)
			p.expectLive(testBranchProduction, head)

			By("Reporting how each tag came to be")
			p.expectTagSource(testBranchDevelopment, promoterv1alpha1.ValidatedTagSourceSeeded)
			p.expectTagSource(testBranchStaging, promoterv1alpha1.ValidatedTagSourceAutomatic)
			p.expectTagSource(testBranchProduction, promoterv1alpha1.ValidatedTagSourceAutomatic)
		})
	})

	Context("When an environment depends on two others", func() {
		It("validates a commit for it only once both have succeeded on that commit", func() {
			p := newValidatedPipeline(ctx, "validated-fan-in")
			p.promotionStrategy.Spec.Environments = []promoterv1alpha1.Environment{
				{Branch: testBranchDevelopment},
				{Branch: testBranchStaging},
				{Branch: testBranchProduction, DependsOn: []string{testBranchDevelopment, testBranchStaging}},
			}
			p.create()
			first := p.dryHead()
			live := p.dryParent(first)

			By("Following the dry branch in both environments that have no upstreams")
			p.expectTag(testBranchDevelopment, first)
			p.expectTag(testBranchStaging, first)
			p.expectTag(testBranchProduction, live)

			By("Holding production back while only one of them has succeeded on the commit")
			p.promote(testBranchDevelopment)
			Eventually(func(g Gomega) {
				g.Expect(p.environmentStatus(g, testBranchDevelopment).SuccessfulShas).To(HaveLen(2))
			}, constants.EventuallyTimeout).Should(Succeed())
			Consistently(func(g Gomega) {
				g.Expect(p.tag(testBranchProduction)).To(Equal(live))
				cleared := p.environmentStatus(g, testBranchProduction).UpstreamCleared
				g.Expect(cleared).NotTo(BeNil())
				g.Expect(cleared.Sha).To(Equal(live))
			}, 5*time.Second, 500*time.Millisecond).Should(Succeed())

			By("Validating the commit for production when the other has succeeded on it too")
			p.promote(testBranchStaging)
			p.expectTag(testBranchProduction, first)
			Expect(p.validationsOn(first)).To(ConsistOf(
				And(
					HaveField("Target", "environment/production-tag"),
					HaveField("Source", validations.SourceAutomatic),
					HaveField("By", "environment/development, environment/staging successful"),
				),
			))

			By("Doing the same for the next dry commit, whichever environment gets there first")
			second := p.commitDry("second change")
			p.expectTag(testBranchDevelopment, second)
			p.expectTag(testBranchStaging, second)
			p.promote(testBranchStaging)
			Consistently(func() string {
				return p.tag(testBranchProduction)
			}, 5*time.Second, 500*time.Millisecond).Should(Equal(first))
			p.promote(testBranchDevelopment)
			p.expectTag(testBranchProduction, second)
		})
	})

	Context("When hydrating a commit changes nothing for an environment", func() {
		It("counts the environment as having succeeded on the commit, so that the next one gets it", func() {
			p := newValidatedPipeline(ctx, "validated-no-op")
			p.create()
			first := p.dryHead()
			live := p.dryParent(first)

			p.expectTag(testBranchDevelopment, first)
			p.expectTag(testBranchStaging, live)

			By("Hydrating development from its tag without a new hydrated commit: only the hydrator note moves")
			// No webhook announces a note. The gate nudges the ChangeTransferPolicy until it has read it.
			Expect(addNoteToEnvironment(ctx, p.gitPath, testBranchDevelopmentNext, first)).To(Succeed())

			By("Validating the commit for staging although nothing was merged in development")
			p.expectTag(testBranchStaging, first)
			Eventually(func(g Gomega) {
				observed := p.observed(g, testBranchDevelopment)
				g.Expect(observed.Active.Dry.Sha).To(Equal(live), "development still runs the manifests of the commit before")
				g.Expect(observed.Proposed.Note).NotTo(BeNil())
				g.Expect(observed.Proposed.Note.DrySha).To(Equal(first))

				successes := p.environmentStatus(g, testBranchDevelopment).SuccessfulShas
				g.Expect(successes).NotTo(BeEmpty())
				g.Expect(successes[0].Sha).To(Equal(first))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Carrying on through staging in the same way")
			Expect(addNoteToEnvironment(ctx, p.gitPath, testBranchStagingNext, first)).To(Succeed())
			p.expectTag(testBranchProduction, first)
		})
	})

	Context("When a second ValidatedCommitStatus references the same PromotionStrategy", func() {
		It("leaves the tags to the one the PromotionStrategy uses", func() {
			p := newValidatedPipeline(ctx, "validated-two-gates")
			p.create()
			p.expectTag(testBranchDevelopment, p.dryHead())

			second := &promoterv1alpha1.ValidatedCommitStatus{
				Name:      p.name + "-second",
				Namespace: "default",
				Spec: promoterv1alpha1.ValidatedCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: p.name},
					Key:                  "validated-second",
					DryBranch:            p.dryBranch,
				},
			}
			Expect(k8sClient.Create(ctx, second)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, second) })

			Eventually(func(g Gomega) {
				var gate promoterv1alpha1.ValidatedCommitStatus
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(second), &gate)).To(Succeed())
				ready := meta.FindStatusCondition(gate.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(ready.Reason).To(Equal(string(promoterConditions.EnvironmentTagsOwnedByAnotherGate)))
				g.Expect(ready.Message).To(ContainSubstring(p.name))
				g.Expect(gate.Status.Environments).To(BeEmpty())
				g.Expect(gate.Status.DryBranch).To(BeNil())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				var gate promoterv1alpha1.ValidatedCommitStatus
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(p.gate), &gate)).To(Succeed())
				ready := meta.FindStatusCondition(gate.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Status).To(Equal(metav1.ConditionTrue), ready.Message)
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When spec.environments names a branch the PromotionStrategy does not have", func() {
		It("moves nothing and says why", func() {
			p := newValidatedPipeline(ctx, "validated-bad-branch")
			p.gate.Spec.Environments = []promoterv1alpha1.ValidatedCommitStatusEnvironment{
				{Branch: "environment/prod"},
			}
			p.create()

			Eventually(func(g Gomega) {
				var gate promoterv1alpha1.ValidatedCommitStatus
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(p.gate), &gate)).To(Succeed())
				ready := meta.FindStatusCondition(gate.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(ready.Message).To(ContainSubstring(`"environment/prod", which is not an environment of PromotionStrategy`))
			}, constants.EventuallyTimeout).Should(Succeed())
			Consistently(func() string {
				return p.tag(testBranchDevelopment)
			}, 3*time.Second, 500*time.Millisecond).Should(BeEmpty())
		})
	})

	Context("When it is created beside the gate a PromotionStrategy already uses", func() {
		It("starts every tag at what is live and follows validations from there, without being referenced", func() {
			p := newValidatedPipeline(ctx, "validated-beside")
			declareDependentsSuccessfulGate(p.promotionStrategy)
			old := &promoterv1alpha1.DependentsSuccessfulCommitStatus{
				Name: p.name, Namespace: "default",
				Spec: promoterv1alpha1.DependentsSuccessfulCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: p.name},
					Key:                  promoterv1alpha1.DependentsSuccessfulCommitStatusKey,
				},
			}
			Expect(k8sClient.Create(ctx, old)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, old) })
			p.create()
			first := p.dryHead()
			live := p.dryParent(first)

			By("Starting the tags at what is live in each environment")
			p.expectTag(testBranchDevelopment, first)
			p.expectTag(testBranchStaging, live)
			p.expectTagSource(testBranchStaging, promoterv1alpha1.ValidatedTagSourceSeeded)
			p.expectTag(testBranchProduction, live)
			p.expectTagSource(testBranchProduction, promoterv1alpha1.ValidatedTagSourceSeeded)

			By("Promoting with the old gate and hydrators that still read the dry branch")
			for _, proposed := range []string{testBranchDevelopmentNext, testBranchStagingNext, testBranchProductionNext} {
				Expect(hydrateEnvironment(ctx, p.gitPath, proposed, first, "hydrate from the dry branch")).To(Succeed())
			}
			p.expectLive(testBranchDevelopment, first)
			p.expectLive(testBranchStaging, first)
			p.expectLive(testBranchProduction, first)

			By("Moving the tags along as each environment succeeds, ready for the switch")
			p.expectTag(testBranchStaging, first)
			p.expectTag(testBranchProduction, first)
			Eventually(func(g Gomega) {
				var ps promoterv1alpha1.PromotionStrategy
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(p.promotionStrategy), &ps)).To(Succeed())
				g.Expect(ps.Spec.OrderCommitStatusRef.Kind).To(Equal(promoterv1alpha1.DefaultOrderCommitStatusKind))
				ready := meta.FindStatusCondition(ps.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Status).To(Equal(metav1.ConditionTrue), ready.Message)
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})

var _ = Describe("ValidatedCommitStatus rules", func() {
	const (
		shaOne = "1111111111111111111111111111111111111111"
		shaTwo = "2222222222222222222222222222222222222222"
	)
	passing := []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{{Key: "argocd-health", Phase: "success"}}
	pending := []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{{Key: "argocd-health", Phase: "pending"}}

	environment := func(activeDry, proposedDry, noteDry string, active []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase) promoterv1alpha1.EnvironmentStatus {
		status := promoterv1alpha1.EnvironmentStatus{
			Branch: testBranchStaging,
			Active: promoterv1alpha1.CommitBranchState{
				Dry:            promoterv1alpha1.CommitShaState{Sha: activeDry},
				CommitStatuses: active,
			},
			Proposed: promoterv1alpha1.CommitBranchState{
				Dry:      promoterv1alpha1.CommitShaState{Sha: proposedDry},
				Hydrated: promoterv1alpha1.CommitShaState{Sha: "3333333333333333333333333333333333333333"},
			},
		}
		if noteDry != "" {
			status.Proposed.Note = &promoterv1alpha1.HydratorMetadata{DrySha: noteDry}
		}
		return status
	}

	DescribeTable("the dry commit an environment has succeeded on",
		func(observed promoterv1alpha1.EnvironmentStatus, expected string) {
			Expect(succeededOn(observed)).To(Equal(expected))
		},
		Entry("is the live commit when every active commit status passes", environment(shaOne, shaOne, "", passing), shaOne),
		Entry("is the live commit when there are no active commit statuses", environment(shaOne, shaOne, "", nil), shaOne),
		Entry("is none while an active commit status is pending", environment(shaOne, shaOne, "", pending), ""),
		Entry("is none while nothing is live", environment("", shaOne, "", passing), ""),
		Entry("is the live commit while a newer one is proposed", environment(shaOne, shaTwo, shaTwo, passing), shaOne),
		Entry("is the commit a hydrator note records when hydrating it changed nothing", environment(shaOne, shaOne, shaTwo, passing), shaTwo),
		Entry("is none for a commit that changed nothing while an active commit status is pending", environment(shaOne, shaOne, shaTwo, pending), ""),
		Entry("is none when the hydrator recorded something that is not a commit", environment("n/a", "n/a", "", passing), ""),
		Entry("is none when a hydrator note records something that is not a commit", environment(shaOne, shaOne, "4f2a9c", passing), ""),
	)

	DescribeTable("what the gate reports on a proposed commit",
		func(observed promoterv1alpha1.EnvironmentStatus, tagSha string, phase promoterv1alpha1.CommitStatusPhase, description string) {
			env := &validatedEnvironment{branch: testBranchStaging, tagName: "environment/staging-tag", observed: observed}
			gotPhase, gotDescription := gateReport(env, tagSha)
			Expect(gotPhase).To(Equal(phase))
			Expect(gotDescription).To(Equal(description))
		},
		Entry("passes when the commit was hydrated from the tag",
			environment(shaOne, shaTwo, "", nil), shaTwo,
			promoterv1alpha1.CommitPhaseSuccess, "Proposed commit matches environment/staging-tag"),
		Entry("passes when the hydrator note says the tag's commit produced the same manifests",
			environment("", shaOne, shaTwo, nil), shaTwo,
			promoterv1alpha1.CommitPhaseSuccess, "Proposed commit matches environment/staging-tag"),
		Entry("waits for the hydrator when the commit was hydrated from somewhere else",
			environment(shaOne, shaTwo, "", nil), shaOne,
			promoterv1alpha1.CommitPhasePending, "Waiting for hydrator to process environment/staging-tag at 1111111 (currently 2222222)"),
		Entry("waits for the hydrator when nothing was hydrated",
			environment(shaOne, "", "", nil), shaOne,
			promoterv1alpha1.CommitPhasePending, "Waiting for hydrator to process environment/staging-tag at 1111111 (currently none)"),
		Entry("waits for a validation while the tag does not exist",
			environment("", shaTwo, "", nil), "",
			promoterv1alpha1.CommitPhasePending, "Waiting for a validation: environment/staging-tag does not exist yet"),
	)

	It("only reports on an environment that has a proposed change", func() {
		Expect(hasProposal(&validatedEnvironment{observed: environment(shaOne, shaTwo, "", nil)})).To(BeTrue())
		Expect(hasProposal(&validatedEnvironment{observed: environment(shaOne, shaOne, shaTwo, nil)})).To(BeFalse(), "a note alone proposes nothing")
		Expect(hasProposal(&validatedEnvironment{})).To(BeFalse(), "nothing is reported for the environment yet")
	})

	It("indexes a gate by the commits it last saw on the dry branch and on each tag", func() {
		gate := &promoterv1alpha1.ValidatedCommitStatus{}
		Expect(validatedCommitStatusRefShas(gate)).To(BeEmpty())

		gate.Status.DryBranch = &promoterv1alpha1.ValidatedCommitStatusDryBranchStatus{Sha: shaTwo}
		gate.Status.Environments = []promoterv1alpha1.ValidatedCommitStatusEnvironmentStatus{
			{Branch: testBranchDevelopment, Tag: &promoterv1alpha1.ValidatedCommitStatusTagStatus{Name: "environment/development-tag", Sha: shaTwo}},
			{Branch: testBranchStaging, Tag: &promoterv1alpha1.ValidatedCommitStatusTagStatus{Name: "environment/staging-tag", Sha: shaOne}},
			{Branch: testBranchProduction, Tag: &promoterv1alpha1.ValidatedCommitStatusTagStatus{Name: "environment/production-tag"}},
			{Branch: "environment/extra"},
		}
		Expect(validatedCommitStatusRefShas(gate)).To(Equal([]string{shaTwo, shaOne}))
	})

	It("tells when a PromotionStrategy is ordered by a ValidatedCommitStatus", func() {
		ps := &promoterv1alpha1.PromotionStrategy{}
		ps.Spec.OrderCommitStatusRef = promoterv1alpha1.OrderCommitStatusRef{Name: "demo"}
		Expect(referencedValidatedCommitStatus(ps)).To(BeEmpty(), "the default kind is DependentsSuccessfulCommitStatus")

		ps.Spec.OrderCommitStatusRef.Kind = promoterv1alpha1.ValidatedOrderCommitStatusKind
		Expect(referencedValidatedCommitStatus(ps)).To(Equal("demo"))

		ps.Spec.OrderCommitStatusRef.Group = "ordering.example.com"
		Expect(referencedValidatedCommitStatus(ps)).To(BeEmpty(), "a kind of the same name in another group is someone else's")
	})

	It("spaces the nudges of one ChangeTransferPolicy, and not those of another", func() {
		reconciler := &ValidatedCommitStatusReconciler{}
		start := time.Now()
		one := client.ObjectKey{Namespace: "default", Name: "one"}
		other := client.ObjectKey{Namespace: "default", Name: "other"}

		Expect(reconciler.shouldNudge(one, start, 15*time.Second)).To(BeTrue())
		Expect(reconciler.shouldNudge(one, start.Add(5*time.Second), 15*time.Second)).To(BeFalse())
		Expect(reconciler.shouldNudge(other, start.Add(5*time.Second), 15*time.Second)).To(BeTrue())
		Expect(reconciler.shouldNudge(one, start.Add(15*time.Second), 15*time.Second)).To(BeTrue())

		By("Forgetting a ChangeTransferPolicy that has not been nudged for longer than nudging goes on")
		Expect(reconciler.shouldNudge(other, start.Add(time.Hour), 15*time.Second)).To(BeTrue())
		Expect(reconciler.lastNudges).To(HaveLen(1))
	})

	Describe("whether to work out where a tag would be seeded", func() {
		tag := func(sha string) *promoterv1alpha1.ValidatedCommitStatusTagStatus {
			return &promoterv1alpha1.ValidatedCommitStatusTagStatus{Name: "environment/staging-tag", Sha: sha}
		}
		existing := git.TagState{Raw: shaOne, Commit: shaOne}

		It("does for a tag that does not exist and was never looked at", func() {
			run := &validatedRun{firstRun: true}
			Expect(run.needsSeed(&validatedEnvironment{})).To(BeTrue())
		})

		It("does not, again and again, for an environment that waits for a validation with nothing live", func() {
			run := &validatedRun{}
			env := &validatedEnvironment{}
			env.previous.Tag = tag("")
			Expect(run.needsSeed(env)).To(BeFalse())
		})

		It("does once something is live, a validation arrived, or the tag was deleted", func() {
			run := &validatedRun{}

			live := &validatedEnvironment{}
			live.previous.Tag = tag("")
			live.observed.Active.Dry.Sha = shaOne
			Expect(run.needsSeed(live)).To(BeTrue())

			validated := &validatedEnvironment{unapplied: []validations.Validation{{Sha: shaTwo, Automatic: true}}}
			validated.previous.Tag = tag("")
			Expect(run.needsSeed(validated)).To(BeTrue())

			deleted := &validatedEnvironment{}
			deleted.previous.Tag = tag(shaOne)
			Expect(run.needsSeed(deleted)).To(BeTrue())
		})

		It("does not for a tag that exists, unless the controller has no record of it", func() {
			known := &validatedEnvironment{remote: existing}
			known.previous.Tag = tag(shaOne)
			Expect((&validatedRun{}).needsSeed(known)).To(BeFalse())

			unrecorded := &validatedEnvironment{remote: existing}
			unrecorded.previous.Tag = tag("")
			Expect((&validatedRun{}).needsSeed(unrecorded)).To(BeTrue())
			Expect((&validatedRun{firstRun: true}).needsSeed(unrecorded)).To(BeFalse(), "on a first run every tag is taken as found")
		})
	})
})
