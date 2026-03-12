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

package statusapply

import (
	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	acv1alpha1 "github.com/argoproj-labs/gitops-promoter/applyconfiguration/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	acmetav1 "k8s.io/client-go/applyconfigurations/meta/v1"
)

// NilIfEmpty returns nil if s is empty, otherwise returns a pointer to s.
// Use this when assigning optional string fields in apply configuration structs:
// a nil pointer means "omit this field from the SSA patch entirely", which avoids
// writing an empty string that would fail CRD pattern/regex validation.
func NilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// NilIfZeroTime returns nil if t is the zero Time, otherwise returns a pointer to t.
// Use this when assigning optional metav1.Time fields in apply configuration structs.
func NilIfZeroTime(t metav1.Time) *metav1.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// ConditionsToApplyConfiguration converts a []metav1.Condition slice to the apply configuration equivalent.
func ConditionsToApplyConfiguration(conditions []metav1.Condition) []*acmetav1.ConditionApplyConfiguration {
	result := make([]*acmetav1.ConditionApplyConfiguration, 0, len(conditions))
	for i := range conditions {
		c := &conditions[i]
		ac := acmetav1.Condition().
			WithType(c.Type).
			WithStatus(c.Status).
			WithObservedGeneration(c.ObservedGeneration).
			WithLastTransitionTime(c.LastTransitionTime).
			WithReason(c.Reason).
			WithMessage(c.Message)
		result = append(result, ac)
	}
	return result
}

// CommitShaStateToApply converts a CommitShaState to its apply configuration.
func CommitShaStateToApply(s v1alpha1.CommitShaState) *acv1alpha1.CommitShaStateApplyConfiguration {
	ac := acv1alpha1.CommitShaState().
		WithRepoURL(s.RepoURL).
		WithAuthor(s.Author).
		WithSubject(s.Subject).
		WithBody(s.Body)
	ac.Sha = NilIfEmpty(s.Sha)
	ac.CommitTime = NilIfZeroTime(s.CommitTime)
	for _, ref := range s.References {
		refAC := acv1alpha1.RevisionReference()
		if ref.Commit != nil {
			cm := acv1alpha1.CommitMetadata().
				WithAuthor(ref.Commit.Author).
				WithSubject(ref.Commit.Subject).
				WithBody(ref.Commit.Body).
				WithRepoURL(ref.Commit.RepoURL)
			cm.Sha = NilIfEmpty(ref.Commit.Sha)
			cm.Date = ref.Commit.Date
			refAC = refAC.WithCommit(cm)
		}
		ac = ac.WithReferences(refAC)
	}
	return ac
}

// HydratorMetadataToApply converts a HydratorMetadata to its apply configuration.
func HydratorMetadataToApply(m *v1alpha1.HydratorMetadata) *acv1alpha1.HydratorMetadataApplyConfiguration {
	if m == nil {
		return nil
	}
	ac := acv1alpha1.HydratorMetadata().
		WithRepoURL(m.RepoURL).
		WithAuthor(m.Author).
		WithSubject(m.Subject).
		WithBody(m.Body)
	ac.DrySha = NilIfEmpty(m.DrySha)
	ac.Date = NilIfZeroTime(m.Date)
	for _, ref := range m.References {
		refAC := acv1alpha1.RevisionReference()
		if ref.Commit != nil {
			cm := acv1alpha1.CommitMetadata().
				WithAuthor(ref.Commit.Author).
				WithSubject(ref.Commit.Subject).
				WithBody(ref.Commit.Body).
				WithRepoURL(ref.Commit.RepoURL)
			cm.Sha = NilIfEmpty(ref.Commit.Sha)
			cm.Date = ref.Commit.Date
			refAC = refAC.WithCommit(cm)
		}
		ac = ac.WithReferences(refAC)
	}
	return ac
}

// CommitStatusesToApply converts a slice of ChangeRequestPolicyCommitStatusPhase to apply configuration.
func CommitStatusesToApply(statuses []v1alpha1.ChangeRequestPolicyCommitStatusPhase) []*acv1alpha1.ChangeRequestPolicyCommitStatusPhaseApplyConfiguration {
	result := make([]*acv1alpha1.ChangeRequestPolicyCommitStatusPhaseApplyConfiguration, 0, len(statuses))
	for _, s := range statuses {
		if s.Phase == "" {
			continue
		}
		result = append(result, acv1alpha1.ChangeRequestPolicyCommitStatusPhase().
			WithKey(s.Key).
			WithPhase(s.Phase).
			WithUrl(s.Url).
			WithDescription(s.Description))
	}
	return result
}

// CommitBranchStateToApply converts a CommitBranchState to its apply configuration.
func CommitBranchStateToApply(s v1alpha1.CommitBranchState) *acv1alpha1.CommitBranchStateApplyConfiguration {
	ac := acv1alpha1.CommitBranchState().
		WithDry(CommitShaStateToApply(s.Dry)).
		WithHydrated(CommitShaStateToApply(s.Hydrated)).
		WithCommitStatuses(CommitStatusesToApply(s.CommitStatuses)...)
	if s.Note != nil {
		ac = ac.WithNote(HydratorMetadataToApply(s.Note))
	}
	return ac
}

// PullRequestCommonStatusToApply converts a PullRequestCommonStatus to its apply configuration.
func PullRequestCommonStatusToApply(pr *v1alpha1.PullRequestCommonStatus) *acv1alpha1.PullRequestCommonStatusApplyConfiguration {
	if pr == nil {
		return nil
	}
	ac := acv1alpha1.PullRequestCommonStatus().
		WithID(pr.ID).
		WithState(pr.State).
		WithUrl(pr.Url)
	ac.PRCreationTime = NilIfZeroTime(pr.PRCreationTime)
	ac.PRMergeTime = NilIfZeroTime(pr.PRMergeTime)
	ac.ExternallyMergedOrClosed = pr.ExternallyMergedOrClosed
	return ac
}

// HistoryToApply converts a History entry to its apply configuration.
func HistoryToApply(h v1alpha1.History) *acv1alpha1.HistoryApplyConfiguration {
	proposed := acv1alpha1.CommitBranchStateHistoryProposed().
		WithHydrated(CommitShaStateToApply(h.Proposed.Hydrated)).
		WithCommitStatuses(CommitStatusesToApply(h.Proposed.CommitStatuses)...)
	ac := acv1alpha1.History().
		WithProposed(proposed).
		WithActive(CommitBranchStateToApply(h.Active))
	ac.PullRequest = PullRequestCommonStatusToApply(h.PullRequest)
	return ac
}
