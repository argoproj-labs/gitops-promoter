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
	_ "embed"
	"errors"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

//go:embed testdata/DryShaValidationCommitStatus.yaml
var testDryShaValidationCommitStatusYAML string

// TestDryShaValidationCommitStatusTestdataUnmarshals keeps the documented sample honest: it is
// embedded into docs/crd-specs.md, so a field that drifts from the API would ship as bad docs.
func TestDryShaValidationCommitStatusTestdataUnmarshals(t *testing.T) {
	t.Parallel()

	if err := unmarshalYamlStrict(testDryShaValidationCommitStatusYAML, &promoterv1alpha1.DryShaValidationCommitStatus{}); err != nil {
		t.Fatalf("testdata/DryShaValidationCommitStatus.yaml does not match the API type: %v", err)
	}
}

// dryShaEnvs builds an environment slice from alternating (branch, dependsOn) pairs so tests can
// declare a graph compactly. dependsOn is a comma-joined string, empty for a graph root.
func dryShaEnvs(pairs ...string) []promoterv1alpha1.DryShaValidationEnvironment {
	out := make([]promoterv1alpha1.DryShaValidationEnvironment, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		var dependsOn []string
		if pairs[i+1] != "" {
			dependsOn = strings.Split(pairs[i+1], ",")
		}
		out = append(out, promoterv1alpha1.DryShaValidationEnvironment{Branch: pairs[i], DependsOn: dependsOn})
	}
	return out
}

func mustGraph(t *testing.T, environments []promoterv1alpha1.DryShaValidationEnvironment) *dryShaGraph {
	t.Helper()
	graph, err := buildDryShaGraph(environments)
	if err != nil {
		t.Fatalf("buildDryShaGraph returned an unexpected error: %v", err)
	}
	return graph
}

func TestBuildDryShaGraphRejectsDuplicateBranches(t *testing.T) {
	t.Parallel()

	if _, err := buildDryShaGraph(dryShaEnvs("dev", "", "dev", "")); err == nil {
		t.Fatal("expected an error for a duplicate branch, got nil")
	}
}

func TestDryShaGraphValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantErr string
		envs    []promoterv1alpha1.DryShaValidationEnvironment
	}{
		{name: "linear chain", envs: dryShaEnvs("dev", "", "stg", "dev", "prd", "stg")},
		{name: "fan out and fan in", envs: dryShaEnvs("dev", "", "e2e", "dev", "perf", "dev", "prd", "e2e,perf")},
		{name: "unknown upstream", envs: dryShaEnvs("dev", "", "prd", "nope"), wantErr: "unknown branch"},
		{name: "cycle", envs: dryShaEnvs("a", "b", "b", "a"), wantErr: "cycle"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			graph, err := buildDryShaGraph(tt.envs)
			if err != nil {
				t.Fatalf("buildDryShaGraph returned an unexpected error: %v", err)
			}
			err = graph.validate()
			switch {
			case tt.wantErr == "":
				if err != nil {
					t.Errorf("validate returned an unexpected error: %v", err)
				}
			case err == nil:
				t.Errorf("validate returned nil, want an error containing %q", tt.wantErr)
			default:
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("validate error = %q, want it to contain %q", err, tt.wantErr)
				}
			}
		})
	}
}

func TestDryShaGraphUpstreamClosure(t *testing.T) {
	t.Parallel()

	// dev -> {e2e, perf} -> prd, so prd's lower environments are e2e, perf and, transitively, dev.
	graph := mustGraph(t, dryShaEnvs("dev", "", "e2e", "dev", "perf", "dev", "prd", "e2e,perf"))

	tests := []struct {
		branch string
		want   []string
	}{
		{branch: "dev", want: []string{}},
		{branch: "e2e", want: []string{"dev"}},
		{branch: "prd", want: []string{"e2e", "perf", "dev"}},
	}

	for _, tt := range tests {
		t.Run(tt.branch, func(t *testing.T) {
			t.Parallel()

			got := graph.upstreamClosure(tt.branch)
			if !slices.Equal(got, tt.want) {
				t.Errorf("upstreamClosure(%q) = %v, want %v", tt.branch, got, tt.want)
			}
		})
	}
}

