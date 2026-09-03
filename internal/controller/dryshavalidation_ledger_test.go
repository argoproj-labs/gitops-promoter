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
	"errors"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
)

// fakeCommit is one commit on a fake environment branch, newest first in fakeHistoryReader.commits.
type fakeCommit struct {
	// historyNote and messageTrailers are the promotion trailers, from the note and the commit
	// message respectively. readPromotionTrailers prefers the note.
	historyNote     map[string][]string
	messageTrailers map[string][]string
	// commitTime is the merge commit's own time, returned by GetShaMetadataFromGit.
	commitTime time.Time
	sha        string
	// drySha is what <activePath>/hydrator.metadata reports at this commit, i.e. the dry commit this
	// commit made live. Empty means the metadata is unreadable.
	drySha string
	// noteDrySha is the hydrator git note's dry SHA, used when the metadata is unreadable.
	noteDrySha string
	// metadataErr makes the hydrator metadata read fail, exercising the fallbacks.
	metadataErr bool
}

// fakeHistoryReader implements dryShaHistoryReader over an in-memory branch.
type fakeHistoryReader struct {
	// revListErr fails the branch walk itself.
	revListErr error
	commits    []fakeCommit
}

func (f *fakeHistoryReader) find(sha string) (fakeCommit, bool) {
	for _, c := range f.commits {
		if c.sha == sha {
			return c, true
		}
	}
	return fakeCommit{}, false
}

func (f *fakeHistoryReader) GetRevListFirstParent(_ context.Context, _ string, maxCount int) ([]string, error) {
	if f.revListErr != nil {
		return nil, f.revListErr
	}
	shas := make([]string, 0, maxCount)
	for i, c := range f.commits {
		if i >= maxCount {
			break
		}
		shas = append(shas, c.sha)
	}
	return shas, nil
}

func (f *fakeHistoryReader) GetShaMetadataFromFile(_ context.Context, sha, _ string) (promoterv1alpha1.CommitShaState, error) {
	c, ok := f.find(sha)
	if !ok {
		return promoterv1alpha1.CommitShaState{}, fmt.Errorf("unknown sha %q", sha)
	}
	if c.metadataErr {
		return promoterv1alpha1.CommitShaState{}, errors.New("hydrator metadata unreadable")
	}
	return promoterv1alpha1.CommitShaState{Sha: c.drySha}, nil
}

func (f *fakeHistoryReader) GetShaMetadataFromGit(_ context.Context, sha string) (promoterv1alpha1.CommitShaState, error) {
	c, ok := f.find(sha)
	if !ok {
		return promoterv1alpha1.CommitShaState{}, fmt.Errorf("unknown sha %q", sha)
	}
	return promoterv1alpha1.CommitShaState{Sha: sha, CommitTime: metav1.NewTime(c.commitTime)}, nil
}

func (f *fakeHistoryReader) GetHistoryNote(_ context.Context, sha string) (map[string][]string, error) {
	c, _ := f.find(sha)
	return c.historyNote, nil
}

func (f *fakeHistoryReader) GetTrailers(_ context.Context, sha string) (map[string][]string, error) {
	c, _ := f.find(sha)
	return c.messageTrailers, nil
}

func (f *fakeHistoryReader) GetHydratorNote(_ context.Context, sha string) (*promoterv1alpha1.HydratorMetadata, error) {
	c, ok := f.find(sha)
	if !ok || c.noteDrySha == "" {
		return nil, nil
	}
	return &promoterv1alpha1.HydratorMetadata{DrySha: c.noteDrySha}, nil
}

// healthyTrailers builds the trailers a promotion commit carries when the dry commit that was
// active before it (outgoingDry) was healthy under the given commit status keys.
func healthyTrailers(outgoingDry string, keys ...string) map[string][]string {
	return statusTrailers(outgoingDry, "success", keys...)
}

func statusTrailers(outgoingDry, phase string, keys ...string) map[string][]string {
	trailers := map[string][]string{
		constants.TrailerShaDryActive: {outgoingDry},
	}
	for _, key := range keys {
		trailers[constants.TrailerCommitStatusActivePrefix+key+"-phase"] = []string{phase}
	}
	return trailers
}

func healthyActive(drySha string) promoterv1alpha1.CommitBranchState {
	return promoterv1alpha1.CommitBranchState{
		Dry: promoterv1alpha1.CommitShaState{Sha: drySha},
		CommitStatuses: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
			{Key: "argocd-health", Phase: "success"},
		},
	}
}

