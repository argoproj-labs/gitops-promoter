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
	// Source is Manual when a manual validation is part of what asked for the move, and Automatic
	// otherwise. A manual move is made again wherever the commit is.
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
	// WhenIdle is true when an automatic validation may only move the tag while the environment is idle.
	WhenIdle bool
	// Idle is whether the environment is idle: it has finished whatever its tag last asked of it. Only
	// read with WhenIdle.
	Idle bool
}

// steps returns everything there is to replay for the tag, oldest first.
func (in Input) steps() []Validation {
	// What was left over from the previous decision comes first, oldest first: the move that was decided
	// on and could not be pushed, then the automatic validation that was waiting behind it. The waiting
	// one is looked at afresh, because the environment may have become idle, or the move it waited behind
	// may be a different one now.
	var leftover []Validation
	if in.Pending != nil {
		leftover = append(leftover, Validation{Sha: in.Pending.Sha, Automatic: in.Pending.Source != promoterv1alpha1.ValidatedTagSourceManual})
	}
	if in.Held != nil {
		leftover = append(leftover, Validation{Sha: in.Held.Sha, Automatic: true})
	}
	if in.RootCandidate != "" {
		leftover = append(leftover, Validation{Sha: in.RootCandidate, Automatic: true})
	}
	return slices.Concat(leftover, in.Validations)
}

// Result is where an environment's tag should point.
type Result struct {
	// Held is the automatic validation that is still waiting, if any.
	Held *Held
	// Tag is the commit the tag should point at. It equals Input.Tag when the tag should stay.
	Tag string
	// Source is the kind of validation that moves the tag. Empty when the tag stays.
	Source promoterv1alpha1.ValidatedTagSource
	// ManualMove is true when a manual validation moved the tag on the way to Tag, whatever kind of
	// validation moved it last. The move then does not depend on where the tag was: should it have to be
	// made again, it is made as a manual one. False when the tag stays.
	ManualMove bool
}

// Plan decides where an environment's tag should point.
//
// Validations are replayed in the order they were written and only the end result is returned, so the
// commits in between are skipped. Manual and automatic validations follow different rules:
//
//   - A manual validation moves the tag to its commit, wherever that commit is: ahead of the tag, behind
//     it, or on another line of history. It applies whether or not the environment is idle, and whatever
//     automatic validation was waiting before it is dropped.
//   - An automatic validation moves the tag only to a commit that descends from the commit the tag is on.
//     It then replaces whatever automatic validation was waiting: the newest commit the tag can move to
//     is the one that counts.
//   - An automatic validation of a commit the tag is on, or has passed, is ignored. It changes nothing,
//     and an automatic validation that is waiting goes on waiting.
//   - An automatic validation of a commit on another line of history cannot apply: which commit contains
//     which never changes. It is held as Pinned, to say so, and stays held until a newer validation takes
//     its place. It does not take the place of a validation that waits as Busy, which can still apply.
//   - With WhenIdle, an automatic validation that could move the tag is held as Busy while the environment
//     is not idle, and is looked at again the next time. After a manual validation moved the tag, the
//     environment is not idle for the rest of the replay: it has something new to do.
func Plan(ctx context.Context, graph Ancestry, in Input) (Result, error) {
	p := planner{graph: graph, tag: in.Tag, busy: in.WhenIdle && !in.Idle, whenIdle: in.WhenIdle}
	if err := p.replay(ctx, in.steps()); err != nil {
		return Result{}, err
	}
	return p.result(in.Tag), nil
}

// planner carries one environment's tag through a replay.
type planner struct {
	graph  Ancestry
	held   *Held
	tag    string
	source promoterv1alpha1.ValidatedTagSource
	// whenIdle is true when automatic validations wait for the environment to be idle.
	whenIdle bool
	// busy is true when the environment is not idle.
	busy bool
	// manualMove is true once a manual validation has moved the tag.
	manualMove bool
}

// replay applies validations in the order given.
func (p *planner) replay(ctx context.Context, steps []Validation) error {
	for _, validation := range steps {
		if err := p.apply(ctx, validation); err != nil {
			return err
		}
	}
	return nil
}

