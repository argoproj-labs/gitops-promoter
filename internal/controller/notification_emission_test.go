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
	"reflect"
	"sync"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	notificationevents "github.com/argoproj-labs/gitops-promoter/internal/notification/events"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// These tests exercise the reconcilers' notification EMISSION wiring in isolation: the code
// that snapshots the prior status, diffs it against the freshly computed status, and calls
// Publisher.Publish with the right Event. This is the boundary the envtest notification suite
// (internal/controller/notification) deliberately stubs by publishing directly onto the broker.
//
// Why a focused emission test rather than a full git-driven transition: driving a REAL
// CTPActive/PromotionComplete through calculateStatus requires the gitkit/hydrator harness
// (heavy, flake-prone). publishShaTransitions / publishEnvironmentTransitions are pure functions
// of (prev snapshot, current status, Publisher), so invoking them directly with fabricated
// status proves the emission wiring fires correctly without git or etcd. If this wiring
// regresses (e.g. stops calling Publish, or emits the wrong type/fields), these tests fail even
// though the broker-driven envtest suite would stay green.

// capturePublisher records every event passed to Publish. It is safe for concurrent use.
type capturePublisher struct {
	events []notificationevents.Event
	mu     sync.Mutex
}

func (c *capturePublisher) Publish(_ context.Context, e notificationevents.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
	return nil
}

func (c *capturePublisher) all() []notificationevents.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]notificationevents.Event, len(c.events))
	copy(out, c.events)
	return out
}

func (c *capturePublisher) byType(t notificationevents.EventType) []notificationevents.Event {
	var out []notificationevents.Event
	for _, e := range c.all() {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	// Shared literals for the emission tests (kept as consts to satisfy goconst).
	emitNamespace = "ns-emit"
	emitCTPName   = "ctp-emit"
	emitPSName    = "ps-emit"
	labelTeam     = "team"
	labelApp      = "app"
	labelPayments = "payments"
	emitGateKey   = "argocd-health"
)

// kindCTP / kindPS are the Kind names of the originating resources, derived from the types so
// no string literal duplicates the template-data map keys used in production code (goconst).
var (
	kindCTP = reflect.TypeFor[promoterv1alpha1.ChangeTransferPolicy]().Name()
	kindPS  = reflect.TypeFor[promoterv1alpha1.PromotionStrategy]().Name()
)

// --- ChangeTransferPolicy emission: publishShaTransitions ---

func newCTP(activeDry, proposedDry string) *promoterv1alpha1.ChangeTransferPolicy {
	ctp := &promoterv1alpha1.ChangeTransferPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      emitCTPName,
			Namespace: emitNamespace,
			Labels:    map[string]string{labelTeam: labelPayments},
		},
		Spec: promoterv1alpha1.ChangeTransferPolicySpec{
			ActiveBranch:   testBranchProduction,
			ProposedBranch: testBranchProductionNext,
		},
	}
	ctp.Status.Active.Dry.Sha = activeDry
	ctp.Status.Proposed.Dry.Sha = proposedDry
	return ctp
}

func TestPublishShaTransitions_EmitsCTPActiveOnActiveShaAdvance(t *testing.T) {
	t.Parallel()
	pub := &capturePublisher{}
	r := &ChangeTransferPolicyReconciler{Publisher: pub}

	// Active dry sha advances from shaA -> shaB; proposed unchanged.
	ctp := newCTP(shaB, shaA)
	r.publishShaTransitions(context.Background(), ctp, shaA /*prevProposed*/, shaA /*prevActive*/)

	active := pub.byType(notificationevents.TypeCTPActive)
	if len(active) != 1 {
		t.Fatalf("expected exactly 1 CTPActive event, got %d (all=%+v)", len(active), pub.all())
	}
	e := active[0]
	if e.Object.Kind != kindCTP || e.Object.Namespace != emitNamespace || e.Object.Name != emitCTPName {
		t.Errorf("CTPActive object ref = %+v, want ns-emit/ChangeTransferPolicy/ctp-emit", e.Object)
	}
	if e.PreviousSha != shaA || e.NewSha != shaB {
		t.Errorf("CTPActive shas = prev %q new %q, want prev %q new %q", e.PreviousSha, e.NewSha, shaA, shaB)
	}
	if e.Environment != testBranchProduction {
		t.Errorf("CTPActive Environment = %q, want environment/production", e.Environment)
	}
	if e.Labels[labelTeam] != labelPayments {
		t.Errorf("CTPActive Labels = %+v, want team=payments (from CR labels)", e.Labels)
	}
	// Proposed unchanged => no CTPProposed.
	if got := len(pub.byType(notificationevents.TypeCTPProposed)); got != 0 {
		t.Errorf("expected 0 CTPProposed events when proposed sha unchanged, got %d", got)
	}
}

