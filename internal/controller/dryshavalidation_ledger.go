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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// dryShaHistoryReader is the read-only slice of git.EnvironmentOperations the ledger needs. Taking
// an interface rather than the concrete type keeps the walk unit-testable without a real clone.
type dryShaHistoryReader interface {
	GetRevListFirstParent(ctx context.Context, revision string, maxCount int) ([]string, error)
	GetShaMetadataFromFile(ctx context.Context, sha, activePath string) (promoterv1alpha1.CommitShaState, error)
	GetShaMetadataFromGit(ctx context.Context, sha string) (promoterv1alpha1.CommitShaState, error)
	GetHistoryNote(ctx context.Context, sha string) (map[string][]string, error)
	GetTrailers(ctx context.Context, sha string) (map[string][]string, error)
	GetHydratorNote(ctx context.Context, sha string) (*promoterv1alpha1.HydratorMetadata, error)
}

// validatedDryShaRecord is one entry in a dryShaLedger: the dry commit was made active on the
// environment's active branch by MergeSha, and was observed healthy there.
type validatedDryShaRecord struct {
	// DryCommitTime is the dry commit's own time, as recorded in the hydrator metadata.
	DryCommitTime metav1.Time

	// MergeSha is the active-branch commit that made the dry commit live. The merge commit's own
	// time is resolved lazily, only for the record that ends up satisfying a gate.
	MergeSha string
}

// dryShaLedger is the answer to "which dry commits has this environment already run successfully?",
// reconstructed from the environment's active branch. It is bounded by the lookback window: a dry
// commit that went live longer ago than that is absent, which reads as "not validated".
type dryShaLedger struct {
	// Validated maps a dry commit SHA to the promotion that validated it.
	Validated map[string]validatedDryShaRecord

	// CommitsScanned is how many first-parent commits the walk actually covered.
	CommitsScanned int
}

// buildDryShaLedger walks an environment's active branch newest-first and records the dry commits
// that both went live there and were observed healthy.
//
// Health is not recorded on the commit that introduced a dry commit. The promoter snapshots the
// *outgoing* active commit statuses when it updates a promotion pull request, so the health of the
// dry commit introduced by commit C is carried by the trailers of the commit merged after C:
// that commit's Sha-dry-active names the dry commit those statuses describe. Walking newest-first,
// the trailers of the previous iteration are exactly the ones that judge the current commit; the
// branch tip has no successor yet, so its health comes from the live PromotionStrategy status.
//
// Trailers are read from the promotion-history git note first and fall back to the commit message,
// mirroring buildHistoryEntry: a squash merge or an SCM-side merge rewrites the message, but the
// note survives.
//
// requireHealth is false for an environment that configures no active commit statuses at all. There
// is then nothing for the environment to be healthy about, so having gone live is the whole signal —
// the same allowance the previous-environment gate makes.
//
// The walk is best effort per commit: a commit whose dry SHA cannot be resolved is skipped rather
// than failing the whole ledger, because an unreadable commit deep in history should not take out
// the gate. Missing evidence always reads as "not validated"; the gate fails closed.
func buildDryShaLedger(
	ctx context.Context,
	reader dryShaHistoryReader,
	activeBranch, activePath string,
	lookback int,
	liveActive promoterv1alpha1.CommitBranchState,
	requireHealth bool,
) (dryShaLedger, error) {
	logger := logf.FromContext(ctx)
	ledger := dryShaLedger{Validated: map[string]validatedDryShaRecord{}}

	shas, err := reader.GetRevListFirstParent(ctx, "origin/"+activeBranch, lookback)
	if err != nil {
		return ledger, fmt.Errorf("failed to list commits on active branch %q: %w", activeBranch, err)
	}
	ledger.CommitsScanned = len(shas)

	// Trailers of the commit merged immediately after the one being judged. Nil while judging the
	// branch tip, which has no successor.
	var successorTrailers map[string][]string

	for i, sha := range shas {
		drySha, dryCommitTime := resolveActivatedDryCommit(ctx, reader, sha, activePath)

		switch {
		case drySha == "":
			logger.V(4).Info("Skipping commit with no resolvable dry commit", "branch", activeBranch, "sha", sha)
		case !requireHealth:
			recordValidatedDrySha(ledger.Validated, drySha, sha, dryCommitTime)
		case i == 0:
			// The tip's health is live: only trust it while the status still describes this commit.
			if liveActive.Dry.Sha == drySha && utils.AreCommitStatusesPassing(liveActive.CommitStatuses) {
				recordValidatedDrySha(ledger.Validated, drySha, sha, dryCommitTime)
			}
		default:
			// Every commit below the tip is judged by the trailers of the one merged after it.
			if activeTrailersProveHealthy(ctx, successorTrailers, drySha) {
				recordValidatedDrySha(ledger.Validated, drySha, sha, dryCommitTime)
			}
		}

		// Judging the next (older) commit needs this one's trailers. The oldest commit in the
		// window has nothing after it to judge, so don't pay for a read we won't use.
		if i < len(shas)-1 {
			successorTrailers = readPromotionTrailers(ctx, reader, sha)
		}
	}

	return ledger, nil
}

