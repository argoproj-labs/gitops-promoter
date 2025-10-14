package controller

import (
	"context"
	"fmt"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// updateDoraMetrics updates DORA metrics based on the current state of the ChangeTransferPolicy.
// This should be called after calculateStatus to ensure we have the latest state.
func (r *ChangeTransferPolicyReconciler) updateDoraMetrics(ctx context.Context, ctp *promoterv1alpha1.ChangeTransferPolicy) {
	logger := log.FromContext(ctx)

	// Track lead time: start timing when a new commit enters the environment
	r.trackLeadTimeStart(ctx, ctp)

	// Record deployment and lead time metrics when a change is successfully deployed
	r.recordDeploymentAndLeadTime(ctx, ctp)

	// Track and record failure metrics
	r.trackAndRecordFailures(ctx, ctp)

	// Track and record MTTR when recovering from failures
	r.trackAndRecordMTTR(ctx, ctp)

	logger.V(4).Info("Updated DORA metrics", "changeTransferPolicy", ctp.Name)
}

// trackLeadTimeStart initializes lead time tracking when a new commit is being promoted.
func (r *ChangeTransferPolicyReconciler) trackLeadTimeStart(ctx context.Context, ctp *promoterv1alpha1.ChangeTransferPolicy) {
	logger := log.FromContext(ctx)

	// If there's a proposed dry SHA that's different from the active one, and we're not already tracking it
	if ctp.Status.Proposed.Dry.Sha != "" &&
		ctp.Status.Proposed.Dry.Sha != ctp.Status.Active.Dry.Sha &&
		ctp.Status.DoraMetrics.LeadTimeStartSha != ctp.Status.Proposed.Dry.Sha {

		// Check if we're interrupting a previous incomplete release
		if ctp.Status.DoraMetrics.LeadTimeStartSha != "" &&
			ctp.Status.DoraMetrics.LeadTimeStartSha != ctp.Status.Active.Dry.Sha {
			// Log and emit event for interrupted release
			previousShortSha := ctp.Status.DoraMetrics.LeadTimeStartSha
			if len(previousShortSha) > 7 {
				previousShortSha = previousShortSha[:7]
			}
			newShortSha := ctp.Status.Proposed.Dry.Sha
			if len(newShortSha) > 7 {
				newShortSha = newShortSha[:7]
			}

			logger.Info("Incomplete release interrupted by new commit",
				"changeTransferPolicy", ctp.Name,
				"activeBranch", ctp.Spec.ActiveBranch,
				"previousSha", ctp.Status.DoraMetrics.LeadTimeStartSha,
				"newSha", ctp.Status.Proposed.Dry.Sha,
			)
			r.Recorder.Event(ctp, "Normal", "PromotionInterrupted",
				fmt.Sprintf("Environment %s: Incomplete release %s was interrupted by new commit %s",
					ctp.Spec.ActiveBranch,
					previousShortSha,
					newShortSha))
		}

		// Start tracking the new commit
		ctp.Status.DoraMetrics.LeadTimeStartSha = ctp.Status.Proposed.Dry.Sha
		ctp.Status.DoraMetrics.LeadTimeStartCommitTime = ctp.Status.Proposed.Dry.CommitTime
		logger.V(4).Info("Started lead time tracking",
			"changeTransferPolicy", ctp.Name,
			"activeBranch", ctp.Spec.ActiveBranch,
			"sha", ctp.Status.Proposed.Dry.Sha,
			"commitTime", ctp.Status.Proposed.Dry.CommitTime,
		)
	}
}

// recordDeploymentAndLeadTime records deployment and lead time metrics when a commit is successfully deployed.
func (r *ChangeTransferPolicyReconciler) recordDeploymentAndLeadTime(ctx context.Context, ctp *promoterv1alpha1.ChangeTransferPolicy) {
	logger := log.FromContext(ctx)

	// Get the promotion strategy name and namespace for metrics
	psName := ctp.Labels[promoterv1alpha1.PromotionStrategyLabel]
	if psName == "" {
		logger.V(4).Info("ChangeTransferPolicy missing promotion strategy label, skipping DORA metrics")
		return
	}

	// Check if there's a new merge to the active branch (new entry in history)
	if len(ctp.Status.History) > 0 {
		latestHistory := ctp.Status.History[0]
		activeSha := ctp.Status.Active.Dry.Sha

		// If the active SHA matches the latest history entry and we haven't recorded this deployment yet
		if latestHistory.Active.Dry.Sha == activeSha &&
			ctp.Status.DoraMetrics.LastDeployedSha != activeSha {

			// Record deployment - isTerminal will be determined by PromotionStrategy
			metrics.RecordDeployment(psName, ctp.Namespace, ctp.Spec.ActiveBranch, false)
			ctp.Status.DoraMetrics.LastDeployedSha = activeSha
			ctp.Status.DoraMetrics.DeploymentCount++

			activeShortSha := activeSha
			if len(activeShortSha) > 7 {
				activeShortSha = activeShortSha[:7]
			}

			logger.Info("Recorded deployment",
				"changeTransferPolicy", ctp.Name,
				"activeBranch", ctp.Spec.ActiveBranch,
				"sha", activeSha,
				"deploymentCount", ctp.Status.DoraMetrics.DeploymentCount,
			)
			r.Recorder.Event(ctp, "Normal", "CommitPromoted",
				fmt.Sprintf("Commit %s promoted to %s",
					activeShortSha, ctp.Spec.ActiveBranch))
		}
	}

	// Check if the active commit status is successful and we're tracking this SHA for lead time
	if ctp.Status.Active.Dry.Sha != "" &&
		ctp.Status.DoraMetrics.LeadTimeStartSha == ctp.Status.Active.Dry.Sha &&
		utils.AreCommitStatusesPassing(ctp.Status.Active.CommitStatuses) {

		// Calculate and record lead time
		if !ctp.Status.DoraMetrics.LeadTimeStartCommitTime.IsZero() {
			leadTime := time.Since(ctp.Status.DoraMetrics.LeadTimeStartCommitTime.Time)
			leadTimeSeconds := leadTime.Seconds()
			metrics.RecordLeadTime(psName, ctp.Namespace, ctp.Spec.ActiveBranch, false, leadTimeSeconds)
			ctp.Status.DoraMetrics.LastLeadTimeSeconds = &metav1.Duration{Duration: leadTime}

			activeShortSha := ctp.Status.Active.Dry.Sha
			if len(activeShortSha) > 7 {
				activeShortSha = activeShortSha[:7]
			}

			logger.Info("Recorded lead time",
				"changeTransferPolicy", ctp.Name,
				"activeBranch", ctp.Spec.ActiveBranch,
				"sha", ctp.Status.Active.Dry.Sha,
				"leadTimeSeconds", leadTimeSeconds,
			)
			r.Recorder.Event(ctp, "Normal", "LeadTimeRecorded",
				fmt.Sprintf("Lead time for %s in %s: %.2f seconds",
					activeShortSha,
					ctp.Spec.ActiveBranch,
					leadTimeSeconds))

			// Clear the tracking since we've recorded the lead time
			ctp.Status.DoraMetrics.LeadTimeStartSha = ctp.Status.Active.Dry.Sha
		}
	}
}

// trackAndRecordFailures tracks and records change failure rate metrics.
func (r *ChangeTransferPolicyReconciler) trackAndRecordFailures(ctx context.Context, ctp *promoterv1alpha1.ChangeTransferPolicy) {
	logger := log.FromContext(ctx)

	// Get the promotion strategy name for metrics
	psName := ctp.Labels[promoterv1alpha1.PromotionStrategyLabel]
	if psName == "" {
		return
	}

	// Check if the active commit status has failed
	activeSha := ctp.Status.Active.Dry.Sha
	if activeSha != "" && !utils.AreCommitStatusesPassing(ctp.Status.Active.CommitStatuses) {
		// Only record failure once per commit
		if ctp.Status.DoraMetrics.LastFailedCommitSha != activeSha {
			metrics.RecordChangeFailure(psName, ctp.Namespace, ctp.Spec.ActiveBranch, false)
			ctp.Status.DoraMetrics.LastFailedCommitSha = activeSha
			ctp.Status.DoraMetrics.FailureCount++

			activeShortSha := activeSha
			if len(activeShortSha) > 7 {
				activeShortSha = activeShortSha[:7]
			}

			logger.Info("Recorded change failure",
				"changeTransferPolicy", ctp.Name,
				"activeBranch", ctp.Spec.ActiveBranch,
				"sha", activeSha,
				"failureCount", ctp.Status.DoraMetrics.FailureCount,
			)
			r.Recorder.Event(ctp, "Warning", "ChangeFailureRecorded",
				fmt.Sprintf("Change failure in %s for commit %s",
					ctp.Spec.ActiveBranch, activeShortSha))

			// Start tracking MTTR if not already tracking
			if ctp.Status.DoraMetrics.FailureStartTime.IsZero() {
				ctp.Status.DoraMetrics.FailureStartTime = metav1.Now()
			}
		}
	}
}

// trackAndRecordMTTR tracks and records mean time to restore metrics.
func (r *ChangeTransferPolicyReconciler) trackAndRecordMTTR(ctx context.Context, ctp *promoterv1alpha1.ChangeTransferPolicy) {
	logger := log.FromContext(ctx)

	// Get the promotion strategy name for metrics
	psName := ctp.Labels[promoterv1alpha1.PromotionStrategyLabel]
	if psName == "" {
		return
	}

	// Check if we're tracking a failure and the environment has recovered
	if !ctp.Status.DoraMetrics.FailureStartTime.IsZero() &&
		utils.AreCommitStatusesPassing(ctp.Status.Active.CommitStatuses) {

		// Calculate and record MTTR
		mttr := time.Since(ctp.Status.DoraMetrics.FailureStartTime.Time)
		mttrSeconds := mttr.Seconds()
		metrics.RecordMeanTimeToRestore(psName, ctp.Namespace, ctp.Spec.ActiveBranch, false, mttrSeconds)
		ctp.Status.DoraMetrics.LastMTTRSeconds = &metav1.Duration{Duration: mttr}

		activeShortSha := ctp.Status.Active.Dry.Sha
		if len(activeShortSha) > 7 {
			activeShortSha = activeShortSha[:7]
		}

		logger.Info("Recorded MTTR",
			"changeTransferPolicy", ctp.Name,
			"activeBranch", ctp.Spec.ActiveBranch,
			"sha", ctp.Status.Active.Dry.Sha,
			"mttrSeconds", mttrSeconds,
		)
		r.Recorder.Event(ctp, "Normal", "MTTRRecorded",
			fmt.Sprintf("MTTR for %s in %s: %.2f seconds",
				activeShortSha,
				ctp.Spec.ActiveBranch,
				mttrSeconds))

		// Clear failure tracking
		ctp.Status.DoraMetrics.FailureStartTime = metav1.Time{}
	}
}