func TestBuildDryShaLedger(t *testing.T) {
	t.Parallel()

	// A branch that promoted d1, then d2, then d3 (tip). Each promotion's trailers describe the
	// health of the dry commit that was active *before* it, so d1's health rides on the commit that
	// brought in d2, and so on.
	healthyBranch := []fakeCommit{
		{sha: "c3", drySha: "d3", historyNote: healthyTrailers("d2", "argocd-health")},
		{sha: "c2", drySha: "d2", historyNote: healthyTrailers("d1", "argocd-health")},
		{sha: "c1", drySha: "d1", historyNote: healthyTrailers("d0", "argocd-health")},
	}

	tests := []struct {
		wantValidated map[string]string // dry sha -> merge sha
		name          string
		commits       []fakeCommit
		liveActive    promoterv1alpha1.CommitBranchState
		lookback      int
		wantScanned   int
		requireHealth bool
	}{
		{
			name:          "records dry commits the branch has already run and moved past",
			commits:       healthyBranch,
			lookback:      10,
			liveActive:    healthyActive("d3"),
			requireHealth: true,
			// This is the whole point of the gate: the branch's tip is d3, but d1 and d2 are still
			// recorded as validated, so a downstream promoting either of them is not blocked.
			wantValidated: map[string]string{"d1": "c1", "d2": "c2", "d3": "c3"},
			wantScanned:   3,
		},
		{
			name:          "does not credit a dry commit whose successor reported a failing status",
			commits:       []fakeCommit{{sha: "c2", drySha: "d2", historyNote: statusTrailers("d1", "failure", "argocd-health")}, {sha: "c1", drySha: "d1"}},
			lookback:      10,
			liveActive:    healthyActive("d2"),
			requireHealth: true,
			wantValidated: map[string]string{"d2": "c2"},
			wantScanned:   2,
		},
		{
			name:          "does not credit a dry commit whose successor carries no status trailers",
			commits:       []fakeCommit{{sha: "c2", drySha: "d2"}, {sha: "c1", drySha: "d1"}},
			lookback:      10,
			liveActive:    healthyActive("d2"),
			requireHealth: true,
			wantValidated: map[string]string{"d2": "c2"},
			wantScanned:   2,
		},
		{
			name: "does not credit statuses recorded against a different dry commit",
			// The successor's Sha-dry-active names d9, not d1, so its passing statuses describe some
			// other deployment and must not be read as proof that d1 was healthy.
			commits:       []fakeCommit{{sha: "c2", drySha: "d2", historyNote: healthyTrailers("d9", "argocd-health")}, {sha: "c1", drySha: "d1"}},
			lookback:      10,
			liveActive:    healthyActive("d2"),
			requireHealth: true,
			wantValidated: map[string]string{"d2": "c2"},
			wantScanned:   2,
		},
		{
			name:          "drops promotions older than the lookback window",
			commits:       healthyBranch,
			lookback:      2,
			liveActive:    healthyActive("d3"),
			requireHealth: true,
			// d1 went live at c1, which the window does not reach.
			wantValidated: map[string]string{"d2": "c2", "d3": "c3"},
			wantScanned:   2,
		},
		{
			name:          "does not credit the tip while the live status describes another dry commit",
			commits:       healthyBranch,
			lookback:      10,
			liveActive:    healthyActive("d-something-else"),
			requireHealth: true,
			wantValidated: map[string]string{"d1": "c1", "d2": "c2"},
			wantScanned:   3,
		},
		{
			name:     "does not credit the tip while it is unhealthy",
			commits:  healthyBranch,
			lookback: 10,
			liveActive: promoterv1alpha1.CommitBranchState{
				Dry:            promoterv1alpha1.CommitShaState{Sha: "d3"},
				CommitStatuses: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{{Key: "argocd-health", Phase: "pending"}},
			},
			requireHealth: true,
			wantValidated: map[string]string{"d1": "c1", "d2": "c2"},
			wantScanned:   3,
		},
		{
			name: "counts having gone live as the whole signal when the environment gates on nothing",
			// No active commit statuses are configured, so there is nothing to be healthy about and
			// every promotion in the window counts.
			commits:       []fakeCommit{{sha: "c2", drySha: "d2"}, {sha: "c1", drySha: "d1"}},
			lookback:      10,
			liveActive:    promoterv1alpha1.CommitBranchState{Dry: promoterv1alpha1.CommitShaState{Sha: "d2"}},
			requireHealth: false,
			wantValidated: map[string]string{"d1": "c1", "d2": "c2"},
			wantScanned:   2,
		},
		{
			name: "falls back to the commit message when no history note was written",
			commits: []fakeCommit{
				{sha: "c2", drySha: "d2", messageTrailers: healthyTrailers("d1", "argocd-health")},
				{sha: "c1", drySha: "d1"},
			},
			lookback:      10,
			liveActive:    healthyActive("d2"),
			requireHealth: true,
			wantValidated: map[string]string{"d1": "c1", "d2": "c2"},
			wantScanned:   2,
		},
		{
			name: "falls back to the history note's proposed dry sha when hydrator metadata is unreadable",
			commits: []fakeCommit{
				{sha: "c2", drySha: "d2", historyNote: healthyTrailers("d1", "argocd-health")},
				{sha: "c1", metadataErr: true, historyNote: map[string][]string{constants.TrailerShaDryProposed: {"d1"}}},
			},
			lookback:      10,
			liveActive:    healthyActive("d2"),
			requireHealth: true,
			wantValidated: map[string]string{"d1": "c1", "d2": "c2"},
			wantScanned:   2,
		},
		{
			name: "falls back to the hydrator note when nothing else names the dry commit",
			commits: []fakeCommit{
				{sha: "c2", drySha: "d2", historyNote: healthyTrailers("d1", "argocd-health")},
				{sha: "c1", metadataErr: true, noteDrySha: "d1"},
			},
			lookback:      10,
			liveActive:    healthyActive("d2"),
			requireHealth: true,
			wantValidated: map[string]string{"d1": "c1", "d2": "c2"},
			wantScanned:   2,
		},
		{
			name: "skips a commit whose dry commit cannot be resolved at all",
			commits: []fakeCommit{
				{sha: "c2", drySha: "d2", historyNote: healthyTrailers("d1", "argocd-health")},
				{sha: "c1", metadataErr: true},
			},
			lookback:      10,
			liveActive:    healthyActive("d2"),
			requireHealth: true,
			wantValidated: map[string]string{"d2": "c2"},
			wantScanned:   2,
		},
		{
			name:          "requires every recorded status to have passed",
			commits:       []fakeCommit{{sha: "c2", drySha: "d2", historyNote: mergeTrailers(healthyTrailers("d1", "argocd-health"), statusTrailers("d1", "failure", "smoke-test"))}, {sha: "c1", drySha: "d1"}},
			lookback:      10,
			liveActive:    healthyActive("d2"),
			requireHealth: true,
			wantValidated: map[string]string{"d2": "c2"},
			wantScanned:   2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reader := &fakeHistoryReader{commits: tt.commits}
			ledger, err := buildDryShaLedger(context.Background(), reader, "environments/dev", "environments/dev", tt.lookback, tt.liveActive, tt.requireHealth)
			if err != nil {
				t.Fatalf("buildDryShaLedger returned an unexpected error: %v", err)
			}

			if ledger.CommitsScanned != tt.wantScanned {
				t.Errorf("CommitsScanned = %d, want %d", ledger.CommitsScanned, tt.wantScanned)
			}
			if len(ledger.Validated) != len(tt.wantValidated) {
				t.Errorf("validated dry commits = %v, want %v", validatedKeys(ledger), tt.wantValidated)
			}
			for drySha, wantMergeSha := range tt.wantValidated {
				record, ok := ledger.Validated[drySha]
				if !ok {
					t.Errorf("dry commit %q missing from the ledger; got %v", drySha, validatedKeys(ledger))
					continue
				}
				if record.MergeSha != wantMergeSha {
					t.Errorf("dry commit %q recorded against merge commit %q, want %q", drySha, record.MergeSha, wantMergeSha)
				}
			}
		})
	}
}