// recordValidatedDrySha keeps the newest promotion of a dry commit. The walk runs newest-first, so
// the first record wins; a dry commit can go live more than once (a revert and a re-promotion).
func recordValidatedDrySha(validated map[string]validatedDryShaRecord, drySha, mergeSha string, dryCommitTime metav1.Time) {
	if _, seen := validated[drySha]; seen {
		return
	}
	validated[drySha] = validatedDryShaRecord{MergeSha: mergeSha, DryCommitTime: dryCommitTime}
}

// resolveActivatedDryCommit answers "which dry commit did this active-branch commit make live?".
//
// The hydrator metadata committed at that revision is the ground truth. The promotion-history note
// is the first fallback: its proposed dry SHA is reconciled against the merge commit when the note
// is written, so it is correct even for externally merged pull requests. The hydrator git note is
// the last resort. Returns an empty SHA when none of them can answer.
func resolveActivatedDryCommit(ctx context.Context, reader dryShaHistoryReader, sha, activePath string) (string, metav1.Time) {
	logger := logf.FromContext(ctx)

	state, err := reader.GetShaMetadataFromFile(ctx, sha, activePath)
	if err != nil {
		logger.V(4).Info("failed to read hydrator metadata for commit", "sha", sha, "err", err)
	} else if state.Sha != "" {
		return state.Sha, state.CommitTime
	}

	if trailers := readPromotionTrailers(ctx, reader, sha); len(trailers) > 0 {
		if drySha := getFirstTrailerValue(trailers, constants.TrailerShaDryProposed); drySha != "" {
			return drySha, metav1.Time{}
		}
	}

	note, err := reader.GetHydratorNote(ctx, sha)
	if err != nil {
		logger.V(4).Info("failed to read hydrator note for commit", "sha", sha, "err", err)
	} else if note != nil && note.DrySha != "" {
		return note.DrySha, note.Date
	}

	return "", metav1.Time{}
}

// readPromotionTrailers returns a commit's promoter trailers, preferring the promotion-history git
// note (which survives an SCM-side message rewrite) and falling back to the commit message.
// Returns nil when neither is readable — the callers treat that as absent evidence.
func readPromotionTrailers(ctx context.Context, reader dryShaHistoryReader, sha string) map[string][]string {
	logger := logf.FromContext(ctx)

	trailers, err := reader.GetHistoryNote(ctx, sha)
	if err != nil {
		logger.V(4).Info("failed to read promotion history note, falling back to commit message trailers", "sha", sha, "err", err)
	}
	if len(trailers) > 0 {
		return trailers
	}

	trailers, err = reader.GetTrailers(ctx, sha)
	if err != nil {
		logger.V(4).Info("failed to read commit message trailers", "sha", sha, "err", err)
		return nil
	}
	return trailers
}

// activeTrailersProveHealthy reports whether a successor commit's trailers show drySha having been
// healthy while it was the active commit.
//
// Every check here fails closed. The Sha-dry-active guard matters most: without it, a set of
// passing statuses could be credited to a dry commit they never described.
func activeTrailersProveHealthy(ctx context.Context, successorTrailers map[string][]string, drySha string) bool {
	if len(successorTrailers) == 0 {
		return false
	}
	if getFirstTrailerValue(successorTrailers, constants.TrailerShaDryActive) != drySha {
		return false
	}

	activeKeys, _ := getCommitStatusKeysFromTrailers(ctx, successorTrailers)
	if len(activeKeys) == 0 {
		// The environment gates on active commit statuses (requireHealth), but this promotion
		// recorded none. That is absent evidence, not proof of health.
		return false
	}

	for _, key := range activeKeys {
		phase := getFirstTrailerValue(successorTrailers, constants.TrailerCommitStatusActivePrefix+key+"-phase")
		if phase != string(promoterv1alpha1.CommitPhaseSuccess) {
			return false
		}
	}
	return true
}
