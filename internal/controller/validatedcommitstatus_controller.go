/*
Copyright 2026.

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
	"path"
	"reflect"
	"strings"
	"sync"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	acmetav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	acv1alpha1 "github.com/argoproj-labs/gitops-promoter/applyconfiguration/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/gitauth"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/argoproj-labs/gitops-promoter/internal/validations"
)

const (
	// vcsSoftRetry is how soon the gate tries again after a failure it reports on its Ready condition instead
	// of returning as an error (the dry repository could not be read, or a tag push was rejected).
	vcsSoftRetry = time.Minute
	// vcsNudgeBaseDelay and vcsNudgeMaxDelay bound how often the gate wakes a ChangeTransferPolicy whose
	// hydrator has not caught up to its environment tag yet.
	vcsNudgeBaseDelay = 15 * time.Second
	vcsNudgeMaxDelay  = 2 * time.Minute
	// vcsNoteSearchDepth is how many commits back on an active branch the gate looks for a qualification
	// note when it has no status to start from.
	vcsNoteSearchDepth = 200
	// vcsShortShaLength is how much of a SHA is shown in commit status descriptions.
	vcsShortShaLength = 7
	// vcsLostCursorWalk is how many commits of the validation notes the gate looks back over when it no
	// longer knows how far it had applied them.
	vcsLostCursorWalk = 500
	// vcsValidatedByLength is how much of a validator's name is shown in commit status descriptions.
	vcsValidatedByLength = 60
	// vcsHandMoveReason is the reason written with the record of a tag that was moved by hand.
	vcsHandMoveReason = "Tag moved by hand"
)

// ValidatedCommitStatusURLTemplateData is the data passed to ValidatedCommitStatus.spec.url.template.
type ValidatedCommitStatusURLTemplateData struct {
	PromotionStrategy *promoterv1alpha1.PromotionStrategy
	Environment       string
	// DependsOnQuery is DependsOn encoded as repeated env= query parameters
	// (e.g. "env=e2e&env=perf"), ready to append after "?". Empty when DependsOn is empty.
	DependsOnQuery string
	// DependsOn is the current environment's immediate upstream branches (one edge away).
	DependsOn             []string
	ValidatedCommitStatus promoterv1alpha1.ValidatedCommitStatus
}

// ---------------------------------------------------------------------------------------------------------
// Decisions.
//
// Everything in this section is a pure function of what the reconciler observed. The reconciler below only
// gathers the observations (PromotionStrategy status, tags, the dry commit graph) and carries the decisions
// out (pushes tags, writes notes, applies CommitStatuses).
// ---------------------------------------------------------------------------------------------------------

// vcsCommitGraph answers ancestry questions about dry commits.
type vcsCommitGraph interface {
	// Contains reports whether descendant is ancestor or has ancestor in its history. A commit the graph
	// does not know is contained in nothing and contains nothing.
	Contains(ctx context.Context, descendant, ancestor string) (bool, error)
	// MergeBase returns the newest commit every one of the given commits contains, or "" when there is none.
	MergeBase(ctx context.Context, shas ...string) (string, error)
}

// vcsEnvironment is what the gate knows about one environment when it decides.
type vcsEnvironment struct {
	// Qualified is the newest dry commit the environment has qualified, nil when it has qualified nothing.
	Qualified *promoterv1alpha1.ValidatedCommitStatusWatermark
	// Recorded is the tag as the gate's status last had it: where it was, what had put it there, and the
	// target a person's placement of it acknowledged. Nil when status has no record of the environment.
	Recorded *promoterv1alpha1.ValidatedCommitStatusTagStatus
	// Validation is the newest validation of a dry commit for this environment that has not been applied:
	// one written since the validation notes were last applied, or else the one whose move of the tag could
	// not be pushed. Nil when there is none.
	Validation *vcsValidation
	// Noted is what the validation notes say put the tag where it is. Only set for a tag that status has no
	// record of.
	Noted *vcsNotedTag
	// HandMoves holds, by commit, when the gate wrote into the validation notes that it had found the tag
	// moved there by hand, for the records its status does not have: the reconcile that wrote them did not
	// get to write status.
	HandMoves map[string]metav1.Time
	// Branch is the environment's active branch.
	Branch string
	// Tag is the name of the environment's tag; TagSha is the dry commit it points at, "" when it does not exist.
	Tag    string
	TagSha string
	// TagRaw is what the tag ref held when it was read: the commit, or the tag object of an annotated tag.
	// A push of the tag is leased on it, so a tag someone moved since is never overwritten.
	TagRaw string
	// TagSource says what put the tag on TagSha, and TagValidatedBy who validated that commit when a
	// validation did. Both are filled in once the tags are planned and pushed, for the gate.
	TagSource      promoterv1alpha1.ValidatedTagSource
	TagValidatedBy string
	// LiveDrySha is the dry commit whose manifests the environment is running (see vcsLiveDrySha).
	LiveDrySha string
	// DependsOn lists the environment's direct upstreams. Empty for a root.
	DependsOn []string
	// Status is the environment's status on the PromotionStrategy.
	Status promoterv1alpha1.EnvironmentStatus
	// Manual is true when a person moves the tag (validation: Manual).
	Manual bool
	// Restored is true when the live commit was put there by a RestoreActiveCommit. It is only known to be
	// right when an older commit than Qualified is live, which is the only time it is used.
	Restored bool
}

// vcsValidation is a validation of a dry commit for an environment: a person or CI said the commit may
// deploy there, with a line in the validation notes.
type vcsValidation struct {
	// Sha is the validated dry commit.
	Sha string
	// By names who or what validated it.
	By string
	// Pending is true for a validation that was applied before and whose move of the tag could not be
	// pushed. A push of the tag by hand since then takes its place.
	Pending bool
}

// vcsNotedTag is what the validation notes say put a tag where it is.
type vcsNotedTag struct {
	// At is when: when the validation was made, or when the gate saw the tag moved by hand.
	At metav1.Time
	// By names who validated the commit. Empty for a tag that was moved by hand.
	By string
	// Source is Manual for a validation and External for a tag that was moved by hand.
	Source promoterv1alpha1.ValidatedTagSource
}

// vcsTagPlan is what the gate decided about one environment's tag: what put the tag where it was found, and
// where the tag should be.
type vcsTagPlan struct {
	// Held is set while the controller has a commit to move the tag to and cannot move it there.
	Held *promoterv1alpha1.ValidatedCommitStatusHeld
	// MovedAt is when the tag came to be where it was found, as far as the gate knows.
	MovedAt *metav1.Time
	// Validation is the validation that asks for the move to To. Nil when the move is the controller's own.
	Validation *vcsValidation
	// Branch is the environment's active branch, and Tag the name of its tag.
	Branch string
	Tag    string
	// From is the commit the tag was found on, "" when the tag does not exist. To is the commit it should be
	// on: From when the tag stays, and "" when a tag that does not exist is not created yet.
	From string
	To   string
	// Source says what put the tag on From. Empty when the tag does not exist.
	Source promoterv1alpha1.ValidatedTagSource
	// MoveSource says what puts the tag on To. Only set when To differs from From.
	MoveSource promoterv1alpha1.ValidatedTagSource
	// ValidatedBy names who validated the commit the tag was found on. Only set when Source is Manual.
	ValidatedBy string
	// Acknowledged is the target a person's placement of the tag acknowledged (see
	// ValidatedCommitStatusTagStatus.AcknowledgedTarget). Empty for a tag the controller placed.
	Acknowledged string
	// MoveAcknowledged is the target the validation that moves the tag acknowledges. Only read with
	// Validation.
	MoveAcknowledged string
	// Target is the commit an automatically validated tag is headed for: the dry branch HEAD for a root, the
	// newest commit every direct upstream has qualified for any other environment. Empty while an upstream
	// has qualified nothing.
	Target string
	// MovedByHand is true when the tag was found somewhere other than where the controller recorded it, and
	// nothing the controller did explains it.
	MovedByHand bool
}

// moves reports whether the tag has to be pushed.
func (p vcsTagPlan) moves() bool {
	return p.To != p.From
}

// tagStatus returns the tag as it is after this reconcile. moved says whether the planned move was pushed.
func (p vcsTagPlan) tagStatus(moved bool, now metav1.Time) *promoterv1alpha1.ValidatedCommitStatusTagStatus {
	tag := &promoterv1alpha1.ValidatedCommitStatusTagStatus{
		Name:               p.Tag,
		Sha:                p.From,
		Source:             p.Source,
		MovedAt:            p.MovedAt,
		ValidatedBy:        p.ValidatedBy,
		AcknowledgedTarget: p.Acknowledged,
	}
	if moved {
		tag.Sha, tag.Source, tag.MovedAt = p.To, p.MoveSource, &now
		if p.Validation != nil {
			tag.ValidatedBy, tag.AcknowledgedTarget = p.Validation.By, p.MoveAcknowledged
		} else if vcsControllerPlaced(p.MoveSource) {
			tag.ValidatedBy, tag.AcknowledgedTarget = "", ""
		}
	}
	if tag.Sha == "" {
		// Nothing is known about a tag that does not exist.
		return &promoterv1alpha1.ValidatedCommitStatusTagStatus{Name: p.Tag}
	}
	return tag
}

// vcsControllerPlaced reports whether a tag with this source is where the controller decided it should be,
// as opposed to where a person put it.
func vcsControllerPlaced(source promoterv1alpha1.ValidatedTagSource) bool {
	return source == promoterv1alpha1.ValidatedTagSourceAutomatic || source == promoterv1alpha1.ValidatedTagSourceSeeded
}

// vcsGate is the ordering commit status for one environment's proposed commit.
type vcsGate struct {
	Phase       promoterv1alpha1.CommitStatusPhase
	Description string
	Upstreams   []promoterv1alpha1.ValidatedCommitStatusUpstreamStatus
}

func vcsShortSha(sha string) string {
	return utils.TruncateString(sha, vcsShortShaLength)
}

// vcsShortShaOrNone is vcsShortSha for descriptions that may have no commit to name.
func vcsShortShaOrNone(sha string) string {
	if sha == "" {
		return "none"
	}
	return vcsShortSha(sha)
}

// vcsLiveDrySha returns the dry commit whose manifests an environment is running.
//
// That is normally the active dry SHA. The exception is a no-op hydration: the hydrator processed a newer dry
// commit, found nothing to change, and recorded that in a git note on the proposed tip instead of pushing a
// commit. If the proposed tip is what the environment is running, the environment is running the newer
// commit's manifests too. sameManifests answers that; it is only asked when a note claims a newer dry commit
// and nothing is waiting to be promoted.
func vcsLiveDrySha(status promoterv1alpha1.EnvironmentStatus, sameManifests func() (bool, error)) (string, error) {
	active := status.Active.Dry.Sha
	if active != status.Proposed.Dry.Sha {
		// A promotion is pending; the note describes the proposed commit, not what is live.
		return active, nil
	}
	reported := getEffectiveHydratedDrySha(status)
	if reported == "" || reported == active {
		return active, nil
	}
	same, err := sameManifests()
	if err != nil {
		return "", fmt.Errorf("failed to compare the proposed and active manifests of %q: %w", status.Branch, err)
	}
	if same {
		return reported, nil
	}
	// The same dry commit was hydrated again with different output, so the note is about manifests the
	// environment is not running.
	return active, nil
}

// vcsQualify returns the environment's watermark after looking at what is live in it now.
//
// It returns env.Qualified itself (the same pointer) when nothing changes, a new watermark when the
// environment qualifies a newer commit or a restore pulls the watermark back, and nil when the environment
// has qualified nothing that is still on the dry branch.
func vcsQualify(ctx context.Context, graph vcsCommitGraph, head string, env vcsEnvironment, now metav1.Time) (*promoterv1alpha1.ValidatedCommitStatusWatermark, error) {
	watermark := env.Qualified
	if watermark != nil {
		onBranch, err := graph.Contains(ctx, head, watermark.DrySha)
		if err != nil {
			return nil, fmt.Errorf("failed to check whether %q's qualified commit is on the dry branch: %w", env.Branch, err)
		}
		if !onBranch {
			// The dry branch was rewritten and the commit is gone from it.
			watermark = nil
		}
	}

	live := env.LiveDrySha
	if live == "" || (watermark != nil && watermark.DrySha == live) {
		return watermark, nil
	}
	liveOnBranch, err := graph.Contains(ctx, head, live)
	if err != nil {
		return nil, fmt.Errorf("failed to check whether %q's live commit is on the dry branch: %w", env.Branch, err)
	}
	if !liveOnBranch {
		return watermark, nil
	}

	next := &promoterv1alpha1.ValidatedCommitStatusWatermark{
		QualifiedAt: now,
		DrySha:      live,
		HydratedSha: env.Status.Active.Hydrated.Sha,
	}

	if watermark != nil {
		behind, err := graph.Contains(ctx, watermark.DrySha, live)
		if err != nil {
			return nil, fmt.Errorf("failed to compare %q's live commit with its qualified commit: %w", env.Branch, err)
		}
		if behind {
			// An older commit is live. That alone revokes nothing (qualification is latched), unless the
			// commit was restored: a restore says the commits after it should not have been trusted.
			if env.Restored {
				return next, nil
			}
			return watermark, nil
		}
	}

	if !utils.AreCommitStatusesPassing(env.Status.Active.CommitStatuses) {
		return watermark, nil
	}

	if watermark != nil {
		ahead, err := graph.Contains(ctx, live, watermark.DrySha)
		if err != nil {
			return nil, fmt.Errorf("failed to compare %q's live commit with its qualified commit: %w", env.Branch, err)
		}
		if !ahead {
			// Neither commit contains the other; moving the watermark would un-qualify commits.
			return watermark, nil
		}
	}
	return next, nil
}

// vcsPlanTags decides, for every environment in order, what put its tag where it is and where the tag
// should be.
//
// A root's tag follows the dry branch HEAD. A dependent's tag follows the newest commit every direct upstream
// has qualified. A tag only moves forward on its own, except that a tag which is no longer on the dry branch
// (the branch was force-pushed) is put back on it. A validation: Manual tag is created when it is missing
// and something is live, and is never moved.
//
// A tag a person pushed stays where they put it until the controller has a newer commit to move it to than
// it had when they pushed.
func vcsPlanTags(ctx context.Context, graph vcsCommitGraph, head string, envs []vcsEnvironment, now metav1.Time) ([]vcsTagPlan, error) {
	byBranch := make(map[string]vcsEnvironment, len(envs))
	for _, env := range envs {
		byBranch[env.Branch] = env
	}

	plans := make([]vcsTagPlan, 0, len(envs))
	for _, env := range envs {
		target, err := vcsTagTarget(ctx, graph, head, env, byBranch)
		if err != nil {
			return nil, err
		}
		plan, err := vcsPlanTag(ctx, graph, head, env, target, now)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

// vcsPlanTag decides about one environment's tag. target is where an automatically validated tag is headed
// (see vcsTagTarget).
//
// What a person asked for comes first. A validation moves the tag to its commit, wherever that is, and the
// controller moves the tag nowhere on its own in the same reconcile.
func vcsPlanTag(ctx context.Context, graph vcsCommitGraph, head string, env vcsEnvironment, target string, now metav1.Time) (vcsTagPlan, error) {
	plan := vcsTagPlan{Branch: env.Branch, Tag: env.Tag, From: env.TagSha, To: env.TagSha, Target: target}
	validation := env.Validation
	if env.TagSha == "" {
		if validation != nil {
			return vcsApplyValidation(plan, env, validation, now), nil
		}
		return vcsPlanMissingTag(ctx, graph, head, env, plan)
	}
	if err := vcsTagProvenance(ctx, graph, head, env, now, &plan); err != nil {
		return vcsTagPlan{}, err
	}
	if validation != nil && validation.Pending && plan.MovedByHand {
		// The tag was pushed by hand after the validation was made. The newer word counts.
		validation = nil
	}
	if validation != nil {
		return vcsApplyValidation(plan, env, validation, now), nil
	}
	if env.Manual || target == "" {
		return plan, nil
	}
	return vcsAdvanceTag(ctx, graph, head, plan)
}

// vcsApplyValidation plans what a validation asks for: the tag on the validated commit, as placed by that
// validation. The person who validated acknowledges what the controller has for the tag now, so the tag
// stays until the controller has something newer.
func vcsApplyValidation(plan vcsTagPlan, env vcsEnvironment, validation *vcsValidation, now metav1.Time) vcsTagPlan {
	acknowledged := ""
	if !env.Manual {
		acknowledged = plan.Target
	}
	if validation.Sha != plan.From {
		plan.To, plan.MoveSource, plan.Validation, plan.MoveAcknowledged = validation.Sha, promoterv1alpha1.ValidatedTagSourceManual, validation, acknowledged
		return plan
	}
	// The tag is on the commit already: the controller put it there and did not get to record it, or a
	// person validates the commit the tag is on. Either way it is a validated tag from here on.
	plan.Source, plan.ValidatedBy, plan.Acknowledged = promoterv1alpha1.ValidatedTagSourceManual, validation.By, acknowledged
	plan.MovedByHand = false
	if plan.MovedAt == nil {
		plan.MovedAt = &now
	}
	return plan
}

// vcsTagTarget returns the commit an automatically advanced tag wants to be at: the dry branch HEAD for a
// root, the newest commit every direct upstream has qualified for a dependent, and "" for a dependent with an
// upstream that has qualified nothing.
func vcsTagTarget(ctx context.Context, graph vcsCommitGraph, head string, env vcsEnvironment, byBranch map[string]vcsEnvironment) (string, error) {
	if len(env.DependsOn) == 0 {
		return head, nil
	}
	qualified := make([]string, 0, len(env.DependsOn))
	for _, upstream := range env.DependsOn {
		watermark := byBranch[upstream].Qualified
		if watermark == nil {
			return "", nil
		}
		qualified = append(qualified, watermark.DrySha)
	}
	target, err := graph.MergeBase(ctx, qualified...)
	if err != nil {
		return "", fmt.Errorf("failed to find the newest commit every upstream of %q has qualified: %w", env.Branch, err)
	}
	return target, nil
}

// vcsPlanMissingTag decides where to create a tag that does not exist.
func vcsPlanMissingTag(ctx context.Context, graph vcsCommitGraph, head string, env vcsEnvironment, plan vcsTagPlan) (vcsTagPlan, error) {
	// A tag the gate has on record was deleted. It goes back where it was, with what was known about it:
	// creating it afresh would take an environment that a person had put somewhere off what they chose.
	if recorded := env.Recorded; recorded != nil && recorded.Sha != "" {
		known, err := graph.Contains(ctx, recorded.Sha, recorded.Sha)
		if err != nil {
			return vcsTagPlan{}, fmt.Errorf("failed to look up the commit tag %q was on: %w", env.Tag, err)
		}
		if known {
			plan.To, plan.MoveSource, plan.Acknowledged = recorded.Sha, recorded.Source, recorded.AcknowledgedTarget
			plan.ValidatedBy = recorded.ValidatedBy
			plan.MovedByHand = true
			return plan, nil
		}
	}

	to, err := vcsNewTagSha(ctx, graph, head, env, plan.Target)
	if err != nil {
		return vcsTagPlan{}, err
	}
	plan.To = to
	if to != "" {
		plan.MoveSource = promoterv1alpha1.ValidatedTagSourceSeeded
	}
	return plan, nil
}

// vcsNewTagSha returns where to create a tag the gate has never seen: at the commit that is live in the
// environment, so the first hydration from the tag changes nothing. An automatically validated tag is created
// directly at its target when that is ahead of what is live, or when nothing on the dry branch is live.
//
// It returns "" when there is nowhere to create the tag yet: nothing on the dry branch is live in the
// environment and either a person validates for it, or its upstreams have qualified nothing. Creating the tag
// at the dry branch HEAD would propose a commit nobody chose and nothing upstream has run.
func vcsNewTagSha(ctx context.Context, graph vcsCommitGraph, head string, env vcsEnvironment, target string) (string, error) {
	base := ""
	if env.LiveDrySha != "" {
		onBranch, err := graph.Contains(ctx, head, env.LiveDrySha)
		if err != nil {
			return "", fmt.Errorf("failed to check whether %q's live commit is on the dry branch: %w", env.Branch, err)
		}
		if onBranch {
			base = env.LiveDrySha
		}
	}
	if env.Manual || target == "" || target == base {
		return base, nil
	}
	if base == "" {
		return target, nil
	}
	ahead, err := graph.Contains(ctx, target, base)
	if err != nil {
		return "", fmt.Errorf("failed to compare the target of tag %q with %q's live commit: %w", env.Tag, env.Branch, err)
	}
	if ahead {
		return target, nil
	}
	return base, nil
}

// vcsTagProvenance works out what put an existing tag where it was found, and fills that into plan.
func vcsTagProvenance(ctx context.Context, graph vcsCommitGraph, head string, env vcsEnvironment, now metav1.Time, plan *vcsTagPlan) error {
	recorded := env.Recorded
	if recorded == nil || (recorded.Sha == env.TagSha && recorded.Source == "") {
		// The gate has no record of the tag: it is new to the environment, or its status was lost. The
		// validation notes may still say that a person put the tag here.
		if noted := env.Noted; noted != nil && recorded == nil {
			plan.Source, plan.ValidatedBy, plan.MovedAt = noted.Source, noted.By, &noted.At
			if !env.Manual {
				// What the controller had for the tag at the time is not known. Whatever it has now is
				// not newer than the person's decision for certain, so the tag stays until that changes.
				plan.Acknowledged = plan.Target
			}
			return nil
		}
		plan.Source = vcsUnrecordedSource(env)
		return nil
	}
	if recorded.Sha == env.TagSha {
		plan.Source, plan.MovedAt, plan.Acknowledged, plan.ValidatedBy = recorded.Source, recorded.MovedAt, recorded.AcknowledgedTarget, recorded.ValidatedBy
		return nil
	}

	// The tag is not where the gate recorded it.
	plan.MovedAt = &now
	source, own, err := vcsOwnMove(ctx, graph, head, env, plan.Target)
	if err != nil {
		return err
	}
	if own {
		plan.Source = source
		return nil
	}
	plan.Source = promoterv1alpha1.ValidatedTagSourceExternal
	plan.MovedByHand = true
	if seen, again := env.HandMoves[env.TagSha]; again {
		// The reconcile that first saw this move wrote it into the notes and did not get to write status.
		// Taking over when it saw the move keeps the one record from becoming two.
		plan.MovedAt = &seen
	}
	if !env.Manual {
		// The tag stays where the person put it until the controller has something newer than this.
		plan.Acknowledged = plan.Target
	}
	return nil
}

// vcsUnrecordedSource is what the gate takes a tag to have been placed by when it has no record of it: the
// controller for an environment it validates for, a person for an environment people validate for.
func vcsUnrecordedSource(env vcsEnvironment) promoterv1alpha1.ValidatedTagSource {
	if env.Manual {
		return promoterv1alpha1.ValidatedTagSourceExternal
	}
	return promoterv1alpha1.ValidatedTagSourceAutomatic
}

// vcsOwnMove reports whether a tag that is not where status recorded it could have been put there by the
// controller itself, and by what kind of move. The controller pushes a tag before it records the push, so the
// reconcile that moved the tag may not have got to write status.
//
// Nothing is lost by taking a person's push for the controller's own here: it is a push to a commit the
// controller would have moved the tag to or past, and the gate verifies the tag the same either way.
func vcsOwnMove(ctx context.Context, graph vcsCommitGraph, head string, env vcsEnvironment, target string) (promoterv1alpha1.ValidatedTagSource, bool, error) {
	recorded := env.Recorded
	if recorded.Sha == "" {
		// Status says the tag did not exist. The controller creates a tag in one place only.
		seed, err := vcsNewTagSha(ctx, graph, head, env, target)
		if err != nil {
			return "", false, err
		}
		return promoterv1alpha1.ValidatedTagSourceSeeded, seed == env.TagSha, nil
	}
	if env.Manual || target == "" {
		// The controller does not move this tag.
		return "", false, nil
	}

	reached, err := graph.Contains(ctx, target, env.TagSha)
	if err != nil {
		return "", false, fmt.Errorf("failed to compare tag %q with its target: %w", env.Tag, err)
	}
	if !reached {
		return "", false, nil
	}
	forward, err := graph.Contains(ctx, env.TagSha, recorded.Sha)
	if err != nil {
		return "", false, fmt.Errorf("failed to compare tag %q with where it was: %w", env.Tag, err)
	}
	if forward {
		return promoterv1alpha1.ValidatedTagSourceAutomatic, true, nil
	}
	// Not forward: the controller only goes there to put a tag back on the dry branch, and never one that a
	// person validated.
	if recorded.Source == promoterv1alpha1.ValidatedTagSourceManual {
		return "", false, nil
	}
	wasOnBranch, err := graph.Contains(ctx, head, recorded.Sha)
	if err != nil {
		return "", false, fmt.Errorf("failed to check whether tag %q was on the dry branch: %w", env.Tag, err)
	}
	return promoterv1alpha1.ValidatedTagSourceAutomatic, !wasOnBranch, nil
}

// vcsAdvanceTag decides whether the controller moves an existing, automatically validated tag to its
// target, which is known.
func vcsAdvanceTag(ctx context.Context, graph vcsCommitGraph, head string, plan vcsTagPlan) (vcsTagPlan, error) {
	onBranch, err := graph.Contains(ctx, head, plan.From)
	if err != nil {
		return vcsTagPlan{}, fmt.Errorf("failed to check whether tag %q is on the dry branch: %w", plan.Tag, err)
	}
	if !onBranch && plan.Source != promoterv1alpha1.ValidatedTagSourceManual {
		// The tag points at history the dry branch does not have: the branch was rewritten, or someone
		// pushed the tag there. "Forward" has no meaning from there, and nothing off the branch can be
		// promoted without a validation, so the tag goes back on the branch.
		plan.To, plan.MoveSource = plan.Target, promoterv1alpha1.ValidatedTagSourceAutomatic
		return plan, nil
	}

	acknowledged, err := vcsTargetAcknowledged(ctx, graph, head, plan)
	if err != nil {
		return vcsTagPlan{}, err
	}
	if acknowledged || plan.Target == plan.From {
		return plan, nil
	}
	ahead, err := graph.Contains(ctx, plan.Target, plan.From)
	if err != nil {
		return vcsTagPlan{}, fmt.Errorf("failed to compare tag %q with its target: %w", plan.Tag, err)
	}
	if ahead {
		plan.To, plan.MoveSource = plan.Target, promoterv1alpha1.ValidatedTagSourceAutomatic
		return plan, nil
	}
	behind, err := graph.Contains(ctx, plan.From, plan.Target)
	if err != nil {
		return vcsTagPlan{}, fmt.Errorf("failed to compare tag %q with its target: %w", plan.Tag, err)
	}
	if behind {
		// The tag is ahead of what is cleared. It is never moved backward; the gate holds the promotion.
		return plan, nil
	}
	plan.Held = &promoterv1alpha1.ValidatedCommitStatusHeld{
		Sha:     plan.Target,
		Reason:  promoterv1alpha1.ValidatedHeldReasonPinned,
		Message: "Waiting for the dry branch to contain " + vcsShortSha(plan.From),
	}
	return plan, nil
}

// vcsTargetAcknowledged reports whether the tag's target is nothing new to the person who placed the tag.
// They put the tag where it is when the controller had plan.Acknowledged to move it to, and the tag stays
// until the controller has something newer than that. An acknowledged commit the dry branch no longer has
// says nothing about what is new, and neither does a tag that acknowledged nothing: the controller placed
// it, or had nothing for it when a person did.
func vcsTargetAcknowledged(ctx context.Context, graph vcsCommitGraph, head string, plan vcsTagPlan) (bool, error) {
	if plan.Acknowledged == "" {
		return false, nil
	}
	stillThere, err := graph.Contains(ctx, head, plan.Acknowledged)
	if err != nil {
		return false, fmt.Errorf("failed to check whether the target tag %q acknowledged is on the dry branch: %w", plan.Tag, err)
	}
	if !stillThere {
		return false, nil
	}
	nothingNew, err := graph.Contains(ctx, plan.Acknowledged, plan.Target)
	if err != nil {
		return false, fmt.Errorf("failed to compare the target of tag %q with the one it acknowledged: %w", plan.Tag, err)
	}
	return nothingNew, nil
}

// vcsEvaluateGate decides whether an environment's proposed commit may be promoted: the hydrator has caught up
// to the environment's tag, the tagged commit is on the dry branch, and every direct upstream has qualified
// the tagged commit or a newer one that contains it.
func vcsEvaluateGate(ctx context.Context, graph vcsCommitGraph, head, dryBranch string, env vcsEnvironment, byBranch map[string]vcsEnvironment) (vcsGate, error) {
	pending := func(description string) vcsGate {
		return vcsGate{Phase: promoterv1alpha1.CommitPhasePending, Description: description}
	}

	if env.TagSha == "" {
		return pending(fmt.Sprintf("Waiting for tag %q to be created", env.Tag)), nil
	}

	hydrated := getEffectiveHydratedDrySha(env.Status)
	if hydrated != env.TagSha {
		return pending(fmt.Sprintf("Waiting for hydrator to process tag %q at %s (currently %s)", env.Tag, vcsShortSha(env.TagSha), vcsShortShaOrNone(hydrated))), nil
	}

	if env.TagSource == promoterv1alpha1.ValidatedTagSourceManual {
		// A person validated this commit for this environment. That is enough on its own: the gate does not
		// ask where the commit is, or what the upstream environments made of it.
		by := utils.TruncateString(env.TagValidatedBy, vcsValidatedByLength)
		if by == "" {
			by = "a person"
		}
		return vcsGate{
			Phase:       promoterv1alpha1.CommitPhaseSuccess,
			Description: fmt.Sprintf("Tag %q at %s is hydrated and was validated by %s", env.Tag, vcsShortSha(env.TagSha), by),
		}, nil
	}

	onBranch, err := graph.Contains(ctx, head, env.TagSha)
	if err != nil {
		return vcsGate{}, fmt.Errorf("failed to check whether tag %q is on the dry branch: %w", env.Tag, err)
	}
	if !onBranch {
		return pending(fmt.Sprintf("Tag %q at %s is not on dry branch %q", env.Tag, vcsShortSha(env.TagSha), dryBranch)), nil
	}

	gate := vcsGate{
		Phase:       promoterv1alpha1.CommitPhaseSuccess,
		Description: fmt.Sprintf("Tag %q at %s is hydrated", env.Tag, vcsShortSha(env.TagSha)),
	}
	if len(env.DependsOn) == 0 {
		return gate, nil
	}
	gate.Description += " and qualified upstream"

	gate.Upstreams = make([]promoterv1alpha1.ValidatedCommitStatusUpstreamStatus, 0, len(env.DependsOn))
	for _, upstream := range env.DependsOn {
		newest := ""
		satisfied := false
		if watermark := byBranch[upstream].Qualified; watermark != nil {
			newest = watermark.DrySha
			satisfied, err = graph.Contains(ctx, watermark.DrySha, env.TagSha)
			if err != nil {
				return vcsGate{}, fmt.Errorf("failed to check whether %q has qualified tag %q: %w", upstream, env.Tag, err)
			}
		}
		if satisfied {
			gate.Upstreams = append(gate.Upstreams, promoterv1alpha1.ValidatedCommitStatusUpstreamStatus{Branch: upstream, Satisfied: true})
			continue
		}
		reason := fmt.Sprintf("Waiting for %q to qualify dry %s (newest qualified: %s)", upstream, vcsShortSha(env.TagSha), vcsShortShaOrNone(newest))
		gate.Upstreams = append(gate.Upstreams, promoterv1alpha1.ValidatedCommitStatusUpstreamStatus{Branch: upstream, Reason: reason})
		if gate.Phase != promoterv1alpha1.CommitPhasePending {
			gate.Phase = promoterv1alpha1.CommitPhasePending
			gate.Description = reason
		}
	}
	return gate, nil
}

// vcsManualValidation reports whether a line of the validation notes is a person's validation: not the
// record of a tag moved by hand, and not an automatic validation, which this controller never writes and
// which must not pass for a person's.
func vcsManualValidation(line validations.Line) bool {
	return line.Validates() && !line.Automatic()
}

// vcsResumeAfter finds where to take up the validations of the tag that is on the given commit, in the lines
// of the notes, which are in the order they were written. It is for a gate that no longer knows how far it
// had applied the notes.
//
// The tag resumes after the newest line of its commit: the validation that moved the tag there, or the
// record that it was moved there by hand. That line is returned as matched, and the validations written
// after it as later. Whatever was written before it has been applied, or was passed over when the tag was
// moved by hand, and is not applied again. With no line of its commit, the tag was last placed by the
// controller, which writes no line for that, and the validations in the notes are older than that move:
// nothing is returned.
func vcsResumeAfter(lines []git.ValidationEvent, tag, commit string) (matched *git.ValidationEvent, later []git.ValidationEvent) {
	for i := range lines {
		line := &lines[i]
		if line.Line.Target != tag || line.Line.Automatic() {
			continue
		}
		if commit != "" && line.Sha == commit {
			matched, later = line, nil
			continue
		}
		// The record of a move by hand to another commit moves nothing, now or ever.
		if line.Line.Validates() {
			later = append(later, *line)
		}
	}
	if matched == nil {
		return nil, nil
	}
	return matched, later
}

// vcsNoted returns what a line of the validation notes says put the tag on the line's commit.
func vcsNoted(line *git.ValidationEvent) *vcsNotedTag {
	noted := &vcsNotedTag{At: metav1.NewTime(line.At()), Source: promoterv1alpha1.ValidatedTagSourceExternal}
	if line.Line.Validates() {
		noted.Source, noted.By = promoterv1alpha1.ValidatedTagSourceManual, line.By()
	}
	return noted
}

// vcsResumesFromHead reports whether a gate that lost its place in the validation notes takes none of the
// validations in them for this tag: status still says that the tag is where a push by hand or the
// controller's own move put it. Which validations were written before that can no longer be told, and
// applying one of those would undo the move. A tag that a validation placed resumes after that validation
// (see vcsResumeAfter).
func vcsResumesFromHead(env vcsEnvironment) bool {
	recorded := env.Recorded
	return recorded != nil && recorded.Sha == env.TagSha && recorded.Source != "" && recorded.Source != promoterv1alpha1.ValidatedTagSourceManual
}

// vcsPushOutcome is what became of a push of a tag that reported an error.
type vcsPushOutcome int

const (
	// vcsPushFailed means the tag is where it was.
	vcsPushFailed vcsPushOutcome = iota
	// vcsPushLanded means the tag is where the push put it: the push went through and its answer was lost.
	vcsPushLanded
	// vcsPushOvertaken means someone else moved the tag in the meantime.
	vcsPushOvertaken
)

// vcsFailedPushOutcome says what became of a push of a tag to pushed that reported an error. before is the
// tag as it was read before the push, and after as it was read after it, with the error of that read.
func vcsFailedPushOutcome(before, after git.TagState, readErr error, pushed string) vcsPushOutcome {
	switch {
	case readErr != nil:
		return vcsPushFailed
	case after.Commit == pushed:
		return vcsPushLanded
	case after.Raw != before.Raw:
		return vcsPushOvertaken
	default:
		return vcsPushFailed
	}
}

// vcsHandMoveRecord returns the line that records, in the validation notes, that the gate found a tag on a
// commit someone had pushed it to. The line validates nothing and never moves a tag. It is there so that
// what happened to the tag can be told from git alone, by a gate that lost its status.
func vcsHandMoveRecord(gate string, tag *promoterv1alpha1.ValidatedCommitStatusTagStatus) (git.ValidationEntry, bool) {
	if tag == nil || tag.Source != promoterv1alpha1.ValidatedTagSourceExternal || tag.Sha == "" || tag.MovedAt == nil {
		return git.ValidationEntry{}, false
	}
	return git.ValidationEntry{
		Sha: tag.Sha,
		Line: validations.Line{
			Target: tag.Name,
			Source: validations.SourceExternal,
			By:     "ValidatedCommitStatus/" + gate,
			Reason: vcsHandMoveReason,
			At:     tag.MovedAt.Time,
			// One record per move: the same move, seen again by a reconcile that has no record of the
			// first, is not written twice.
			ID: tag.Name + "@" + tag.MovedAt.UTC().Format(time.RFC3339),
		},
	}, true
}

// vcsNeedsNudge reports whether the environment's ChangeTransferPolicy should be woken to look at the proposed
// branch again: its tag points at a commit the hydrator has not reported yet.
func vcsNeedsNudge(env vcsEnvironment) bool {
	return env.TagSha != "" && getEffectiveHydratedDrySha(env.Status) != env.TagSha
}

// vcsBackoff spaces out nudges for one (key, target) pair: immediately, then after base, doubling up to max.
// A new target for a key starts over.
type vcsBackoff struct {
	entries map[string]vcsBackoffEntry
	base    time.Duration
	max     time.Duration
	mu      sync.Mutex
}

type vcsBackoffEntry struct {
	nextAt time.Time
	target string
	delay  time.Duration
}

func newVCSBackoff(base, maxDelay time.Duration) *vcsBackoff {
	return &vcsBackoff{entries: map[string]vcsBackoffEntry{}, base: base, max: maxDelay}
}

// next reports whether to act now for key, and how long to wait before asking again.
func (b *vcsBackoff) next(key, target string, now time.Time) (fire bool, wait time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	entry, ok := b.entries[key]
	if !ok || entry.target != target {
		b.entries[key] = vcsBackoffEntry{target: target, delay: min(2*b.base, b.max), nextAt: now.Add(b.base)}
		return true, b.base
	}
	if now.Before(entry.nextAt) {
		return false, entry.nextAt.Sub(now)
	}
	wait = entry.delay
	entry.nextAt = now.Add(wait)
	entry.delay = min(2*wait, b.max)
	b.entries[key] = entry
	return true, wait
}

// forget drops the state for key, so the next call to next fires immediately.
func (b *vcsBackoff) forget(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.entries, key)
}

// vcsEnvironmentSetting is how the gate treats one environment, with the defaults filled in.
type vcsEnvironmentSetting struct {
	// Tag is the name of the tag the environment's hydrator reads.
	Tag string
	// Manual is true when a person moves the tag (validation: Manual).
	Manual bool
}

// vcsEnvironmentSettings returns the setting of every environment of the PromotionStrategy, by branch.
// spec.environments only lists the environments that differ from the defaults: the controller moves the tag,
// and the tag is named after the environment's branch (see validations.TagName).
func vcsEnvironmentSettings(vcs *promoterv1alpha1.ValidatedCommitStatus, ps *promoterv1alpha1.PromotionStrategy) map[string]vcsEnvironmentSetting {
	settings := make(map[string]vcsEnvironmentSetting, len(ps.Spec.Environments))
	for branch, activePath := range vcsActivePaths(ps) {
		settings[branch] = vcsEnvironmentSetting{Tag: validations.TagName(branch, activePath)}
	}
	for _, env := range vcs.Spec.Environments {
		setting, isEnvironment := settings[env.Branch]
		if !isEnvironment {
			continue
		}
		if env.Tag != "" {
			setting.Tag = env.Tag
		}
		setting.Manual = env.Validation == promoterv1alpha1.ValidationManual
		settings[env.Branch] = setting
	}
	return settings
}

// vcsValidateEnvironments checks that every branch in spec.environments is an environment of the
// PromotionStrategy, and that no two environments read the same tag. branches are the strategy's
// environments, in order.
func vcsValidateEnvironments(specEnvs []promoterv1alpha1.ValidatedCommitStatusEnvironment, branches []string, settings map[string]vcsEnvironmentSetting) error {
	known := make(map[string]struct{}, len(branches))
	for _, branch := range branches {
		known[branch] = struct{}{}
	}
	for _, env := range specEnvs {
		if _, ok := known[env.Branch]; !ok {
			return fmt.Errorf("spec.environments lists %q, which is not an environment of the PromotionStrategy", env.Branch)
		}
	}
	readBy := make(map[string]string, len(branches))
	for _, branch := range branches {
		tag := settings[branch].Tag
		if other, taken := readBy[tag]; taken {
			return fmt.Errorf("environments %q and %q both read tag %q; give one of them its own tag in spec.environments", other, branch, tag)
		}
		readBy[tag] = branch
	}
	return nil
}

// vcsNoteKey is the key of an environment's entry in a qualification note. Environments that share an active
// branch (each with its own activePath) annotate the same commits, so the path is part of the key.
func vcsNoteKey(branch, activePath string) string {
	if activePath == "" {
		return branch
	}
	return branch + ":" + activePath
}

// vcsRestored reports whether the commit that is live on branch was put there by one of the given
// RestoreActiveCommits. A RestoreActiveCommit is deleted once it no longer blocks its environment, so false
// does not mean the commit is not a restore: see liveCommitIsRestore.
func vcsRestored(racs []promoterv1alpha1.RestoreActiveCommit, branch, activeHydratedSha string) bool {
	if activeHydratedSha == "" {
		return false
	}
	for _, rac := range racs {
		if rac.Spec.Branch == branch && rac.Status.ActiveSha == activeHydratedSha {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------------------------------------
// Reconciler.
// ---------------------------------------------------------------------------------------------------------

// ValidatedCommitStatusReconciler reconciles a ValidatedCommitStatus object
type ValidatedCommitStatusReconciler struct {
	client.Client
	// APIReader reads the ValidatedCommitStatus from the API server instead of the cache. The gate's status
	// is the record of what the controller found and did about each tag, and a reconcile that starts right
	// after another one has to see what that one wrote. Optional: without it the cache is read.
	APIReader   client.Reader
	Recorder    events.EventRecorder
	Scheme      *runtime.Scheme
	SettingsMgr *settings.Manager

	// EnqueueCTP wakes a ChangeTransferPolicy so it looks at its proposed branch again. Optional.
	EnqueueCTP CTPEnqueueFunc

	// enqueueFunc requests a reconcile of a gate from outside the controller (the webhook receiver).
	enqueueFunc func(namespace, name string)

	nudges     *vcsBackoff
	nudgesOnce sync.Once
}

// gateReader returns what the ValidatedCommitStatus is read through: the API server when the reconciler
// was given a reader for it, and the cache otherwise.
func (r *ValidatedCommitStatusReconciler) gateReader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

// GetEnqueueFunc returns a function that requests a reconcile of a ValidatedCommitStatus. The
// webhook receiver uses it when the dry branch or an environment tag is pushed. It should be called after
// SetupWithManager.
func (r *ValidatedCommitStatusReconciler) GetEnqueueFunc() func(namespace, name string) {
	return r.enqueueFunc
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=validatedcommitstatuses,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=validatedcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=validatedcommitstatuses/finalizers,verbs=update
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitstatuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=restoreactivecommits,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitrepositories,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=scmproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=clusterscmproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/reconcile
//
//nolint:dupl // Ordering gate controllers share the same Reconcile skeleton by design.
func (r *ValidatedCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := logf.FromContext(ctx)
	logger.Info("Reconciling ValidatedCommitStatus")
	startTime := time.Now()

	var vcs promoterv1alpha1.ValidatedCommitStatus
	// This applies the resource status via Server-Side Apply at the end of reconciliation. Don't write status manually.
	var previousReady *metav1.Condition
	defer utils.HandleReconciliationResult(ctx, startTime, &vcs, r.Client, r.Recorder, constants.ValidatedCommitStatusControllerFieldOwner, &result, &err, &previousReady)

	// 1. Fetch the ValidatedCommitStatus instance.
	if err = r.gateReader().Get(ctx, req.NamespacedName, &vcs); err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("ValidatedCommitStatus not found")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get ValidatedCommitStatus %q: %w", req.Name, err)
	}

	// Start fresh on the Ready condition each reconcile.
	previousReady = utils.RemoveReadyCondition(&vcs)

	if err := ensureControllerInstanceIDStable(ctx, r.SettingsMgr); err != nil {
		return ctrl.Result{}, err
	}

	// 2. Fetch the referenced PromotionStrategy.
	var ps promoterv1alpha1.PromotionStrategy
	psKey := client.ObjectKey{Namespace: vcs.Namespace, Name: vcs.Spec.PromotionStrategyRef.Name}
	if err = r.Get(ctx, psKey, &ps); err != nil {
		if k8serrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("referenced PromotionStrategy %q not found: %w", vcs.Spec.PromotionStrategyRef.Name, err)
		}
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %q: %w", vcs.Spec.PromotionStrategyRef.Name, err)
	}

	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.ValidatedCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get requeue duration for ValidatedCommitStatus %q: %w", vcs.Name, err)
	}

	// 3. Only one gate moves the tags of a PromotionStrategy. Two would each take the other's moves for
	// moves made by hand.
	owner, err := r.tagOwner(ctx, &vcs, &ps)
	if err != nil {
		return ctrl.Result{}, err
	}
	if owner != vcs.Name {
		vcsSetNotReady(&vcs, promoterConditions.EnvironmentTagsOwnedByAnotherGate,
			fmt.Sprintf("ValidatedCommitStatus %q already moves the environment tags of PromotionStrategy %q; only one may", owner, ps.Name))
		return ctrl.Result{RequeueAfter: requeueDuration}, nil
	}

	// 4. Advance tags, record qualifications, and write the gate for every environment.
	retryAfter, err := r.reconcileEnvironments(ctx, &vcs, &ps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile environment tags: %w", err)
	}

	// 5. Requeue using the configured requeue duration, or sooner when something is waiting on a retry.
	if retryAfter > 0 && retryAfter < requeueDuration {
		requeueDuration = retryAfter
	}

	return ctrl.Result{RequeueAfter: requeueDuration}, nil
}

// referencedValidatedCommitStatus returns the name of the ValidatedCommitStatus the PromotionStrategy
// uses as its ordering gate, "" when its ordering gate is of another kind.
func referencedValidatedCommitStatus(ps *promoterv1alpha1.PromotionStrategy) string {
	ref := ps.Spec.OrderCommitStatusRef.WithDefaults()
	if ref.Group != promoterv1alpha1.GroupVersion.Group || ref.Kind != promoterv1alpha1.ValidatedOrderCommitStatusKind {
		return ""
	}
	return ref.Name
}

// validatedCommitStatusFor returns the one ValidatedCommitStatus that moves the environment tags of ps:
// the one the strategy references as its ordering gate, or else the oldest that references the strategy.
// It returns nil when no ValidatedCommitStatus references the strategy.
func validatedCommitStatusFor(ctx context.Context, c client.Client, ps *promoterv1alpha1.PromotionStrategy) (*promoterv1alpha1.ValidatedCommitStatus, error) {
	var gates promoterv1alpha1.ValidatedCommitStatusList
	if err := c.List(ctx, &gates, client.InNamespace(ps.Namespace), client.MatchingFields{PromotionStrategyRefField: ps.Name}); err != nil {
		return nil, fmt.Errorf("failed to list ValidatedCommitStatuses for PromotionStrategy %q: %w", ps.Name, err)
	}

	referenced := referencedValidatedCommitStatus(ps)
	var oldest *promoterv1alpha1.ValidatedCommitStatus
	for i := range gates.Items {
		gate := &gates.Items[i]
		if gate.Name == referenced {
			return gate, nil
		}
		if oldest == nil || gate.CreationTimestamp.Before(&oldest.CreationTimestamp) ||
			(gate.CreationTimestamp.Equal(&oldest.CreationTimestamp) && gate.Name < oldest.Name) {
			oldest = gate
		}
	}
	return oldest, nil
}

// tagOwner returns the name of the one ValidatedCommitStatus that moves the environment tags of ps.
func (r *ValidatedCommitStatusReconciler) tagOwner(ctx context.Context, vcs *promoterv1alpha1.ValidatedCommitStatus, ps *promoterv1alpha1.PromotionStrategy) (string, error) {
	owner, err := validatedCommitStatusFor(ctx, r.Client, ps)
	if err != nil {
		return "", err
	}
	if owner == nil {
		// The list is read from a cache that has not caught up with this gate yet.
		return vcs.Name, nil
	}
	return owner.Name, nil
}

// vcsRepositories opens the git clones one reconcile needs, at most once each. The dry repository and the
// PromotionStrategy's (hydrated) repository are usually the same GitRepository and then share a clone.
type vcsRepositories struct {
	reconciler *ValidatedCommitStatusReconciler
	vcs        *promoterv1alpha1.ValidatedCommitStatus
	ps         *promoterv1alpha1.PromotionStrategy
	opened     map[string]*git.EnvironmentOperations
	// notesFetched is true once the qualification notes of the hydrated repository were fetched.
	notesFetched bool
}

func (q *vcsRepositories) dryRepositoryName() string {
	if ref := q.vcs.Spec.DrySource.GitRepositoryRef; ref != nil && ref.Name != "" {
		return ref.Name
	}
	return q.ps.Spec.RepositoryReference.Name
}

// dry returns the clone that holds the dry branch and the environment tags.
func (q *vcsRepositories) dry(ctx context.Context) (*git.EnvironmentOperations, error) {
	return q.open(ctx, q.dryRepositoryName())
}

// hydrated returns the clone that holds the environment branches.
func (q *vcsRepositories) hydrated(ctx context.Context) (*git.EnvironmentOperations, error) {
	return q.open(ctx, q.ps.Spec.RepositoryReference.Name)
}

func (q *vcsRepositories) open(ctx context.Context, repositoryName string) (*git.EnvironmentOperations, error) {
	if ops, ok := q.opened[repositoryName]; ok {
		return ops, nil
	}
	r := q.reconciler
	repoRef := promoterv1alpha1.ObjectReference{Name: repositoryName}
	repoKey := client.ObjectKey{Namespace: q.vcs.Namespace, Name: repositoryName}

	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.Client, r.SettingsMgr, repoRef, q.vcs)
	if err != nil {
		return nil, fmt.Errorf("failed to get ScmProvider and secret for GitRepository %q: %w", repositoryName, err)
	}
	gitAuthProvider, err := gitauth.CreateGitOperationsProvider(ctx, r.Client, scmProvider, secret, repoKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create git operations provider for GitRepository %q: %w", repositoryName, err)
	}
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, r.Client, repoKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository %q: %w", repositoryName, err)
	}

	// The clone on disk is keyed by this identity. controller-runtime never reconciles one gate
	// concurrently with itself, so a per-gate identity gives each clone a single writer.
	ops := git.NewEnvironmentOperations(gitRepo, gitAuthProvider, q.vcs.Namespace+"/"+q.vcs.Name+"/"+repositoryName)
	if err := ops.CloneRepo(ctx); err != nil {
		return nil, fmt.Errorf("failed to clone GitRepository %q: %w", repositoryName, err)
	}
	if q.opened == nil {
		q.opened = map[string]*git.EnvironmentOperations{}
	}
	q.opened[repositoryName] = ops
	return ops, nil
}

// vcsGitGraph answers ancestry questions from the dry clone.
type vcsGitGraph struct {
	ops *git.EnvironmentOperations
}

// Contains reports whether descendant is ancestor or has ancestor in its history. A commit the clone does
// not have is contained in nothing and contains nothing; that is not an error, because a commit that was
// rewritten away by a force-push is exactly such a commit. Every commit the gate asks about for a good
// reason is in the clone: the dry branch and the tags are fetched before anything is decided.
func (g vcsGitGraph) Contains(ctx context.Context, descendant, ancestor string) (bool, error) {
	if !g.ops.HasCommit(ctx, descendant) || !g.ops.HasCommit(ctx, ancestor) {
		return false, nil
	}
	contains, err := g.ops.ContainsCommit(ctx, descendant, ancestor)
	if err != nil {
		return false, fmt.Errorf("failed to check whether %s contains %s: %w", descendant, ancestor, err)
	}
	return contains, nil
}

func (g vcsGitGraph) MergeBase(ctx context.Context, shas ...string) (string, error) {
	base, err := g.ops.MergeBase(ctx, shas...)
	if err != nil {
		return "", fmt.Errorf("failed to find merge base: %w", err)
	}
	return base, nil
}

// vcsDryState is what the gate read from the dry repository.
type vcsDryState struct {
	ops *git.EnvironmentOperations
	// tags maps each environment tag to where it is, a zero state when the tag does not exist.
	tags map[string]git.TagState
	// head is the HEAD of the dry branch.
	head string
	// notesTip is the tip of the validation notes, "" when there are none.
	notesTip string
}

// readDryRepository reads the dry branch HEAD and every environment tag from the remote. Tags are read on
// every reconcile: a person may move one at any time, and not every SCM tells us when they do.
func (r *ValidatedCommitStatusReconciler) readDryRepository(ctx context.Context, vcs *promoterv1alpha1.ValidatedCommitStatus, repos *vcsRepositories, tags []string) (*vcsDryState, error) {
	ops, err := repos.dry(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open the dry repository: %w", err)
	}

	refs, err := ops.SyncDryRefs(ctx, vcs.Spec.DrySource.Branch, tags)
	if err != nil {
		return nil, fmt.Errorf("failed to read dry branch %q and the environment tags: %w", vcs.Spec.DrySource.Branch, err)
	}
	return &vcsDryState{ops: ops, head: refs.Head, tags: refs.Tags, notesTip: refs.NotesTip}, nil
}

// reconcileEnvironments does the work of one reconcile. It returns how soon to reconcile again when
// something is waiting on a retry (0 when nothing is), and an error for failures that should surface as
// Ready=False with reason ReconciliationError.
func (r *ValidatedCommitStatusReconciler) reconcileEnvironments(ctx context.Context, vcs *promoterv1alpha1.ValidatedCommitStatus, ps *promoterv1alpha1.PromotionStrategy) (time.Duration, error) {
	logger := logf.FromContext(ctx)

	environments, err := resolveDependentEnvironments(ps)
	if err != nil {
		return 0, err
	}
	dependencies, err := buildDAG(environments)
	if err != nil {
		return 0, fmt.Errorf("failed to build dependency graph: %w", err)
	}
	if err := dependencies.validateDAG(); err != nil {
		return 0, fmt.Errorf("invalid dependency graph: %w", err)
	}
	environmentSettings := vcsEnvironmentSettings(vcs, ps)
	if err := vcsValidateEnvironments(vcs.Spec.Environments, dependencies.branches, environmentSettings); err != nil {
		return 0, err
	}
	tags := make([]string, 0, len(dependencies.branches))
	for _, branch := range dependencies.branches {
		tags = append(tags, environmentSettings[branch].Tag)
	}

	repos := &vcsRepositories{reconciler: r, vcs: vcs, ps: ps}
	dry, err := r.readDryRepository(ctx, vcs, repos, tags)
	if err != nil {
		// Without the dry repository nothing can be decided. Leave the tags, the child CommitStatuses, and
		// the last reported status alone, and say why on the Ready condition.
		logger.Info("Dry repository is unavailable", "error", err.Error())
		vcsSetNotReady(vcs, promoterConditions.DryRepositoryUnavailable, err.Error())
		return vcsSoftRetry, nil
	}
	graph := vcsGitGraph{ops: dry.ops}
	// A gate with no status at all is new, or lost its status. It takes its place in the validation notes
	// from where the tags are.
	firstRun := vcs.Status.Dry == nil

	// Observe.
	envs, err := r.observeEnvironments(ctx, vcs, ps, dependencies, environmentSettings, dry, repos)
	if err != nil {
		return 0, err
	}

	// Qualify. A failure to persist a qualification does not stop the rest of the reconcile; it is returned
	// at the end so the status still reflects everything else.
	activePaths := vcsActivePaths(ps)
	var qualifyErrs []error
	now := metav1.Now()
	for i := range envs {
		env := &envs[i]
		qualified, err := vcsQualify(ctx, graph, dry.head, *env, now)
		if err != nil {
			return 0, err
		}
		if qualified != nil && qualified != env.Qualified {
			if err := r.recordQualification(ctx, repos, *env, activePaths[env.Branch], qualified); err != nil {
				// The note is the durable record, so do not act on an advance that was not written: keep
				// what was qualified before and try again later. A pull-back is acted on regardless.
				qualifyErrs = append(qualifyErrs, err)
				qualified, err = vcsUnrecordedQualification(ctx, graph, dry.head, *env, qualified)
				if err != nil {
					return 0, err
				}
			} else {
				logger.Info("Environment qualified a dry commit", "branch", env.Branch, "drySha", qualified.DrySha, "restored", env.Restored)
				r.Recorder.Eventf(vcs, nil, "Normal", constants.EnvironmentQualifiedReason, "QualifyingEnvironment",
					constants.EnvironmentQualifiedMessage, env.Branch, qualified.DrySha)
			}
		}
		env.Qualified = qualified
	}

	// Validate. What people asked for since the notes were last applied.
	if err := r.collectValidations(ctx, vcs, dry, graph, envs, firstRun); err != nil {
		return 0, err
	}

	// Move tags.
	vcs.Status.Dry = &promoterv1alpha1.ValidatedCommitStatusDryStatus{Sha: dry.head}
	planEnvs, err := r.withLiveDryShasFromGit(ctx, repos, envs, activePaths)
	if err != nil {
		return 0, err
	}
	plans, err := vcsPlanTags(ctx, graph, dry.head, planEnvs, now)
	if err != nil {
		return 0, err
	}
	r.reportHandMoves(ctx, vcs, envs, plans)
	pushed := r.pushTags(ctx, vcs, ps, graph, dry, plans, envs)
	// From here on status says where every tag is and what put it there, whatever else fails: the next
	// reconcile goes by it.
	vcsRecordEnvironments(vcs, envs, plans, pushed, dry.notesTip, now)
	noteErr := r.noteHandMoves(ctx, vcs, dry.ops)

	// Gate.
	commitStatuses, err := r.writeGates(ctx, vcs, ps, graph, dry.head, envs)
	if err != nil {
		return 0, err
	}
	if err := utils.CleanupOrphanedCommitStatuses(ctx, r.Client, r.Recorder, vcs, commitStatuses); err != nil {
		return 0, fmt.Errorf("failed to cleanup orphaned CommitStatus resources: %w", err)
	}
	utils.InheritNotReadyConditionFromObjects(vcs, promoterConditions.CommitStatusesNotReady, commitStatuses...)

	retryAfter := r.nudgeHydration(ctx, vcs, ps, envs)

	var pushErrs []error
	for _, result := range pushed {
		if result.Err != nil {
			pushErrs = append(pushErrs, result.Err)
		}
		if result.Overtaken {
			// Someone moved the tag while the controller was about to. Look at it again soon.
			retryAfter = vcsSooner(retryAfter, vcsNudgeBaseDelay)
		}
	}
	if len(pushErrs) > 0 {
		vcsSetNotReady(vcs, promoterConditions.TagPushFailed, errors.Join(pushErrs...).Error())
		retryAfter = vcsSooner(retryAfter, vcsSoftRetry)
	} else if noteErr != nil {
		logger.Error(noteErr, "Failed to record tags moved by hand in the validation notes")
		vcsSetNotReady(vcs, promoterConditions.ValidationNotRecorded, noteErr.Error())
		retryAfter = vcsSooner(retryAfter, vcsSoftRetry)
	}
	if len(qualifyErrs) > 0 {
		return 0, errors.Join(qualifyErrs...)
	}
	return retryAfter, nil
}

// vcsSooner returns the shorter of two waits, where 0 means "no wait requested".
func vcsSooner(a, b time.Duration) time.Duration {
	if a == 0 || (b != 0 && b < a) {
		return b
	}
	return a
}

// vcsSetNotReady sets Ready=False with the given reason. HandleReconciliationResult keeps a Ready condition
// the reconciler set itself as long as the reconcile returns no error.
func vcsSetNotReady(vcs *promoterv1alpha1.ValidatedCommitStatus, reason promoterConditions.CommonReason, message string) {
	meta.SetStatusCondition(vcs.GetConditions(), metav1.Condition{
		Type:               string(promoterConditions.Ready),
		Status:             metav1.ConditionFalse,
		Reason:             string(reason),
		Message:            message,
		ObservedGeneration: vcs.Generation,
	})
}

// vcsActivePaths returns the effective activePath of every environment of a PromotionStrategy, by branch.
func vcsActivePaths(ps *promoterv1alpha1.PromotionStrategy) map[string]string {
	paths := make(map[string]string, len(ps.Spec.Environments))
	for _, environment := range ps.Spec.Environments {
		activePath := ps.Spec.ActivePath
		if environment.ActivePath != "" {
			activePath = environment.ActivePath
		}
		paths[environment.Branch] = activePath
	}
	return paths
}

// vcsUnrecordedQualification returns the watermark to act on when attempted, the environment's new watermark,
// could not be written to its note. It errs toward holding promotions back.
//
// An advance is not acted on: the note is the durable record, so the previous watermark stands (if it is
// still on the dry branch) until the note is written. A pull-back after a restore is acted on at once:
// keeping the previous watermark would leave downstream promotions of the commits the restore took back
// open for as long as the note cannot be written.
func vcsUnrecordedQualification(ctx context.Context, graph vcsCommitGraph, head string, env vcsEnvironment, attempted *promoterv1alpha1.ValidatedCommitStatusWatermark) (*promoterv1alpha1.ValidatedCommitStatusWatermark, error) {
	previous := env.Qualified
	if previous == nil {
		return nil, nil
	}
	onBranch, err := graph.Contains(ctx, head, previous.DrySha)
	if err != nil {
		return nil, fmt.Errorf("failed to check whether %q's qualified commit is on the dry branch: %w", env.Branch, err)
	}
	if !onBranch {
		return nil, nil
	}
	if attempted == nil || attempted.DrySha == previous.DrySha {
		return previous, nil
	}
	pulledBack, err := graph.Contains(ctx, previous.DrySha, attempted.DrySha)
	if err != nil {
		return nil, fmt.Errorf("failed to compare %q's new qualified commit with its previous one: %w", env.Branch, err)
	}
	if pulledBack {
		return attempted, nil
	}
	return previous, nil
}

// observeEnvironments gathers what the decisions need for every environment, in PromotionStrategy order.
func (r *ValidatedCommitStatusReconciler) observeEnvironments(
	ctx context.Context,
	vcs *promoterv1alpha1.ValidatedCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
	dependencies *dag,
	environmentSettings map[string]vcsEnvironmentSetting,
	dry *vcsDryState,
	repos *vcsRepositories,
) ([]vcsEnvironment, error) {
	statusByBranch := make(map[string]promoterv1alpha1.EnvironmentStatus, len(ps.Status.Environments))
	for _, envStatus := range ps.Status.Environments {
		statusByBranch[envStatus.Branch] = envStatus
	}
	previousByBranch := make(map[string]promoterv1alpha1.ValidatedCommitStatusEnvironmentStatus, len(vcs.Status.Environments))
	for _, previous := range vcs.Status.Environments {
		previousByBranch[previous.Branch] = previous
	}
	activePaths := vcsActivePaths(ps)

	var racList promoterv1alpha1.RestoreActiveCommitList
	if err := r.List(ctx, &racList, client.InNamespace(vcs.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list RestoreActiveCommits: %w", err)
	}
	racs := make([]promoterv1alpha1.RestoreActiveCommit, 0, len(racList.Items))
	for _, rac := range racList.Items {
		if rac.Spec.PromotionStrategyRef.Name == ps.Name {
			racs = append(racs, rac)
		}
	}

	envs := make([]vcsEnvironment, 0, len(dependencies.branches))
	for _, branch := range dependencies.branches {
		setting := environmentSettings[branch]
		status := statusByBranch[branch]
		activePath := activePaths[branch]

		env := vcsEnvironment{
			Branch:    branch,
			Tag:       setting.Tag,
			TagSha:    dry.tags[setting.Tag].Commit,
			TagRaw:    dry.tags[setting.Tag].Raw,
			Manual:    setting.Manual,
			DependsOn: dependencies.dependsOn[branch],
			Status:    status,
			Restored:  vcsRestored(racs, branch, status.Active.Hydrated.Sha),
		}

		live, err := vcsLiveDrySha(status, func() (bool, error) {
			return r.sameManifests(ctx, repos, branch, activePath, status)
		})
		if err != nil {
			return nil, err
		}
		env.LiveDrySha = live

		if previous, ok := previousByBranch[branch]; ok {
			env.Qualified = previous.Qualified
			env.Recorded = previous.Tag
		} else {
			// Nothing in status to start from (a new gate, or status was lost): rebuild from the notes.
			// This does not wait for the PromotionStrategy to report the environment. The status written at
			// the end of this reconcile has an entry for it either way, so there is no later chance.
			env.Qualified, err = r.loadQualification(ctx, repos, branch, activePath)
			if err != nil {
				return nil, err
			}
		}

		// A restore only matters when something other than the qualified commit is live, so git is only
		// asked then.
		if !env.Restored && env.Qualified != nil && live != "" && live != env.Qualified.DrySha {
			env.Restored, err = r.liveCommitIsRestore(ctx, repos, branch, status.Active.Hydrated.Sha)
			if err != nil {
				return nil, err
			}
		}
		envs = append(envs, env)
	}
	return envs, nil
}

// withLiveDryShasFromGit returns envs with the live dry SHA filled in from git for every environment whose
// tag is missing and whose active commit the PromotionStrategy has not reported yet.
//
// Where a missing tag is created depends on what is live in the environment, and the gate cannot wait for the
// PromotionStrategy to say: on a new setup the hydrator needs the tag before it can create the proposed
// branch, and the ChangeTransferPolicy reports nothing until that branch exists. So the gate reads the active
// branch itself.
//
// The result is only for planning tags. What an environment qualifies is decided from the commit statuses the
// PromotionStrategy reports, never from this reading, so envs itself is left alone.
func (r *ValidatedCommitStatusReconciler) withLiveDryShasFromGit(ctx context.Context, repos *vcsRepositories, envs []vcsEnvironment, activePaths map[string]string) ([]vcsEnvironment, error) {
	planned := make([]vcsEnvironment, len(envs))
	copy(planned, envs)
	for i, env := range envs {
		if env.TagSha != "" || env.Status.Active.Hydrated.Sha != "" {
			continue
		}
		live, err := r.liveDryShaFromGit(ctx, repos, env.Branch, activePaths[env.Branch])
		if err != nil {
			return nil, err
		}
		if live != "" {
			planned[i].LiveDrySha = live
		}
	}
	return planned, nil
}

// vcsIsMissingRemoteRef reports whether a git fetch failed because the ref does not exist on the remote.
func vcsIsMissingRemoteRef(err error) bool {
	return err != nil && strings.Contains(err.Error(), "couldn't find remote ref")
}

// liveDryShaFromGit returns the dry SHA recorded in hydrator.metadata at the tip of an environment's active
// branch, or "" when the branch does not exist or its tip has no metadata.
func (r *ValidatedCommitStatusReconciler) liveDryShaFromGit(ctx context.Context, repos *vcsRepositories, branch, activePath string) (string, error) {
	ops, err := repos.hydrated(ctx)
	if err != nil {
		return "", err
	}
	tip, err := ops.GetBranchSha(ctx, branch, "")
	if err != nil {
		if vcsIsMissingRemoteRef(err) {
			// The environment has never been promoted to, so nothing is live in it.
			return "", nil
		}
		return "", fmt.Errorf("failed to read active branch %q: %w", branch, err)
	}
	metadata, err := ops.GetShaMetadataFromFile(ctx, tip, activePath)
	if err != nil {
		return "", fmt.Errorf("failed to read what is live on active branch %q: %w", branch, err)
	}
	return metadata.Sha, nil
}

// liveCommitIsRestore reports whether the commit that is live on an environment's active branch was written
// by a RestoreActiveCommit.
//
// It reads the Promoter-restored-from marker the restore left on the commit. The RestoreActiveCommit itself
// is not enough to go by: it is deleted once it no longer blocks the environment, which can be before this
// gate has seen it (the gate was not running, or its status was lost and rebuilt from the notes).
func (r *ValidatedCommitStatusReconciler) liveCommitIsRestore(ctx context.Context, repos *vcsRepositories, branch, activeHydratedSha string) (bool, error) {
	if activeHydratedSha == "" {
		return false, nil
	}
	ops, err := repos.hydrated(ctx)
	if err != nil {
		return false, err
	}
	if !ops.CommitExists(ctx, activeHydratedSha) {
		if err := ops.FetchBranch(ctx, branch); err != nil {
			return false, fmt.Errorf("failed to fetch active branch %q: %w", branch, err)
		}
		if !ops.CommitExists(ctx, activeHydratedSha) {
			// The branch moved on since the PromotionStrategy reported; the next reconcile sees the new state.
			logf.FromContext(ctx).V(4).Info("Active commit is not on its branch, not checking whether it is a restore", "branch", branch, "sha", activeHydratedSha)
			return false, nil
		}
	}
	restore, err := ops.CommitIsRestore(ctx, activeHydratedSha)
	if err != nil {
		return false, fmt.Errorf("failed to check whether the active commit of %q is a restore: %w", branch, err)
	}
	return restore, nil
}

// sameManifests reports whether the environment's proposed tip has the same content as what is live on its
// active branch.
func (r *ValidatedCommitStatusReconciler) sameManifests(ctx context.Context, repos *vcsRepositories, branch, activePath string, status promoterv1alpha1.EnvironmentStatus) (bool, error) {
	proposed, active := status.Proposed.Hydrated.Sha, status.Active.Hydrated.Sha
	if proposed == "" || active == "" {
		return false, nil
	}
	if proposed == active {
		return true, nil
	}
	ops, err := repos.hydrated(ctx)
	if err != nil {
		return false, err
	}

	proposedBranch := branch + "-next"
	if activePath != "" {
		proposedBranch = path.Join(proposedBranch, activePath)
	}
	for sha, from := range map[string]string{proposed: proposedBranch, active: branch} {
		if ops.CommitExists(ctx, sha) {
			continue
		}
		if err := ops.FetchBranch(ctx, from); err != nil {
			return false, fmt.Errorf("failed to fetch branch %q: %w", from, err)
		}
		if !ops.CommitExists(ctx, sha) {
			// The branch moved on since the PromotionStrategy reported; the next reconcile sees the new state.
			logf.FromContext(ctx).V(4).Info("Hydrated commit is not on its branch, not comparing manifests", "branch", from, "sha", sha)
			return false, nil
		}
	}

	proposedTree, err := ops.TreeSha(ctx, proposed, activePath)
	if err != nil {
		return false, fmt.Errorf("failed to read the proposed tree of %q: %w", branch, err)
	}
	activeTree, err := ops.TreeSha(ctx, active, activePath)
	if err != nil {
		return false, fmt.Errorf("failed to read the active tree of %q: %w", branch, err)
	}
	return proposedTree != "" && proposedTree == activeTree, nil
}

// loadQualification reads the environment's newest qualification from the notes on its active branch. It
// returns nil when there is none, which includes an active branch that does not exist yet.
func (r *ValidatedCommitStatusReconciler) loadQualification(ctx context.Context, repos *vcsRepositories, branch, activePath string) (*promoterv1alpha1.ValidatedCommitStatusWatermark, error) {
	ops, err := repos.hydrated(ctx)
	if err != nil {
		return nil, err
	}
	if err := ops.FetchBranch(ctx, branch); err != nil {
		if vcsIsMissingRemoteRef(err) {
			// The environment has never been promoted to, so it has qualified nothing.
			return nil, nil
		}
		return nil, fmt.Errorf("failed to fetch active branch %q: %w", branch, err)
	}
	if !repos.notesFetched {
		if err := ops.FetchQualifiedNotes(ctx); err != nil {
			return nil, fmt.Errorf("failed to fetch qualification notes: %w", err)
		}
		repos.notesFetched = true
	}
	entry, hydratedSha, err := ops.FindNewestQualifiedNoteEntry(ctx, "origin/"+branch, vcsNoteKey(branch, activePath), vcsNoteSearchDepth)
	if err != nil {
		return nil, fmt.Errorf("failed to read qualification notes of %q: %w", branch, err)
	}
	if entry == nil || entry.DrySha == "" {
		return nil, nil
	}
	return &promoterv1alpha1.ValidatedCommitStatusWatermark{
		QualifiedAt: entry.QualifiedAt,
		DrySha:      entry.DrySha,
		HydratedSha: hydratedSha,
	}, nil
}

// recordQualification writes the environment's qualification to a note on the active commit that was live.
func (r *ValidatedCommitStatusReconciler) recordQualification(ctx context.Context, repos *vcsRepositories, env vcsEnvironment, activePath string, watermark *promoterv1alpha1.ValidatedCommitStatusWatermark) error {
	if watermark.HydratedSha == "" {
		// Nothing to annotate. A live dry SHA without an active hydrated commit is not something a
		// ChangeTransferPolicy reports; status alone carries the watermark if it ever does.
		return nil
	}
	ops, err := repos.hydrated(ctx)
	if err != nil {
		return err
	}
	if !ops.CommitExists(ctx, watermark.HydratedSha) {
		if err := ops.FetchBranch(ctx, env.Branch); err != nil {
			return fmt.Errorf("failed to fetch active branch %q: %w", env.Branch, err)
		}
	}
	entry := git.QualifiedNoteEntry{
		QualifiedAt:    watermark.QualifiedAt,
		DrySha:         watermark.DrySha,
		CommitStatuses: make([]git.QualifiedNoteCommitStatus, 0, len(env.Status.Active.CommitStatuses)),
	}
	for _, commitStatus := range env.Status.Active.CommitStatuses {
		entry.CommitStatuses = append(entry.CommitStatuses, git.QualifiedNoteCommitStatus{Key: commitStatus.Key, Phase: commitStatus.Phase})
	}
	if err := ops.SetQualifiedNoteEntry(ctx, watermark.HydratedSha, vcsNoteKey(env.Branch, activePath), entry); err != nil {
		return fmt.Errorf("failed to record that %q qualified dry %s: %w", env.Branch, watermark.DrySha, err)
	}
	return nil
}

// reportHandMoves says, in the log and with an event, which tags were found moved by someone else.
func (r *ValidatedCommitStatusReconciler) reportHandMoves(ctx context.Context, vcs *promoterv1alpha1.ValidatedCommitStatus, envs []vcsEnvironment, plans []vcsTagPlan) {
	for i, plan := range plans {
		if !plan.MovedByHand {
			continue
		}
		from := constants.EnvironmentTagNoPreviousSha
		if recorded := envs[i].Recorded; recorded != nil && recorded.Sha != "" {
			from = recorded.Sha
		}
		to := plan.From
		if to == "" {
			to = constants.EnvironmentTagNoPreviousSha
		}
		logf.FromContext(ctx).Info("Environment tag was moved outside the controller", "branch", plan.Branch, "tag", plan.Tag, "from", from, "to", to)
		r.Recorder.Eventf(vcs, nil, "Normal", constants.EnvironmentTagMovedByHandReason, "ObservingEnvironmentTag",
			constants.EnvironmentTagMovedByHandMessage, plan.Tag, plan.Branch, from, to)
	}
}

// vcsHasProposal reports whether the environment has a proposed change for the gate to report on.
func vcsHasProposal(env vcsEnvironment) bool {
	return env.Status.Proposed.Hydrated.Sha != "" && env.Status.Active.Dry.Sha != env.Status.Proposed.Dry.Sha
}

// holdProposal sets the gate to pending on an environment's open proposal when its tag is about to leave
// the commit the proposal was hydrated from. The gate is otherwise rewritten only after the push, and until
// then says success about a proposal that the tag no longer asks for. A promotion that merges in between
// puts a commit live that the move was meant to replace.
func (r *ValidatedCommitStatusReconciler) holdProposal(
	ctx context.Context,
	vcs *promoterv1alpha1.ValidatedCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
	graph vcsCommitGraph,
	head string,
	env vcsEnvironment,
	plan vcsTagPlan,
	byBranch map[string]vcsEnvironment,
) error {
	if !vcsHasProposal(env) {
		return nil
	}
	env.TagSha, env.TagSource, env.TagValidatedBy = plan.To, plan.MoveSource, ""
	if plan.Validation != nil {
		env.TagValidatedBy = plan.Validation.By
	}
	gate, err := vcsEvaluateGate(ctx, graph, head, vcs.Spec.DrySource.Branch, env, byBranch)
	if err != nil {
		return err
	}
	if gate.Phase == promoterv1alpha1.CommitPhaseSuccess {
		return nil
	}
	if _, _, err := r.applyCommitStatus(ctx, vcs, ps, env, gate, env.Status.Proposed.Hydrated.Sha); err != nil {
		return fmt.Errorf("failed to hold the proposed change of %q before moving tag %q: %w", env.Branch, env.Tag, err)
	}
	return nil
}

// vcsPushResult is what became of one environment's planned tag move.
type vcsPushResult struct {
	// Err is why the move was not made. Nil when it was made, when there was none to make, and when someone
	// else moved the tag first.
	Err error
	// Moved is true when the tag is now where the plan wanted it.
	Moved bool
	// Overtaken is true when someone else moved the tag while the controller was about to.
	Overtaken bool
}

// pushTags carries out the planned tag moves and updates envs with the tags' new positions. It returns, by
// environment, what became of the planned move. A failed push does not stop the others.
func (r *ValidatedCommitStatusReconciler) pushTags(
	ctx context.Context,
	vcs *promoterv1alpha1.ValidatedCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
	graph vcsCommitGraph,
	dry *vcsDryState,
	plans []vcsTagPlan,
	envs []vcsEnvironment,
) []vcsPushResult {
	logger := logf.FromContext(ctx)
	byBranch := make(map[string]vcsEnvironment, len(envs))
	for _, env := range envs {
		byBranch[env.Branch] = env
	}

	results := make([]vcsPushResult, len(plans))
	for i, plan := range plans {
		if !plan.moves() {
			continue
		}
		env := &envs[i]
		failed := func(err error) {
			logger.Error(err, "Failed to push environment tag", "branch", plan.Branch, "tag", plan.Tag, "to", plan.To)
			r.Recorder.Eventf(vcs, nil, "Warning", constants.EnvironmentTagPushFailedReason, "AdvancingTag",
				constants.EnvironmentTagPushFailedMessage, plan.Tag, plan.Branch, err)
			results[i].Err = err
		}
		if err := r.holdProposal(ctx, vcs, ps, graph, dry.head, *env, plan, byBranch); err != nil {
			failed(err)
			continue
		}
		// Tags are pushed one at a time: GitHub sends no push event when more than three tags are pushed
		// at once, and one rejected tag must not hold back the others.
		if err := dry.ops.MoveTag(ctx, plan.Tag, plan.To, env.TagRaw); err != nil {
			current, readErr := dry.ops.ReadTag(ctx, plan.Tag)
			switch vcsFailedPushOutcome(git.TagState{Raw: env.TagRaw, Commit: env.TagSha}, current, readErr, plan.To) {
			case vcsPushLanded:
				// The push went through and only its answer was lost.
			case vcsPushOvertaken:
				// The lease held the push back: the tag is no longer where it was read. Whoever moved it
				// has the newer word, and the next reconcile takes it from there.
				logger.Info("Environment tag was moved by someone else before the controller could move it",
					"branch", plan.Branch, "tag", plan.Tag, "wanted", plan.To, "now", current.Commit)
				results[i].Overtaken = true
				continue
			default:
				failed(err)
				continue
			}
		}
		// The controller pushes lightweight tags, so the ref now holds the commit itself.
		env.TagSha, env.TagRaw = plan.To, plan.To
		byBranch[env.Branch] = *env
		results[i].Moved = true

		from := plan.From
		if from == "" {
			from = constants.EnvironmentTagNoPreviousSha
		}
		logger.Info("Moved environment tag", "branch", plan.Branch, "tag", plan.Tag, "from", from, "to", plan.To, "source", plan.MoveSource)
		r.Recorder.Eventf(vcs, nil, "Normal", constants.EnvironmentTagMovedReason, "AdvancingTag",
			constants.EnvironmentTagMovedMessage, plan.Tag, plan.Branch, from, plan.To, plan.MoveSource)
	}
	return results
}

// vcsRecordEnvironments writes what this reconcile found and did about every environment into status, and
// into envs what put each tag where it is now, for the gate. Each environment keeps its last gate report;
// writeGates replaces it where there is a proposed change. cursor is how far the validation notes have been
// applied: every validation up to it has moved its tag, is waiting as pending, or was passed over.
func vcsRecordEnvironments(vcs *promoterv1alpha1.ValidatedCommitStatus, envs []vcsEnvironment, plans []vcsTagPlan, pushed []vcsPushResult, cursor string, now metav1.Time) {
	previous := make(map[string]promoterv1alpha1.ValidatedCommitStatusEnvironmentStatus, len(vcs.Status.Environments))
	for _, entry := range vcs.Status.Environments {
		previous[entry.Branch] = entry
	}

	entries := make([]promoterv1alpha1.ValidatedCommitStatusEnvironmentStatus, 0, len(envs))
	for i := range envs {
		env, plan, result := &envs[i], plans[i], pushed[i]
		entry := promoterv1alpha1.ValidatedCommitStatusEnvironmentStatus{
			Branch:                      env.Branch,
			Tag:                         plan.tagStatus(result.Moved, now),
			Qualified:                   env.Qualified,
			ActiveCommitStatuses:        env.Status.Active.CommitStatuses,
			GateEnvironmentCommitStatus: previous[env.Branch].GateEnvironmentCommitStatus,
			Upstreams:                   previous[env.Branch].Upstreams,
		}
		if plan.Target != "" {
			entry.UpstreamCleared = &promoterv1alpha1.ValidatedCommitStatusUpstreamCleared{Sha: plan.Target}
		}
		if !result.Moved {
			entry.Held = plan.Held
		}
		if plan.Validation != nil && result.Err != nil {
			// The validation is not lost with the reconcile: the move is tried again.
			entry.Pending = &promoterv1alpha1.ValidatedCommitStatusPending{
				Sha:         plan.To,
				ValidatedBy: plan.Validation.By,
				Message:     utils.TruncateString(result.Err.Error(), 1024),
			}
		}
		env.TagSource, env.TagValidatedBy = entry.Tag.Source, entry.Tag.ValidatedBy
		entries = append(entries, entry)
	}
	vcs.Status.Environments = entries
	vcs.Status.ValidationsCursor = cursor
}

// collectValidations reads from the validation notes what people asked for since the notes were last
// applied, and what the gate itself recorded there. It fills in, for every environment, the validation to
// apply (Validation), the records of hand moves that status does not have (HandMoves), and what the notes
// say about a tag that status has no record of (Noted).
func (r *ValidatedCommitStatusReconciler) collectValidations(
	ctx context.Context,
	vcs *promoterv1alpha1.ValidatedCommitStatus,
	dry *vcsDryState,
	graph vcsCommitGraph,
	envs []vcsEnvironment,
	firstRun bool,
) error {
	logger := logf.FromContext(ctx)

	var lines []git.ValidationEvent
	resume := firstRun
	if !firstRun {
		listed, found, err := dry.ops.ListValidations(ctx, vcs.Status.ValidationsCursor, 0)
		if err != nil {
			return fmt.Errorf("failed to list validations: %w", err)
		}
		if found {
			lines = listed
		} else {
			logger.Info("The validation notes no longer contain the commit the controller had applied them up to; resuming from the environment tags",
				"validationsCursor", vcs.Status.ValidationsCursor)
			resume = true
		}
	}
	if resume {
		recent, _, err := dry.ops.ListValidations(ctx, "", vcsLostCursorWalk)
		if err != nil {
			return fmt.Errorf("failed to list validations: %w", err)
		}
		for i := range envs {
			env := &envs[i]
			if vcsResumesFromHead(*env) {
				continue
			}
			matched, later := vcsResumeAfter(recent, env.Tag, env.TagSha)
			if matched == nil {
				continue
			}
			if env.Recorded == nil {
				env.Noted = vcsNoted(matched)
			}
			lines = append(lines, later...)
		}
	}

	byTag := make(map[string]*vcsEnvironment, len(envs))
	for i := range envs {
		byTag[envs[i].Tag] = &envs[i]
	}
	for i := range lines {
		line := &lines[i]
		env, read := byTag[line.Line.Target]
		if !read {
			continue
		}
		if !line.Line.Validates() {
			if env.HandMoves == nil {
				env.HandMoves = map[string]metav1.Time{}
			}
			env.HandMoves[line.Sha] = metav1.NewTime(line.At())
			continue
		}
		if !vcsManualValidation(line.Line) {
			continue
		}
		ignored, err := r.unusableValidation(ctx, dry, graph, line.Sha)
		if err != nil {
			return err
		}
		if ignored != "" {
			logger.Info("Ignored a validation", "tag", env.Tag, "sha", line.Sha, "reason", ignored)
			r.Recorder.Eventf(vcs, nil, "Warning", constants.ValidationIgnoredReason, "ApplyingValidations",
				constants.ValidationIgnoredMessage, line.Sha, env.Tag, ignored)
			continue
		}
		// The lines are in the order they were written, and the newest validation is the one that counts.
		env.Validation = &vcsValidation{Sha: line.Sha, By: line.By()}
	}

	// A move that could not be pushed is tried again, unless a newer validation asks for another.
	previous := make(map[string]*promoterv1alpha1.ValidatedCommitStatusPending, len(vcs.Status.Environments))
	for _, entry := range vcs.Status.Environments {
		previous[entry.Branch] = entry.Pending
	}
	for i := range envs {
		env := &envs[i]
		pending := previous[env.Branch]
		if env.Validation != nil || pending == nil {
			continue
		}
		ignored, err := r.unusableValidation(ctx, dry, graph, pending.Sha)
		if err != nil {
			return err
		}
		if ignored == "" {
			env.Validation = &vcsValidation{Sha: pending.Sha, By: pending.ValidatedBy, Pending: true}
		}
	}
	return nil
}

// unusableValidation says why a validation of the given commit cannot be applied, "" when it can.
func (r *ValidatedCommitStatusReconciler) unusableValidation(ctx context.Context, dry *vcsDryState, graph vcsCommitGraph, sha string) (string, error) {
	found, err := dry.ops.EnsureCommit(ctx, sha)
	if err != nil {
		return "", fmt.Errorf("failed to look for validated commit %s: %w", sha, err)
	}
	if !found {
		return "the commit is not in the repository", nil
	}
	// A hydrated commit is a commit of the repository too, when the environment branches live with the dry
	// branch. Tagging one would have the hydrator hydrate manifests, and the gate pass the result unseen.
	base, err := graph.MergeBase(ctx, dry.head, sha)
	if err != nil {
		return "", fmt.Errorf("failed to compare validated commit %s with the dry branch: %w", sha, err)
	}
	if base == "" {
		return "the commit shares no history with the dry branch", nil
	}
	return "", nil
}

// noteHandMoves writes, into the validation notes, the record of every tag that status says was pushed by
// hand and that the notes do not have yet.
func (r *ValidatedCommitStatusReconciler) noteHandMoves(ctx context.Context, vcs *promoterv1alpha1.ValidatedCommitStatus, ops *git.EnvironmentOperations) error {
	var entries []git.ValidationEntry
	for _, env := range vcs.Status.Environments {
		entry, moved := vcsHandMoveRecord(vcs.Name, env.Tag)
		if !moved {
			continue
		}
		recorded, err := ops.ValidationRecorded(ctx, entry.Sha, entry.Line.ID)
		if err != nil {
			return fmt.Errorf("failed to look for the record of tag %q moved by hand: %w", entry.Line.Target, err)
		}
		if !recorded {
			entries = append(entries, entry)
		}
	}
	if len(entries) == 0 {
		return nil
	}
	if _, err := ops.AppendValidations(ctx, entries); err != nil {
		return fmt.Errorf("failed to record tags moved by hand: %w", err)
	}
	return nil
}

// writeGates writes the child CommitStatus of every environment that has a proposed change, and the gate's
// report into the environment's status entry (see vcsRecordEnvironments). It returns the child CommitStatuses
// that are still wanted. An environment whose gate cannot be written does not stop the others; the failures
// are returned together.
func (r *ValidatedCommitStatusReconciler) writeGates(
	ctx context.Context,
	vcs *promoterv1alpha1.ValidatedCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
	graph vcsCommitGraph,
	head string,
	envs []vcsEnvironment,
) ([]*promoterv1alpha1.CommitStatus, error) {
	logger := logf.FromContext(ctx)
	byBranch := make(map[string]vcsEnvironment, len(envs))
	for _, env := range envs {
		byBranch[env.Branch] = env
	}

	commitStatuses := make([]*promoterv1alpha1.CommitStatus, 0, len(envs))
	var errs []error
	for i, env := range envs {
		entry := &vcs.Status.Environments[i]

		// With no proposed change (active and proposed dry SHAs match) there is no promotion to gate.
		// Keep any existing CommitStatus so orphan cleanup leaves the last report alone, and mirror it
		// onto status so operators still see it after the promotion completes.
		if !vcsHasProposal(env) {
			logger.V(4).Info("Skipping environment with no proposed change", "branch", env.Branch)
			existing := &promoterv1alpha1.CommitStatus{}
			name := utils.CommitStatusResourceName(ctx, vcs, env.Branch)
			if err := r.Get(ctx, client.ObjectKey{Namespace: vcs.Namespace, Name: name}, existing); err != nil {
				if !k8serrors.IsNotFound(err) {
					errs = append(errs, fmt.Errorf("failed to get existing CommitStatus for branch %q: %w", env.Branch, err))
					continue
				}
				entry.GateEnvironmentCommitStatus = promoterv1alpha1.GateEnvironmentCommitStatus{}
				entry.Upstreams = nil
			} else {
				commitStatuses = append(commitStatuses, existing)
				entry.GateEnvironmentCommitStatus = utils.GateEnvironmentCommitStatusFromCommitStatus(existing)
				entry.Upstreams = nil
			}
			continue
		}

		gate, err := vcsEvaluateGate(ctx, graph, head, vcs.Spec.DrySource.Branch, env, byBranch)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		logger.V(4).Info("Evaluated gate for environment",
			"branch", env.Branch,
			"tag", env.Tag,
			"tagSha", env.TagSha,
			"phase", gate.Phase,
			"description", gate.Description)

		// Bind the CommitStatus to the proposed branch's hydrated SHA: that is the commit the
		// ChangeTransferPolicy inspects when gating the promotion PR.
		commitStatus, report, err := r.applyCommitStatus(ctx, vcs, ps, env, gate, env.Status.Proposed.Hydrated.Sha)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to set commit status for branch %q: %w", env.Branch, err))
			continue
		}
		commitStatuses = append(commitStatuses, commitStatus)
		entry.GateEnvironmentCommitStatus = report
		entry.Upstreams = gate.Upstreams
	}
	return commitStatuses, errors.Join(errs...)
}

// applyCommitStatus applies the child CommitStatus that carries an environment's gate.
func (r *ValidatedCommitStatusReconciler) applyCommitStatus(
	ctx context.Context,
	vcs *promoterv1alpha1.ValidatedCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
	env vcsEnvironment,
	gate vcsGate,
	reportedSha string,
) (*promoterv1alpha1.CommitStatus, promoterv1alpha1.GateEnvironmentCommitStatus, error) {
	key := vcs.Spec.Key
	commitStatusName := utils.CommitStatusResourceName(ctx, vcs, env.Branch)

	kind := reflect.TypeFor[promoterv1alpha1.ValidatedCommitStatus]().Name()
	gvk := promoterv1alpha1.GroupVersion.WithKind(kind)

	var renderedURL string
	if vcs.Spec.URL.Template != "" {
		data := ValidatedCommitStatusURLTemplateData{
			Environment:           env.Branch,
			ValidatedCommitStatus: *vcs,
			PromotionStrategy:     ps,
			DependsOn:             env.DependsOn,
			DependsOnQuery:        buildDependsOnQuery(env.DependsOn),
		}
		var err error
		renderedURL, err = renderGateURL(vcs.Spec.URL.Template, vcs.Spec.URL.Options, data)
		if err != nil {
			return nil, promoterv1alpha1.GateEnvironmentCommitStatus{}, err
		}
	}

	// The stable gate key is the SCM commit status context (spec.name), so branch protection rules can
	// reference one predictable name. The per-environment detail goes in the description.
	commitStatusSpec := acv1alpha1.CommitStatusSpec().
		WithRepositoryReference(acv1alpha1.ObjectReference().
			WithName(ps.Spec.RepositoryReference.Name)).
		WithSha(reportedSha).
		WithName(key).
		WithDescription(gate.Description).
		WithPhase(gate.Phase)
	if vcs.Spec.PublishTo != nil {
		commitStatusSpec = commitStatusSpec.WithPublishTo(*vcs.Spec.PublishTo)
	}
	if renderedURL != "" {
		commitStatusSpec = commitStatusSpec.WithUrl(renderedURL)
	}

	commitStatusApply := acv1alpha1.CommitStatus(commitStatusName, vcs.Namespace).
		WithLabels(utils.CommitStatusStandardLabels(vcs, env.Branch, key)).
		WithOwnerReferences(acmetav1.OwnerReference().
			WithAPIVersion(gvk.GroupVersion().String()).
			WithKind(gvk.Kind).
			WithName(vcs.Name).
			WithUID(vcs.UID).
			WithController(true).
			WithBlockOwnerDeletion(true)).
		WithSpec(commitStatusSpec)

	// Read the previously persisted phase (NotFound means this branch is being gated for the first time)
	// so the phase-change event below stays transition-only.
	previousPhase := ""
	existingCommitStatus := &promoterv1alpha1.CommitStatus{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: vcs.Namespace, Name: commitStatusName}, existingCommitStatus); err == nil {
		previousPhase = string(existingCommitStatus.Spec.Phase)
	} else if !k8serrors.IsNotFound(err) {
		return nil, promoterv1alpha1.GateEnvironmentCommitStatus{}, fmt.Errorf("failed to get existing CommitStatus %q: %w", commitStatusName, err)
	}

	commitStatus := &promoterv1alpha1.CommitStatus{}
	commitStatus.Name = commitStatusName
	commitStatus.Namespace = vcs.Namespace
	if err := r.Patch(ctx, commitStatus, utils.ApplyPatch{ApplyConfig: commitStatusApply}, client.FieldOwner(constants.ValidatedCommitStatusControllerFieldOwner), client.ForceOwnership); err != nil {
		return nil, promoterv1alpha1.GateEnvironmentCommitStatus{}, fmt.Errorf("failed to apply CommitStatus: %w", err)
	}

	emitCommitStatusPhaseChangedEvent(r.Recorder, vcs, key, env.Branch, previousPhase, string(gate.Phase))

	return commitStatus, promoterv1alpha1.GateEnvironmentCommitStatus{
		Phase:       gate.Phase,
		Description: gate.Description,
		Url:         renderedURL,
		ReportedSha: reportedSha,
	}, nil
}

// nudgeHydration wakes the ChangeTransferPolicy of every environment whose hydrator has not reported its
// tag's commit yet, so the policy notices the hydrator's output even when no webhook announces it. It
// returns how soon the gate should reconcile again to nudge the next time, 0 when no environment is waiting.
func (r *ValidatedCommitStatusReconciler) nudgeHydration(ctx context.Context, vcs *promoterv1alpha1.ValidatedCommitStatus, ps *promoterv1alpha1.PromotionStrategy, envs []vcsEnvironment) time.Duration {
	if r.EnqueueCTP == nil {
		return 0
	}
	r.nudgesOnce.Do(func() {
		r.nudges = newVCSBackoff(vcsNudgeBaseDelay, vcsNudgeMaxDelay)
	})

	var soonest time.Duration
	now := time.Now()
	for _, env := range envs {
		ctpName := utils.KubeSafeUniqueName(utils.GetChangeTransferPolicyName(ps.Name, env.Branch))
		// The gate's UID is part of the key so two gates (or a gate deleted and recreated) do not share state.
		key := string(vcs.UID) + "/" + ctpName
		if !vcsNeedsNudge(env) {
			r.nudges.forget(key)
			continue
		}
		fire, wait := r.nudges.next(key, env.TagSha, now)
		if fire {
			logf.FromContext(ctx).V(4).Info("Waking ChangeTransferPolicy to look for the hydrator's output", "changeTransferPolicy", ctpName, "tag", env.Tag, "tagSha", env.TagSha)
			r.EnqueueCTP(vcs.Namespace, ctpName)
		}
		soonest = vcsSooner(soonest, wait)
	}
	return soonest
}

// SetupWithManager sets up the controller with the Manager.
//
//nolint:dupl // Gate controllers share the same SetupWithManager skeleton by design.
func (r *ValidatedCommitStatusReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.ValidatedCommitStatusConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get ValidatedCommitStatus rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.ValidatedCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get ValidatedCommitStatus max concurrent reconciles: %w", err)
	}

	// The webhook receiver finds the gates to wake for a push by the dry SHAs they last observed.
	if err := mgr.GetFieldIndexer().IndexField(ctx, &promoterv1alpha1.ValidatedCommitStatus{}, promoterv1alpha1.ValidatedCommitStatusObservedDryShaField, func(rawObj client.Object) []string {
		vcs, ok := rawObj.(*promoterv1alpha1.ValidatedCommitStatus)
		if !ok {
			return nil
		}
		return vcs.ObservedDryShas()
	}); err != nil {
		return fmt.Errorf("failed to set field index for %s: %w", promoterv1alpha1.ValidatedCommitStatusObservedDryShaField, err)
	}

	externalEnqueueChan := make(chan event.GenericEvent, 1024)
	r.enqueueFunc = func(namespace, name string) {
		vcs := &promoterv1alpha1.ValidatedCommitStatus{}
		vcs.SetNamespace(namespace)
		vcs.SetName(name)

		select {
		case externalEnqueueChan <- event.GenericEvent{Object: vcs}:
		default:
			logf.FromContext(ctx).Info("ValidatedCommitStatus enqueue channel is full, blocking until space is available",
				"namespace", namespace, "name", name)
			externalEnqueueChan <- event.GenericEvent{Object: vcs}
		}
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.ValidatedCommitStatus{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&promoterv1alpha1.PromotionStrategy{}, CommitStatusGatePromotionStrategyWatchHandler[promoterv1alpha1.ValidatedCommitStatusList](r.Client)).
		// A restore revokes qualifications, so look again when one appears or reports what it restored.
		Watches(&promoterv1alpha1.RestoreActiveCommit{}, handler.EnqueueRequestsFromMapFunc(r.gatesForRestoreActiveCommit)).
		WatchesRawSource(source.Channel(externalEnqueueChan, &handler.EnqueueRequestForObject{})).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Named("validatedcommitstatus").
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

// gatesForRestoreActiveCommit maps a RestoreActiveCommit to the gates that order its PromotionStrategy.
func (r *ValidatedCommitStatusReconciler) gatesForRestoreActiveCommit(ctx context.Context, obj client.Object) []reconcile.Request {
	rac, ok := obj.(*promoterv1alpha1.RestoreActiveCommit)
	if !ok {
		return nil
	}
	var gates promoterv1alpha1.ValidatedCommitStatusList
	if err := r.List(ctx, &gates, client.InNamespace(rac.Namespace), client.MatchingFields{PromotionStrategyRefField: rac.Spec.PromotionStrategyRef.Name}); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to list ValidatedCommitStatuses for RestoreActiveCommit", "restoreActiveCommit", rac.Name)
		return nil
	}
	requests := make([]reconcile.Request, 0, len(gates.Items))
	for i := range gates.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&gates.Items[i])})
	}
	return requests
}
