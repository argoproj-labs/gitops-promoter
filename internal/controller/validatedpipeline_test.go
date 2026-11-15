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
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/validations"
)

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
			DrySource:            promoterv1alpha1.ValidatedCommitStatusDrySource{Branch: p.dryBranch},
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
func (p *validatedPipeline) commitDry() string {
	GinkgoHelper()
	sha, err := makeDryCommit(p.ctx, p.gitPath, "another change")
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

// validateByHand validates a dry commit for an environment the way a person without a CommitValidation
// does: by pushing a line to the validation note of the commit. Only the target is written, as the
// documentation asks for.
func (p *validatedPipeline) validateByHand(branch, drySha, reason string) {
	GinkgoHelper()
	p.pushValidationLine(drySha, `{"target":"`+validations.TagName(branch, "")+`","reason":"`+reason+`"}`)
}

// pushValidationLine appends a line to the validation note of a dry commit and pushes the notes, the way
// anyone with push access to the repository can. No webhook announces a note, so the gate is enqueued,
// standing in for its poll.
func (p *validatedPipeline) pushValidationLine(drySha, line string) {
	GinkgoHelper()
	// The controller writes to the same ref, so a push can lose the race and has to start over.
	Eventually(func() error {
		refspec := "+" + git.PromoterValidationsNotesRef + ":" + git.PromoterValidationsNotesRef
		if _, err := runGitCmd(p.ctx, p.gitPath, "fetch", "origin", refspec); err != nil && !strings.Contains(err.Error(), "couldn't find remote ref") {
			return err
		}
		if _, err := runGitCmd(p.ctx, p.gitPath, "notes", "--ref="+git.PromoterValidationsNotesRef, "append", "-m", line, drySha); err != nil {
			return err
		}
		_, err := runGitCmd(p.ctx, p.gitPath, "push", "origin", git.PromoterValidationsNotesRef)
		return err
	}, constants.EventuallyTimeout, time.Second).Should(Succeed())
	enqueueValidatedCommitStatus(p.gate.Namespace, p.gate.Name)
}

// moveTagByHand points an environment's tag at a dry commit the way a person with push access does.
func (p *validatedPipeline) moveTagByHand(branch, drySha string) {
	GinkgoHelper()
	ref := "refs/tags/" + validations.TagName(branch, "")
	before := p.remoteRef(ref)
	p.git("push", "--force", "origin", drySha+":"+ref)
	sendWebhookForRef(p.ctx, before, ref)
}

// expectReady waits for the gate to be Ready.
func (p *validatedPipeline) expectReady() {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var gate promoterv1alpha1.ValidatedCommitStatus
		g.Expect(k8sClient.Get(p.ctx, client.ObjectKeyFromObject(p.gate), &gate)).To(Succeed())
		ready := meta.FindStatusCondition(gate.Status.Conditions, string(promoterConditions.Ready))
		g.Expect(ready).NotTo(BeNil())
		g.Expect(ready.Status).To(Equal(metav1.ConditionTrue), ready.Message)
	}, constants.EventuallyTimeout).Should(Succeed())
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
