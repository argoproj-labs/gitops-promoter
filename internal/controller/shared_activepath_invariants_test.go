/*
Copyright 2024.

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
	"fmt"
	"math/rand"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
)

// These specs guard the invariant behind every activePath fix on a shared active branch: status.active.hydrated
// is the branch tip and moves whenever any sibling merges, but nothing environment-scoped (gates, holds,
// history, the proposed branch, events) may change for an environment whose own path did not change. A new
// consumer of the tip that forgets this fails here without anyone having to think of the case.
var _ = Describe("Shared active branch invariants", func() {
	// envSnapshot is everything about one environment that a sibling's merge must leave alone. tip is the
	// one thing that is expected to move and is excluded from the comparison.
	type envSnapshot struct {
		tip              string
		activeDry        string
		pathHydrated     string
		proposedDry      string
		proposedHydrated string
		proposedNoteDry  string
		restores         []string
		history          []string
		timedPhase       string
		timedCommitTime  string
		pullRequestOpen  bool
		events           map[string]int
	}
	watchedEventReasons := []string{constants.ResolvedConflictReason, constants.RestoreSupersededReason, constants.RestoreAdoptedReason}

	snapshot := func(ctx context.Context, g Gomega, s sharedActivePathStrategies, ps *promoterv1alpha1.PromotionStrategy, timed *promoterv1alpha1.TimedCommitStatus) envSnapshot {
		ctp := s.ctp(ctx, g, ps)
		snap := envSnapshot{
			tip:              ctp.Status.Active.Hydrated.Sha,
			activeDry:        ctp.Status.Active.Dry.Sha,
			proposedDry:      ctp.Status.Proposed.Dry.Sha,
			proposedHydrated: ctp.Status.Proposed.Hydrated.Sha,
			events:           map[string]int{},
		}
		if ctp.Status.Active.PathHydrated != nil {
			snap.pathHydrated = ctp.Status.Active.PathHydrated.Sha
		}
		if ctp.Status.Proposed.Note != nil {
			snap.proposedNoteDry = ctp.Status.Proposed.Note.DrySha
		}
		for _, rc := range liveRestoreActiveCommitsForPolicy(ctx, g, &ctp) {
			snap.restores = append(snap.restores, fmt.Sprintf("%s restoredFrom=%s activeSha=%s blocked=%s", rc.Name, rc.Status.RestoredFrom, rc.Status.ActiveSha, rc.Status.BlockedDrySha))
		}
		for _, entry := range s.historyFor(ctx, g, ps) {
			snap.history = append(snap.history, fmt.Sprintf("%s dry=%s restoredFrom=%s", entry.Active.Hydrated.Sha, entry.Active.Dry.Sha, entry.RestoredFrom))
		}
		var tcs promoterv1alpha1.TimedCommitStatus
		g.Expect(k8sClient.Get(ctx, ctrlclient.ObjectKeyFromObject(timed), &tcs)).To(Succeed())
		g.Expect(tcs.Status.Environments).To(HaveLen(1))
		snap.timedPhase = tcs.Status.Environments[0].Phase
		snap.timedCommitTime = tcs.Status.Environments[0].CommitTime.UTC().Format(time.RFC3339)
		prErr := k8sClient.Get(ctx, s.prKey(ps.Spec.ActivePath), &promoterv1alpha1.PullRequest{})
		g.Expect(ctrlclient.IgnoreNotFound(prErr)).To(Succeed())
		snap.pullRequestOpen = prErr == nil
		var events v1.EventList
		g.Expect(k8sClient.List(ctx, &events, ctrlclient.InNamespace(ps.Namespace))).To(Succeed())
		for _, reason := range watchedEventReasons {
			for _, event := range events.Items {
				if event.InvolvedObject.Name == ctp.Name && event.Reason == reason {
					snap.events[reason]++
				}
			}
		}
		return snap
	}
	// withoutTip strips the tip, which is the only field a sibling's merge is allowed to move.
	withoutTip := func(snap envSnapshot) envSnapshot {
		snap.tip = ""
		return snap
	}

	It("does not change an idle environment when a sibling promotes", func() {
		ctx := context.Background()
		// held is under a restore hold (proposed dry differs from active dry); idle has nothing pending and
		// was squash-merged, so its proposed tip is not an ancestor of the active branch and every root
		// hydrator.metadata change on active conflicts with it. mover is the sibling whose promotions must
		// change neither. Together the two idle shapes cover every environment-scoped consumer of the tip.
		s := setupSharedActivePathApps(ctx, "apps/held", "apps/mover", "apps/idle")
		held, mover, idle := s.apps[0], s.apps[1], s.apps[2]

		newTimed := func(ps *promoterv1alpha1.PromotionStrategy) *promoterv1alpha1.TimedCommitStatus {
			GinkgoHelper()
			timed := &promoterv1alpha1.TimedCommitStatus{
				Name:      ps.Name + "-timed",
				Namespace: ps.Namespace,
				Spec: promoterv1alpha1.TimedCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: ps.Name},
					Environments:         []promoterv1alpha1.TimedCommitStatusEnvironments{{Branch: testBranchDevelopment, Duration: metav1.Duration{Duration: 3 * time.Second}}},
				},
			}
			Expect(k8sClient.Create(ctx, timed)).To(Succeed())
			DeferCleanup(func() { _ = ctrlclient.IgnoreNotFound(k8sClient.Delete(ctx, timed)) })
			return timed
		}

		By("Giving the held app a history, a met timed gate and a restore hold")
		dryHeldFirst, heldFirstMerge := s.promote(ctx, held, held.Spec.ActivePath, "held first")
		dryHeldSecond, _ := s.promote(ctx, held, held.Spec.ActivePath, "held second")
		heldTimed := newTimed(held)
		rc := s.restore(ctx, held, heldFirstMerge)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, ctrlclient.ObjectKeyFromObject(rc), rc)).To(Succeed())
			g.Expect(rc.Status.RestoredFrom).To(Equal(heldFirstMerge))
			g.Expect(rc.Status.BlockedDrySha).To(Equal(dryHeldSecond))
		}, constants.EventuallyTimeout).Should(Succeed())

		By("Giving the idle app a squash-merged promotion and a met timed gate")
		dryIdle := s.squashMerge(ctx, idle, "idle squash-merged")
		idleTimed := newTimed(idle)

		var heldBefore, idleBefore envSnapshot
		Eventually(func(g Gomega) {
			s.nudge(held)
			s.nudge(idle)
			heldBefore = snapshot(ctx, g, s, held, heldTimed)
			g.Expect(heldBefore.activeDry).To(Equal(dryHeldFirst), "the restore has been observed")
			g.Expect(heldBefore.pathHydrated).To(Equal(rc.Status.ActiveSha), "the restore commit is the held app's path hydrated commit")
			g.Expect(heldBefore.restores).To(HaveLen(1))
			g.Expect(heldBefore.timedPhase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)), "the held app's soak has completed")
			g.Expect(heldBefore.history).NotTo(BeEmpty())
			idleBefore = snapshot(ctx, g, s, idle, idleTimed)
			g.Expect(idleBefore.activeDry).To(Equal(dryIdle))
			g.Expect(idleBefore.proposedDry).To(Equal(dryIdle), "the idle app has nothing pending")
			g.Expect(idleBefore.pullRequestOpen).To(BeFalse())
			g.Expect(idleBefore.timedPhase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)), "the idle app's soak has completed")
		}, constants.EventuallyTimeout).Should(Succeed())

		By("Promoting the mover twice: once through its own pull request, once as an SCM squash merge")
		s.promote(ctx, mover, mover.Spec.ActivePath, "mover first")
		s.squashMerge(ctx, mover, "mover squash-merged")

		By("Verifying the shared tip moved for both other apps")
		Eventually(func(g Gomega) {
			s.nudge(held)
			s.nudge(idle)
			g.Expect(s.ctp(ctx, g, held).Status.Active.Hydrated.Sha).NotTo(Equal(heldBefore.tip))
			g.Expect(s.ctp(ctx, g, idle).Status.Active.Hydrated.Sha).NotTo(Equal(idleBefore.tip))
		}, constants.EventuallyTimeout).Should(Succeed())

		By("Verifying nothing environment-scoped about the held or idle app changed, and the mover acquired no hold")
		Consistently(func(g Gomega) {
			for _, ps := range s.apps {
				s.nudge(ps)
			}
			g.Expect(withoutTip(snapshot(ctx, g, s, held, heldTimed))).To(Equal(withoutTip(heldBefore)), "a sibling's merge must not change a held environment")
			g.Expect(withoutTip(snapshot(ctx, g, s, idle, idleTimed))).To(Equal(withoutTip(idleBefore)), "a sibling's merge must not change an idle environment")
			ctpMover := s.ctp(ctx, g, mover)
			g.Expect(liveRestoreActiveCommitsForPolicy(ctx, g, &ctpMover)).To(BeEmpty(), "the mover must not adopt the held app's restore")
		}, 8*time.Second, 500*time.Millisecond).Should(Succeed())
	})

	It("keeps every environment's state its own across randomly interleaved promotions and restores", func() {
		ctx := context.Background()
		s := setupSharedActivePathApps(ctx, "apps/alpha", "apps/beta", "apps/gamma")
		seed := GinkgoRandomSeed()
		rng := rand.New(rand.NewSource(seed))
		GinkgoWriter.Printf("interleaving seed %d\n", seed)

		// entry is one commit this environment expects in its history, newest first.
		type entry struct {
			Dry      string
			Sha      string
			Restored bool
		}
		type appState struct {
			entries     []entry
			proposedTip string
		}
		state := map[*promoterv1alpha1.PromotionStrategy]*appState{}
		for _, ps := range s.apps {
			state[ps] = &appState{}
		}
		historyEntries := func(history []promoterv1alpha1.History) []entry {
			got := make([]entry, 0, len(history))
			for _, h := range history {
				got = append(got, entry{Dry: h.Active.Dry.Sha, Sha: h.Active.Hydrated.Sha, Restored: h.RestoredFrom != ""})
			}
			return got
		}

		// check asserts every environment's state against what its own steps produced. held may own one
		// RestoreActiveCommit; nobody else may.
		check := func(held *promoterv1alpha1.PromotionStrategy) {
			GinkgoHelper()
			Eventually(func(g Gomega) {
				for _, ps := range s.apps {
					s.nudge(ps)
					st := state[ps]
					ctp := s.ctp(ctx, g, ps)
					g.Expect(ctp.Status.Active.Dry.Sha).To(Equal(st.entries[0].Dry), "%s: active dry sha", ps.Spec.ActivePath)
					g.Expect(ctp.Status.Active.PathHydrated).NotTo(BeNil(), "%s: path hydrated", ps.Spec.ActivePath)
					g.Expect(ctp.Status.Active.PathHydrated.Sha).To(Equal(st.entries[0].Sha), "%s: path hydrated must be this app's own last commit", ps.Spec.ActivePath)
					g.Expect(ctp.Status.Proposed.Hydrated.Sha).To(Equal(st.proposedTip), "%s: proposed branch must not be rewritten by siblings", ps.Spec.ActivePath)
					racs := liveRestoreActiveCommitsForPolicy(ctx, g, &ctp)
					if ps == held {
						g.Expect(racs).To(HaveLen(1), "%s: its own hold", ps.Spec.ActivePath)
					} else {
						g.Expect(racs).To(BeEmpty(), "%s: must not hold a sibling's restore", ps.Spec.ActivePath)
					}
					g.Expect(historyEntries(s.historyFor(ctx, g, ps))).To(Equal(st.entries), "%s: history must be exactly this app's own commits", ps.Spec.ActivePath)
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		}

		promote := func(ps *promoterv1alpha1.PromotionStrategy, message string) {
			GinkgoHelper()
			dry, tip := s.promote(ctx, ps, ps.Spec.ActivePath, message)
			st := state[ps]
			Eventually(func(g Gomega) {
				ctp := s.ctp(ctx, g, ps)
				g.Expect(ctp.Status.Proposed.Dry.Sha).To(Equal(dry))
				st.proposedTip = ctp.Status.Proposed.Hydrated.Sha
			}, constants.EventuallyTimeout).Should(Succeed())
			st.entries = append([]entry{{Dry: dry, Sha: tip}}, st.entries...)
		}

		// restoreTarget is the newest earlier promotion (never a restore commit, which cannot be a target).
		restoreTarget := func(ps *promoterv1alpha1.PromotionStrategy) (entry, bool) {
			for _, e := range state[ps].entries[1:] {
				if !e.Restored {
					return e, true
				}
			}
			return entry{}, false
		}

		restoreThenPromote := func(ps *promoterv1alpha1.PromotionStrategy, target entry, step int) {
			GinkgoHelper()
			st := state[ps]
			rc := s.restore(ctx, ps, target.Sha)
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, ctrlclient.ObjectKeyFromObject(rc), rc)).To(Succeed())
				g.Expect(rc.Status.RestoredFrom).To(Equal(target.Sha))
				g.Expect(rc.Status.BlockedDrySha).To(Equal(st.entries[0].Dry))
			}, constants.EventuallyTimeout).Should(Succeed())
			st.entries = append([]entry{{Dry: target.Dry, Sha: rc.Status.ActiveSha, Restored: true}}, st.entries...)
			check(ps)

			// Hydrate the next change while held so releasing the hold promotes only it, then release.
			dry := s.hydrate(ctx, ps.Spec.ActivePath, fmt.Sprintf("step %d: %s after restore", step, ps.Spec.ActivePath))
			Eventually(func(g Gomega) {
				s.nudge(ps)
				g.Expect(s.ctp(ctx, g, ps).Status.Proposed.Dry.Sha).To(Equal(dry))
				g.Expect(k8sClient.Get(ctx, s.prKey(ps.Spec.ActivePath), &promoterv1alpha1.PullRequest{})).To(Succeed(), "the held app opens but does not merge its pull request")
			}, constants.EventuallyTimeout).Should(Succeed())
			clearRevertBlockEnvironment(ctx, ctrlclient.ObjectKeyFromObject(rc))
			var tip string
			Eventually(func(g Gomega) {
				s.nudge(ps)
				g.Expect(errors.IsNotFound(k8sClient.Get(ctx, ctrlclient.ObjectKeyFromObject(rc), &promoterv1alpha1.RestoreActiveCommit{}))).To(BeTrue())
				ctp := s.ctp(ctx, g, ps)
				g.Expect(ctp.Status.Active.Dry.Sha).To(Equal(dry))
				tip = ctp.Status.Active.Hydrated.Sha
				st.proposedTip = ctp.Status.Proposed.Hydrated.Sha
			}, constants.EventuallyTimeout).Should(Succeed())
			st.entries = append([]entry{{Dry: dry, Sha: tip}}, st.entries...)
		}

		By("Promoting every app once so each has a baseline")
		for _, ps := range s.apps {
			promote(ps, "baseline "+ps.Spec.ActivePath)
		}
		check(nil)

		const steps = 24
		for step := 1; step <= steps; step++ {
			ps := s.apps[rng.Intn(len(s.apps))]
			if target, ok := restoreTarget(ps); ok && rng.Float64() < 0.3 {
				By(fmt.Sprintf("step %d: restore %s to %s, then promote it again", step, ps.Spec.ActivePath, target.Sha[:8]))
				restoreThenPromote(ps, target, step)
			} else {
				By(fmt.Sprintf("step %d: promote %s", step, ps.Spec.ActivePath))
				promote(ps, fmt.Sprintf("step %d: %s", step, ps.Spec.ActivePath))
			}
			check(nil)
		}
	})
})