// apply applies one validation.
func (p *planner) apply(ctx context.Context, validation Validation) error {
	if !validation.Automatic {
		p.manual(validation.Sha)
		return nil
	}
	return p.automatic(ctx, validation.Sha)
}

// result reports where the replay has taken the tag from start.
func (p *planner) result(start string) Result {
	result := Result{Tag: p.tag, Held: p.held}
	if p.tag != start {
		result.Source, result.ManualMove = p.source, p.manualMove
	}
	return result
}

// manual applies a manual validation of sha. A person asked for this commit, so the tag goes there, and
// an automatic validation that was waiting for the tag's old position has nothing left to wait for.
func (p *planner) manual(sha string) {
	p.held = nil
	if sha == p.tag {
		return
	}
	p.tag = sha
	p.source = promoterv1alpha1.ValidatedTagSourceManual
	p.manualMove = true
	// The environment now has this commit to hydrate and promote.
	p.busy = p.whenIdle
}

// automatic applies an automatic validation of sha.
func (p *planner) automatic(ctx context.Context, sha string) error {
	if sha == p.tag {
		return nil
	}
	if p.tag != "" {
		ahead, err := p.graph.Contains(ctx, sha, p.tag)
		if err != nil {
			return fmt.Errorf("failed to check whether %s descends from %s: %w", sha, p.tag, err)
		}
		if !ahead {
			return p.notAhead(ctx, sha)
		}
	}
	// The commit is the newest the tag can move to, so it takes the place of whatever was waiting.
	if p.busy {
		p.held = &Held{Sha: sha, Reason: promoterv1alpha1.ValidatedHeldReasonBusy}
		return nil
	}
	p.held = nil
	p.tag = sha
	p.source = promoterv1alpha1.ValidatedTagSourceAutomatic
	return nil
}

// notAhead deals with an automatic validation of a commit that does not descend from the commit the tag
// is on. Either the tag has passed the commit, and the validation says nothing new, or the two are on
// different lines of history, and the tag is pinned.
func (p *planner) notAhead(ctx context.Context, sha string) error {
	behind, err := p.graph.Contains(ctx, p.tag, sha)
	if err != nil {
		return fmt.Errorf("failed to check whether %s descends from %s: %w", p.tag, sha, err)
	}
	if behind {
		return nil
	}
	if p.held != nil && p.held.Reason == promoterv1alpha1.ValidatedHeldReasonBusy {
		// The validation that is waiting moves the tag once the environment is idle. This one never can,
		// so it is no reason to give that up.
		return nil
	}
	p.held = &Held{Sha: sha, Reason: promoterv1alpha1.ValidatedHeldReasonPinned}
	return nil
}

// RecordSuccess returns an environment's success memory with sha in it. A commit the environment
// succeeds on for the first time becomes the newest entry. A commit it has succeeded on before keeps its
// place and its time: an environment that goes back to an earlier commit, or that reports the commit it
// runs again once a newer commit that changed nothing for it has a successor, has not cleared anything
// new for the environments after it. The memory holds MaxSuccessfulShas commits.
func RecordSuccess(successes []promoterv1alpha1.ValidatedCommitStatusSuccessfulSha, sha string, now metav1.Time) []promoterv1alpha1.ValidatedCommitStatusSuccessfulSha {
	remembered := slices.ContainsFunc(successes, func(success promoterv1alpha1.ValidatedCommitStatusSuccessfulSha) bool {
		return success.Sha == sha
	})
	if sha == "" || remembered {
		return successes
	}
	updated := make([]promoterv1alpha1.ValidatedCommitStatusSuccessfulSha, 0, min(len(successes)+1, MaxSuccessfulShas))
	updated = append(updated, promoterv1alpha1.ValidatedCommitStatusSuccessfulSha{Sha: sha, Time: now})
	return append(updated, successes[:min(len(successes), MaxSuccessfulShas-1)]...)
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
		// Status keeps times to the second, so two commits can be cleared at the same moment. The one
		// listed first then wins: a memory lists the newest commit first. Going by anything else, the SHA
		// for one, would let the answer flip once the times have been through status.
		if cleared == "" || at.After(clearedAt.Time) {
			cleared, clearedAt = candidate.Sha, at
		}
	}
	return cleared
}