func TestBuildDryShaLedgerPropagatesRevListFailure(t *testing.T) {
	t.Parallel()

	reader := &fakeHistoryReader{revListErr: errors.New("branch not fetched")}
	if _, err := buildDryShaLedger(context.Background(), reader, "environments/dev", "environments/dev", 10, healthyActive("d1"), true); err == nil {
		t.Fatal("expected an error when the branch walk fails, got nil")
	}
}

func TestBuildDryShaLedgerKeepsTheNewestPromotionOfARepeatedDryCommit(t *testing.T) {
	t.Parallel()

	// d1 went live, was reverted, and was promoted again. The newest promotion is the one that
	// describes the environment's current relationship with the commit.
	reader := &fakeHistoryReader{commits: []fakeCommit{
		{sha: "c4", drySha: "d2", historyNote: healthyTrailers("d1", "argocd-health")},
		{sha: "c3", drySha: "d1", historyNote: healthyTrailers("d0", "argocd-health")},
		{sha: "c2", drySha: "d0", historyNote: healthyTrailers("d1", "argocd-health")},
		{sha: "c1", drySha: "d1", historyNote: healthyTrailers("d-older", "argocd-health")},
	}}

	ledger, err := buildDryShaLedger(context.Background(), reader, "environments/dev", "environments/dev", 10, healthyActive("d2"), true)
	if err != nil {
		t.Fatalf("buildDryShaLedger returned an unexpected error: %v", err)
	}
	if got := ledger.Validated["d1"].MergeSha; got != "c3" {
		t.Errorf("d1 recorded against merge commit %q, want the newest promotion %q", got, "c3")
	}
}

func mergeTrailers(maps ...map[string][]string) map[string][]string {
	out := map[string][]string{}
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

func validatedKeys(ledger dryShaLedger) []string {
	keys := make([]string, 0, len(ledger.Validated))
	for k := range ledger.Validated {
		keys = append(keys, k)
	}
	return keys
}
