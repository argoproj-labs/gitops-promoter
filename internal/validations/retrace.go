package validations

import (
	"context"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// Retraced is what Retrace made of a tag that is not where the controller recorded it.
type Retraced struct {
	// FoundBy is the kind of validation that took the tag to where it was found. Empty when none did: the
	// tag was found where the replay starts.
	FoundBy promoterv1alpha1.ValidatedTagSource
	// Result is where the tag should point now. Its Source and ManualMove describe a move away from where
	// the tag was found.
	Result
}

// Retrace decides where an environment's tag should point when the tag was found on a commit other than
// the one the controller recorded for it. The controller pushes a tag before it records the push, so the
// controller itself may have moved the tag, in a reconcile whose record this one does not have.
//
// in.Tag is the recorded commit and found is the commit the tag is on. Retrace replays in as Plan does,
// up to the first point at which the tag is on found. When there is one, the controller's own validations
// explain how the tag got there: Retrace replays the rest from found and reports true. When there is
// none, someone else moved the tag, and Retrace reports false.
//
// Up to that point the environment is taken to be idle, whatever in.Idle says: in.Idle describes the
// environment now that it has found to promote, not as it was when the tag moved.
func Retrace(ctx context.Context, graph Ancestry, in Input, found string) (Retraced, bool, error) {
	p := planner{graph: graph, tag: in.Tag, whenIdle: in.WhenIdle}
	steps := in.steps()
	for p.tag != found {
		if len(steps) == 0 {
			return Retraced{}, false, nil
		}
		if err := p.apply(ctx, steps[0]); err != nil {
			return Retraced{}, false, err
		}
		steps = steps[1:]
	}

	// Whatever moves the tag drops what was waiting, so nothing is waiting here. The rest is judged from
	// found, with the environment as idle as it is, and a move from here on is a new one.
	retraced := Retraced{FoundBy: p.source}
	p.source, p.manualMove = "", false
	p.busy = in.WhenIdle && !in.Idle
	if err := p.replay(ctx, steps); err != nil {
		return Retraced{}, false, err
	}
	retraced.Result = p.result(found)
	return retraced, true, nil
}