func TestDryShaEnvironmentsDerivesAChainFromThePromotionStrategy(t *testing.T) {
	t.Parallel()

	ps := &promoterv1alpha1.PromotionStrategy{
		Spec: promoterv1alpha1.PromotionStrategySpec{
			Environments: []promoterv1alpha1.Environment{{Branch: "dev"}, {Branch: "stg"}, {Branch: "prd"}},
		},
	}
	dsvcs := &promoterv1alpha1.DryShaValidationCommitStatus{}

	got := dryShaEnvironments(dsvcs, ps)
	want := dryShaEnvs("dev", "", "stg", "dev", "prd", "stg")
	if len(got) != len(want) {
		t.Fatalf("derived %d environments, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Branch != want[i].Branch || !slices.Equal(got[i].DependsOn, want[i].DependsOn) {
			t.Errorf("environment %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// An explicit topology wins over the derived chain.
	dsvcs.Spec.Environments = dryShaEnvs("dev", "", "prd", "dev")
	if got := dryShaEnvironments(dsvcs, ps); len(got) != 2 {
		t.Errorf("explicit environments were overridden: got %+v", got)
	}
}

func TestDryShaGraphValidateEnvironmentsMatchPS(t *testing.T) {
	t.Parallel()

	ps := &promoterv1alpha1.PromotionStrategy{
		Spec: promoterv1alpha1.PromotionStrategySpec{
			Environments: []promoterv1alpha1.Environment{{Branch: "dev"}, {Branch: "prd"}},
		},
	}
	ps.Name = "example"

	if err := mustGraph(t, dryShaEnvs("dev", "", "prd", "dev")).validateEnvironmentsMatchPS("gate", ps); err != nil {
		t.Errorf("matching environments returned an error: %v", err)
	}
	if err := mustGraph(t, dryShaEnvs("dev", "")).validateEnvironmentsMatchPS("gate", ps); err == nil {
		t.Error("expected an error when a PromotionStrategy environment is missing from the graph")
	}
	if err := mustGraph(t, dryShaEnvs("dev", "", "prd", "dev", "extra", "")).validateEnvironmentsMatchPS("gate", ps); err == nil {
		t.Error("expected an error when the graph declares a branch the PromotionStrategy does not have")
	}
}

func TestGatesOnActiveCommitStatuses(t *testing.T) {
	t.Parallel()

	// This decides whether a lower environment's promotions must be proven healthy or merely to
	// have happened, so it reads configuration rather than live status: an environment whose status
	// has not been populated yet also has no commit statuses on it, and reading that as "gates on
	// nothing" would credit every promotion in the lookback window without any health check.
	tests := []struct {
		name   string
		ps     promoterv1alpha1.PromotionStrategySpec
		branch string
		want   bool
	}{
		{
			name:   "strategy-wide active commit statuses apply to every environment",
			ps:     promoterv1alpha1.PromotionStrategySpec{ActiveCommitStatuses: []promoterv1alpha1.CommitStatusSelector{{Key: "argocd-health"}}, Environments: []promoterv1alpha1.Environment{{Branch: "dev"}}},
			branch: "dev",
			want:   true,
		},
		{
			name: "an environment's own active commit statuses count",
			ps: promoterv1alpha1.PromotionStrategySpec{Environments: []promoterv1alpha1.Environment{
				{Branch: "dev", ActiveCommitStatuses: []promoterv1alpha1.CommitStatusSelector{{Key: "argocd-health"}}},
				{Branch: "prd"},
			}},
			branch: "dev",
			want:   true,
		},
		{
			name: "another environment's active commit statuses do not",
			ps: promoterv1alpha1.PromotionStrategySpec{Environments: []promoterv1alpha1.Environment{
				{Branch: "dev", ActiveCommitStatuses: []promoterv1alpha1.CommitStatusSelector{{Key: "argocd-health"}}},
				{Branch: "prd"},
			}},
			branch: "prd",
			want:   false,
		},
		{
			name:   "an environment that gates on nothing needs no health proof",
			ps:     promoterv1alpha1.PromotionStrategySpec{Environments: []promoterv1alpha1.Environment{{Branch: "dev"}}},
			branch: "dev",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cache := &dryShaLedgerCache{ps: &promoterv1alpha1.PromotionStrategy{Spec: tt.ps}}
			if got := cache.gatesOnActiveCommitStatuses(tt.branch); got != tt.want {
				t.Errorf("gatesOnActiveCommitStatuses(%q) = %v, want %v", tt.branch, got, tt.want)
			}
		})
	}
}

// fakeLedgerSource serves canned ledgers so gate evaluation can be tested without a clone.
type fakeLedgerSource struct {
	ledgers map[string]dryShaLedger
	err     error
	// consulted records which upstreams were asked, in order.
	consulted []string
}

func (f *fakeLedgerSource) get(_ context.Context, branch string) (dryShaLedger, error) {
	if f.err != nil {
		return dryShaLedger{}, f.err
	}
	f.consulted = append(f.consulted, branch)
	ledger, ok := f.ledgers[branch]
	if !ok {
		return dryShaLedger{Validated: map[string]validatedDryShaRecord{}, CommitsScanned: 10}, nil
	}
	return ledger, nil
}

func (f *fakeLedgerSource) mergeCommitTime(_ context.Context, _ validatedDryShaRecord) (*metav1.Time, error) {
	return nil, nil
}

func ledgerWith(scanned int, dryShas ...string) dryShaLedger {
	validated := map[string]validatedDryShaRecord{}
	for _, sha := range dryShas {
		validated[sha] = validatedDryShaRecord{MergeSha: "merge-" + sha}
	}
	return dryShaLedger{Validated: validated, CommitsScanned: scanned}
}

// promotingStatus is an environment with an in-flight promotion of targetDry.
func promotingStatus(branch, activeDry, targetDry string) promoterv1alpha1.EnvironmentStatus {
	return promoterv1alpha1.EnvironmentStatus{
		Branch: branch,
		Active: promoterv1alpha1.CommitBranchState{Dry: promoterv1alpha1.CommitShaState{Sha: activeDry}},
		Proposed: promoterv1alpha1.CommitBranchState{
			Dry:  promoterv1alpha1.CommitShaState{Sha: targetDry},
			Note: &promoterv1alpha1.HydratorMetadata{DrySha: targetDry},
		},
	}
}

func TestEvaluateEnvironment(t *testing.T) {
	t.Parallel()

	graph := mustGraph(t, dryShaEnvs("dev", "", "e2e", "dev", "perf", "dev", "prd", "e2e,perf"))
	r := &DryShaValidationCommitStatusReconciler{}

	t.Run("a graph root has nothing below it and always passes", func(t *testing.T) {
		t.Parallel()

		statuses := map[string]promoterv1alpha1.EnvironmentStatus{"dev": promotingStatus("dev", "d1", "d5")}
		got, err := r.evaluateEnvironment(context.Background(), graph, &fakeLedgerSource{}, statuses, "dev")
		if err != nil {
			t.Fatalf("evaluateEnvironment returned an unexpected error: %v", err)
		}
		if got.phase != promoterv1alpha1.CommitPhaseSuccess {
			t.Errorf("phase = %q, want success", got.phase)
		}
	})

	t.Run("passes on a dry commit a lower environment has already run and moved past", func(t *testing.T) {
		t.Parallel()

		// This is the starvation case the gate exists for: prd is promoting d5 while e2e has
		// already raced ahead to d7. The previous-environment and DAG gates compare against e2e's
		// *current* dry commit and would report pending here; this gate finds d5 in its history.
		statuses := map[string]promoterv1alpha1.EnvironmentStatus{
			"prd": promotingStatus("prd", "d1", "d5"),
			"e2e": promotingStatus("e2e", "d7", "d7"),
		}
		ledgers := &fakeLedgerSource{ledgers: map[string]dryShaLedger{
			"e2e": ledgerWith(10, "d5", "d6", "d7"),
		}}

		got, err := r.evaluateEnvironment(context.Background(), graph, ledgers, statuses, "prd")
		if err != nil {
			t.Fatalf("evaluateEnvironment returned an unexpected error: %v", err)
		}
		if got.phase != promoterv1alpha1.CommitPhaseSuccess {
			t.Fatalf("phase = %q, want success (description: %s)", got.phase, got.description)
		}
		if got.validatedIn != "e2e" {
			t.Errorf("validatedIn = %q, want %q", got.validatedIn, "e2e")
		}
		if got.targetDrySha != "d5" {
			t.Errorf("targetDrySha = %q, want %q", got.targetDrySha, "d5")
		}
	})

	t.Run("any lower environment satisfies the gate, transitively", func(t *testing.T) {
		t.Parallel()

		// Neither direct upstream has run d5, but dev — two edges down — has.
		statuses := map[string]promoterv1alpha1.EnvironmentStatus{"prd": promotingStatus("prd", "d1", "d5")}
		ledgers := &fakeLedgerSource{ledgers: map[string]dryShaLedger{"dev": ledgerWith(10, "d5")}}

		got, err := r.evaluateEnvironment(context.Background(), graph, ledgers, statuses, "prd")
		if err != nil {
			t.Fatalf("evaluateEnvironment returned an unexpected error: %v", err)
		}
		if got.phase != promoterv1alpha1.CommitPhaseSuccess || got.validatedIn != "dev" {
			t.Errorf("phase = %q validatedIn = %q, want success in dev", got.phase, got.validatedIn)
		}
		if want := []string{"e2e", "perf", "dev"}; !slices.Equal(ledgers.consulted, want) {
			t.Errorf("consulted %v, want %v in graph order", ledgers.consulted, want)
		}
	})

	t.Run("stays pending when no lower environment has run the dry commit", func(t *testing.T) {
		t.Parallel()

		statuses := map[string]promoterv1alpha1.EnvironmentStatus{"prd": promotingStatus("prd", "d1", "d5")}
		ledgers := &fakeLedgerSource{ledgers: map[string]dryShaLedger{"dev": ledgerWith(7, "d4")}}

		got, err := r.evaluateEnvironment(context.Background(), graph, ledgers, statuses, "prd")
		if err != nil {
			t.Fatalf("evaluateEnvironment returned an unexpected error: %v", err)
		}
		if got.phase != promoterv1alpha1.CommitPhasePending {
			t.Errorf("phase = %q, want pending", got.phase)
		}
		if got.commitsScanned != 10 {
			t.Errorf("commitsScanned = %d, want the deepest window consulted (10)", got.commitsScanned)
		}
		if !strings.Contains(got.description, "d5") {
			t.Errorf("description %q does not name the dry commit being waited on", got.description)
		}
	})

	t.Run("stays pending until the hydrator names a dry commit", func(t *testing.T) {
		t.Parallel()

		statuses := map[string]promoterv1alpha1.EnvironmentStatus{
			"prd": {Branch: "prd", Active: promoterv1alpha1.CommitBranchState{Dry: promoterv1alpha1.CommitShaState{Sha: "d1"}}},
		}
		got, err := r.evaluateEnvironment(context.Background(), graph, &fakeLedgerSource{}, statuses, "prd")
		if err != nil {
			t.Fatalf("evaluateEnvironment returned an unexpected error: %v", err)
		}
		if got.phase != promoterv1alpha1.CommitPhasePending {
			t.Errorf("phase = %q, want pending", got.phase)
		}
	})

	t.Run("surfaces a ledger failure rather than reporting a gate result", func(t *testing.T) {
		t.Parallel()

		statuses := map[string]promoterv1alpha1.EnvironmentStatus{"prd": promotingStatus("prd", "d1", "d5")}
		ledgers := &fakeLedgerSource{err: errors.New("clone failed")}
		if _, err := r.evaluateEnvironment(context.Background(), graph, ledgers, statuses, "prd"); err == nil {
			t.Fatal("expected an error when a ledger cannot be built, got nil")
		}
	})
}