func TestPublishShaTransitions_EmitsCTPProposedOnProposedShaAdvance(t *testing.T) {
	t.Parallel()
	pub := &capturePublisher{}
	r := &ChangeTransferPolicyReconciler{Publisher: pub}

	// Proposed dry sha advances; active unchanged.
	ctp := newCTP(shaA, shaB)
	r.publishShaTransitions(context.Background(), ctp, shaA /*prevProposed*/, shaA /*prevActive*/)

	proposed := pub.byType(notificationevents.TypeCTPProposed)
	if len(proposed) != 1 {
		t.Fatalf("expected exactly 1 CTPProposed event, got %d (all=%+v)", len(proposed), pub.all())
	}
	if proposed[0].PreviousSha != shaA || proposed[0].NewSha != shaB {
		t.Errorf("CTPProposed shas = prev %q new %q, want prev %q new %q", proposed[0].PreviousSha, proposed[0].NewSha, shaA, shaB)
	}
	if got := len(pub.byType(notificationevents.TypeCTPActive)); got != 0 {
		t.Errorf("expected 0 CTPActive events when active sha unchanged, got %d", got)
	}
}

// newCTPOwnedBy returns a CTP whose controller OwnerReference points at the given PromotionStrategy
// name, so getPromotionStrategy will resolve it. The CTP keeps its own system label "team=payments".
func newCTPOwnedBy(activeDry, proposedDry, psName string) *promoterv1alpha1.ChangeTransferPolicy {
	ctp := newCTP(activeDry, proposedDry)
	ctp.OwnerReferences = []metav1.OwnerReference{
		{
			APIVersion: promoterv1alpha1.GroupVersion.String(),
			Kind:       kindPS,
			Name:       psName,
			Controller: ptr.To(true),
		},
	}
	return ctp
}

