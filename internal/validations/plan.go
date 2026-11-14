package validations

import (
	"context"
	"fmt"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// MaxSuccessfulShas is how many dry commits an environment's success memory holds.
const MaxSuccessfulShas = 10

// Ancestry answers which dry commit contains which.
type Ancestry interface {
	// Contains reports whether descendant is ancestor, or has ancestor in its history.
	Contains(ctx context.Context, descendant, ancestor string) (bool, error)
}

// Validation is one validation of a dry commit for an environment.
type Validation struct {
	// Sha is the validated dry commit.
	Sha string
	// Automatic is true for a validation the controller wrote once the upstream environments succeeded.
	// Every other validation is manual.
	Automatic bool
}

// Held is an automatic validation that has not been applied.
type Held struct {
	// Sha is the validated dry commit that is waiting.
	Sha string
	// Reason says why it is waiting.
	Reason promoterv1alpha1.ValidatedHeldReason
}

// Pending is a tag move that was decided on but could not be pushed.
type Pending struct {
	// Sha is the dry commit the tag should point at.
	Sha string
	// Source is the kind of validation that asked for the move.
	Source promoterv1alpha1.ValidatedTagSource
}

// Input is what is known about one environment's tag when deciding where it should point.
type Input struct {
	// Held is the automatic validation that was waiting after the previous decision, if any.
	Held *Held
	// Pending is the move that could not be pushed after the previous decision, if any.
	Pending *Pending
	// Tag is the commit the tag points at now, "" when the tag does not exist.
	Tag string
	// RootCandidate is the head of the dry branch when it is new to an environment that follows it, ""
	// otherwise. It counts as an automatic validation.
	RootCandidate string
	// Validations are the validations for this tag that have not been applied yet, in the order they
	// were written.
	Validations []Validation
}

// Result is where an environment's tag should point.
type Result struct {
	// Held is the automatic validation that is still waiting, if any.
	Held *Held
	// Tag is the commit the tag should point at. It equals Input.Tag when the tag should stay.
	Tag string
	// Source is the kind of validation that moves the tag. Empty when the tag stays.
	Source promoterv1alpha1.ValidatedTagSource
}

// Plan decides where an environment's tag should point.
//
// Validations are replayed in the order they were written and only the end result is returned, so the
// commits in between are skipped. An automatic validation moves the tag only to a commit that descends
// from the commit the tag is on. A commit the tag has already passed is ignored. A commit on a different
// line of history is held as Pinned, and stays held until a newer validation replaces it: which commit
// contains which never changes, so it can never apply later.
func Plan(ctx context.Context, graph Ancestry, in Input) (Result, error) {
	p := planner{graph: graph, tag: in.Tag, held: in.Held}

	// What was left over from the previous decision comes first: it is older than anything new.
	if in.Pending != nil {
		if err := p.automatic(ctx, in.Pending.Sha); err != nil {
			return Result{}, err
		}
	}
	if in.RootCandidate != "" {
		if err := p.automatic(ctx, in.RootCandidate); err != nil {
			return Result{}, err
		}
	}
	for _, validation := range in.Validations {
		if !validation.Automatic {
			continue
		}
		if err := p.automatic(ctx, validation.Sha); err != nil {
			return Result{}, err
		}
	}

	result := Result{Tag: p.tag, Held: p.held}
	if p.tag != in.Tag {
		result.Source = p.source
	}
	return result, nil
}

// planner carries one environment's tag through a replay.
type planner struct {
	graph  Ancestry
	held   *Held
	tag    string
	source promoterv1alpha1.ValidatedTagSource
}

// automatic applies an automatic validation of sha. It replaces whatever automatic validation was
// waiting: the newest one is the one that counts.
func (p *planner) automatic(ctx context.Context, sha string) error {
	p.held = nil
	if sha == p.tag {
		return nil
	}
	if p.tag != "" {
		ahead, err := p.graph.Contains(ctx, sha, p.tag)
		if err != nil {
			return fmt.Errorf("failed to check whether %s descends from %s: %w", sha, p.tag, err)
		}
		if !ahead {
			return p.hold(ctx, sha)
		}
	}
	p.tag = sha
	p.source = promoterv1alpha1.ValidatedTagSourceAutomatic
	return nil
}

// hold deals with an automatic validation of a commit that does not descend from the commit the tag is
// on. Either the tag is already past the commit, and there is nothing to wait for, or the two are on
// different lines of history, and the tag is pinned.
func (p *planner) hold(ctx context.Context, sha string) error {
	behind, err := p.graph.Contains(ctx, p.tag, sha)
	if err != nil {
		return fmt.Errorf("failed to check whether %s descends from %s: %w", p.tag, sha, err)
	}
	if !behind {
		p.held = &Held{Sha: sha, Reason: promoterv1alpha1.ValidatedHeldReasonPinned}
	}
	return nil
}

// RecordSuccess returns an environment's success memory with sha as its newest entry. A commit that is
// already the newest entry keeps the time it was first seen. The memory holds MaxSuccessfulShas commits.
func RecordSuccess(successes []promoterv1alpha1.ValidatedCommitStatusSuccessfulSha, sha string, now metav1.Time) []promoterv1alpha1.ValidatedCommitStatusSuccessfulSha {
	if sha == "" || (len(successes) > 0 && successes[0].Sha == sha) {
		return successes
	}
	updated := make([]promoterv1alpha1.ValidatedCommitStatusSuccessfulSha, 0, min(len(successes)+1, MaxSuccessfulShas))
	updated = append(updated, promoterv1alpha1.ValidatedCommitStatusSuccessfulSha{Sha: sha, Time: now})
	for _, success := range successes {
		if len(updated) == MaxSuccessfulShas {
			break
		}
		if success.Sha != sha {
			updated = append(updated, success)
		}
	}
	return updated
}

// UpstreamCleared returns the newest dry commit that every upstream has succeeded on, "" when there is
// none or there are no upstreams. Upstreams need not succeed on a commit at the same moment: the commit
// counts once it is in every upstream's success memory, and it is cleared at the moment the last one
// succeeded on it.
func UpstreamCleared(upstreams ...[]promoterv1alpha1.ValidatedCommitStatusSuccessfulSha) string {
	if len(upstreams) == 0 {
		return ""
	}
	cleared := ""
	var clearedAt metav1.Time
	for _, candidate := range upstreams[0] {
		at := candidate.Time
		everywhere := true
		for _, other := range upstreams[1:] {
			i := slices.IndexFunc(other, func(success promoterv1alpha1.ValidatedCommitStatusSuccessfulSha) bool {
				return success.Sha == candidate.Sha
			})
			if i < 0 {
				everywhere = false
				break
			}
			if other[i].Time.After(at.Time) {
				at = other[i].Time
			}
		}
		if !everywhere {
			continue
		}
		// Ties go to the SHA that sorts first, so the answer does not depend on list order.
		if cleared == "" || at.After(clearedAt.Time) || (at.Equal(&clearedAt) && candidate.Sha < cleared) {
			cleared, clearedAt = candidate.Sha, at
		}
	}
	return cleared
}
