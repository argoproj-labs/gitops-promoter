package scms

import (
	"time"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// GetPullRequestResult holds the outcome of fetching a pull request by status.id on the SCM.
//
// Removed git inference fallback (CTP writePromotionHistoryNote, pre status.mergeCommitSha):
// When the SCM could not disambiguate merged vs closed, the ChangeTransferPolicy finalizer
// located the merge commit by walking first-parent history on the active branch via
// findMergeCommitOnActiveBranch in the CTP controller. Inputs were ctp.spec.activeBranch,
// ctp.spec.proposedBranch, ctp.spec.activePath, livePR.spec.mergeSha (proposed hydrated tip),
// and the proposed dry sha from livePR.spec.commit.message trailers
// (constants.TrailerShaDryProposed). It fetched origin/<activeBranch> and scanned up to
// mergeCommitSearchWindow (50) commits, plus one extra so findDrySha could distinguish
// "saw the whole branch" from "transition before the window".
//
// Pass 1 — parent-link exact match (newest to oldest):
//   - commit == spec.mergeSha (fast-forward), or
//   - merge commit with parents[1] == spec.mergeSha.
//
// The full window was scanned before any ancestry match so a sibling CTP's newer merge on a
// shared active branch could not shadow an exact match deeper in history.
//
// Pass 2 — ancestry fallback (only if spec.mergeSha was locally fetchable and pass 1 found nothing):
//   - merge commit whose second parent is a descendant of spec.mergeSha (external merge of a
//     proposed tip newer than the snapshot recorded on the PullRequest).
//
// Pass 3 — hydrator.metadata dry-sha transition via findDrySha (never before pass 1):
//   - read <activePath>/hydrator.metadata at each commit; find the oldest commit of the newest
//     contiguous first-parent run whose dry sha equals the PR's proposed dry sha.
//   - squash/rebase merges leave no parent link to the proposed branch, but promotion rewrites
//     the recorded dry sha on the active branch.
//   - returned "" when the tip did not carry the dry sha (closed-not-merged), or when a full
//     window of matches had a parent on the oldest commit (ambiguous).
//   - malformed metadata was treated as non-match; genuine git read errors retried finalization.
//
// That inference is intentionally removed in favor of authoritative SCM data from Get.
// Reintroduce only if a provider cannot return merge commit SHA reliably after merge; wire any
// revival through status.mergeCommitSha on the PullRequest CRD rather than re-adding silent
// git walks inside CTP finalization.
type GetPullRequestResult struct {
	MergedAt       time.Time
	State          v1alpha1.PullRequestState
	MergeCommitSHA string
	Found          bool
}