// TestPublishShaTransitions_EnrichesEventLabelsFromOwningPS verifies Option 3: a CTP-sourced
// event is enriched with the owning PromotionStrategy's user labels at emit time, so selector
// matching is uniform with PS-sourced events. The CTP's own labels must still win on conflict.
func TestPublishShaTransitions_EnrichesEventLabelsFromOwningPS(t *testing.T) {
	t.Parallel()
	pub := &capturePublisher{}

	ps := &promoterv1alpha1.PromotionStrategy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      emitPSName,
			Namespace: emitNamespace,
			Labels: map[string]string{
				labelApp:  labelPayments,    // user label only on the PS -> should be merged in
				labelTeam: "platform-on-ps", // conflicts with CTP's team=payments -> CTP must win
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(utils.GetScheme()).WithObjects(ps).Build()
	r := &ChangeTransferPolicyReconciler{Client: cl, Publisher: pub}

	// Both shas advance so we get both a CTPActive and a CTPProposed sharing the enriched labels.
	ctp := newCTPOwnedBy(shaB, shaB, emitPSName)
	r.publishShaTransitions(context.Background(), ctp, shaA /*prevProposed*/, shaA /*prevActive*/)

	events := pub.all()
	if len(events) != 2 {
		t.Fatalf("expected 2 events (CTPActive + CTPProposed), got %d (%+v)", len(events), events)
	}
	for _, e := range events {
		// PS-only label merged in.
		if e.Labels[labelApp] != labelPayments {
			t.Errorf("%s Labels[app] = %q, want payments (merged from owning PS)", e.Type, e.Labels[labelApp])
		}
		// CTP's own label wins the conflict over the PS's value.
		if e.Labels[labelTeam] != labelPayments {
			t.Errorf("%s Labels[team] = %q, want payments (CTP label must win over PS)", e.Type, e.Labels[labelTeam])
		}
	}
}

// TestPublishShaTransitions_NoOwnerPSStillEmitsWithCTPLabels verifies the best-effort path: a CTP
// with no owning PromotionStrategy still emits, carrying only its own labels, and does not panic
// (getPromotionStrategy returns (nil,nil) without touching the client).
func TestPublishShaTransitions_NoOwnerPSStillEmitsWithCTPLabels(t *testing.T) {
	t.Parallel()
	pub := &capturePublisher{}
	// No Client set: with no PS owner reference, getPromotionStrategy must short-circuit before
	// any client call, so a nil Client is safe here.
	r := &ChangeTransferPolicyReconciler{Publisher: pub}

	ctp := newCTP(shaB, shaA) // no OwnerReferences
	r.publishShaTransitions(context.Background(), ctp, shaA /*prevProposed*/, shaA /*prevActive*/)

	active := pub.byType(notificationevents.TypeCTPActive)
	if len(active) != 1 {
		t.Fatalf("expected 1 CTPActive event, got %d", len(active))
	}
	if active[0].Labels[labelTeam] != labelPayments {
		t.Errorf("CTPActive Labels[team] = %q, want payments (CTP label)", active[0].Labels[labelTeam])
	}
	if _, ok := active[0].Labels[labelApp]; ok {
		t.Errorf("CTPActive Labels unexpectedly has app=%q; no owning PS means no enrichment", active[0].Labels[labelApp])
	}
}

func TestPublishShaTransitions_NoEventWhenShaUnchanged(t *testing.T) {
	t.Parallel()
	pub := &capturePublisher{}
	r := &ChangeTransferPolicyReconciler{Publisher: pub}

	// Nothing advanced: current == prev for both.
	ctp := newCTP(shaA, shaB)
	r.publishShaTransitions(context.Background(), ctp, shaB /*prevProposed*/, shaA /*prevActive*/)

	if got := len(pub.all()); got != 0 {
		t.Fatalf("expected no events when no sha advanced, got %d (%+v)", got, pub.all())
	}
}

func TestPublishShaTransitions_NilPublisherIsNoOp(t *testing.T) {
	t.Parallel()
	// Publisher nil => the notification framework is disabled; emission must be a safe no-op
	// and never panic, so a reconcile is never broken by notifications being off.
	r := &ChangeTransferPolicyReconciler{Publisher: nil}
	ctp := newCTP(shaB, shaB)
	// Must not panic.
	r.publishShaTransitions(context.Background(), ctp, shaA, shaA)
}

// --- PromotionStrategy emission: publishEnvironmentTransitions ---

func envStatus(activeDry string, proposedGate *promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase) promoterv1alpha1.EnvironmentStatus {
	es := promoterv1alpha1.EnvironmentStatus{Branch: testBranchProduction}
	es.Active.Dry.Sha = activeDry
	es.Proposed.Dry.Sha = shaB
	if proposedGate != nil {
		es.Proposed.CommitStatuses = []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{*proposedGate}
	}
	return es
}

func newPS(envs ...promoterv1alpha1.EnvironmentStatus) *promoterv1alpha1.PromotionStrategy {
	ps := &promoterv1alpha1.PromotionStrategy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      emitPSName,
			Namespace: emitNamespace,
			Labels:    map[string]string{labelTeam: labelPayments},
		},
	}
	ps.Status.Environments = envs
	return ps
}

func TestPublishEnvironmentTransitions_EmitsPromotionCompleteOnActiveAdvance(t *testing.T) {
	t.Parallel()
	pub := &capturePublisher{}
	r := &PromotionStrategyReconciler{Publisher: pub}

	// Current: production active dry sha is shaB.
	ps := newPS(envStatus(shaB, nil))
	// Prior snapshot: same branch had active dry sha shaA => advanced.
	prev := []promoterv1alpha1.EnvironmentStatus{envStatus(shaA, nil)}

	r.publishEnvironmentTransitions(context.Background(), ps, prev)

	complete := pub.byType(notificationevents.TypePromotionComplete)
	if len(complete) != 1 {
		t.Fatalf("expected exactly 1 PromotionComplete event, got %d (all=%+v)", len(complete), pub.all())
	}
	e := complete[0]
	if e.Object.Kind != kindPS || e.Object.Namespace != emitNamespace || e.Object.Name != emitPSName {
		t.Errorf("PromotionComplete object ref = %+v, want ns-emit/PromotionStrategy/ps-emit", e.Object)
	}
	if e.Environment != testBranchProduction {
		t.Errorf("PromotionComplete Environment = %q, want environment/production", e.Environment)
	}
	if e.PreviousSha != shaA || e.NewSha != shaB {
		t.Errorf("PromotionComplete shas = prev %q new %q, want prev %q new %q", e.PreviousSha, e.NewSha, shaA, shaB)
	}
	if e.Labels[labelTeam] != labelPayments {
		t.Errorf("PromotionComplete Labels = %+v, want team=payments", e.Labels)
	}
}

func TestPublishEnvironmentTransitions_NoPromotionCompleteWhenNoPriorSnapshot(t *testing.T) {
	t.Parallel()
	pub := &capturePublisher{}
	r := &PromotionStrategyReconciler{Publisher: pub}

	// Current has an active sha but there is NO prior snapshot for the branch: the emission
	// guard requires hadPrev, so the first observation must NOT emit (avoids spurious events on
	// controller startup / first reconcile).
	ps := newPS(envStatus(shaB, nil))
	r.publishEnvironmentTransitions(context.Background(), ps, nil /*no prior*/)

	if got := len(pub.byType(notificationevents.TypePromotionComplete)); got != 0 {
		t.Fatalf("expected 0 PromotionComplete events with no prior snapshot, got %d", got)
	}
}

func TestPublishEnvironmentTransitions_EmitsGateFailedOnTransitionIntoFailure(t *testing.T) {
	t.Parallel()
	pub := &capturePublisher{}
	r := &PromotionStrategyReconciler{Publisher: pub}

	failGate := &promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
		Key:   emitGateKey,
		Phase: string(promoterv1alpha1.CommitPhaseFailure),
	}
	// Current: gate is failing; active sha unchanged so no PromotionComplete.
	ps := newPS(envStatus(shaA, failGate))
	// Prior: same branch, same active sha, gate was pending.
	pendingGate := &promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
		Key:   emitGateKey,
		Phase: string(promoterv1alpha1.CommitPhasePending),
	}
	prev := []promoterv1alpha1.EnvironmentStatus{envStatus(shaA, pendingGate)}

	r.publishEnvironmentTransitions(context.Background(), ps, prev)

	failed := pub.byType(notificationevents.TypeGateFailed)
	if len(failed) != 1 {
		t.Fatalf("expected exactly 1 GateFailed event, got %d (all=%+v)", len(failed), pub.all())
	}
	e := failed[0]
	if e.GateName != emitGateKey {
		t.Errorf("GateFailed GateName = %q, want argocd-health", e.GateName)
	}
	if e.NewState != string(promoterv1alpha1.CommitPhaseFailure) {
		t.Errorf("GateFailed NewState = %q, want failure", e.NewState)
	}
	if e.PreviousState != string(promoterv1alpha1.CommitPhasePending) {
		t.Errorf("GateFailed PreviousState = %q, want pending", e.PreviousState)
	}
	// Active sha unchanged => no PromotionComplete.
	if got := len(pub.byType(notificationevents.TypePromotionComplete)); got != 0 {
		t.Errorf("expected 0 PromotionComplete (active unchanged), got %d", got)
	}
}

func TestPublishEnvironmentTransitions_NoGateFailedWhenAlreadyFailing(t *testing.T) {
	t.Parallel()
	pub := &capturePublisher{}
	r := &PromotionStrategyReconciler{Publisher: pub}

	failGate := &promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
		Key:   emitGateKey,
		Phase: string(promoterv1alpha1.CommitPhaseFailure),
	}
	// Gate failing in BOTH current and prior => only the transition into failure should emit,
	// so a gate that was already failing must NOT re-emit on every reconcile.
	ps := newPS(envStatus(shaA, failGate))
	prev := []promoterv1alpha1.EnvironmentStatus{envStatus(shaA, failGate)}

	r.publishEnvironmentTransitions(context.Background(), ps, prev)

	if got := len(pub.byType(notificationevents.TypeGateFailed)); got != 0 {
		t.Fatalf("expected 0 GateFailed events when gate was already failing, got %d", got)
	}
}

func TestPublishEnvironmentTransitions_NilPublisherIsNoOp(t *testing.T) {
	t.Parallel()
	r := &PromotionStrategyReconciler{Publisher: nil}
	ps := newPS(envStatus(shaB, nil))
	prev := []promoterv1alpha1.EnvironmentStatus{envStatus(shaA, nil)}
	// Must not panic.
	r.publishEnvironmentTransitions(context.Background(), ps, prev)
}
