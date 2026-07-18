package utils

import (
	"context"
	"fmt"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	acv1alpha1 "github.com/argoproj-labs/gitops-promoter/applyconfiguration/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	acmetav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// CommitStatusResourceName returns a DNS-safe unique Kubernetes name for a CommitStatus
// owned by a gate controller: KubeSafeUniqueName(parent.Name-branch-kebabStem), where kebabStem
// matches commitStatusGateKebabStem (for example timed, argo-cd, web-request).
// Kind is taken from TypeMeta when set; otherwise resolved from GetScheme via apiutil.GVKForObject.
func CommitStatusResourceName(ctx context.Context, parent client.Object, branch string) string {
	_ = ctx // reserved for future scheme-aware naming; naming is deterministic from parent + branch.
	return KubeSafeUniqueName(parent.GetName() + "-" + branch + "-" + commitStatusGateKebabStem(parent))
}

// UpsertCommitStatusParams holds the parameters for creating or updating a CommitStatus via Server-Side Apply.
type UpsertCommitStatusParams struct {
	Parent      client.Object
	RepoRefName string
	Branch      string
	Sha         string
	Key         string
	Description string
	Phase       promoterv1alpha1.CommitStatusPhase
	FieldOwner  string
}

// UpsertCommitStatus creates or updates a CommitStatus resource using Server-Side Apply.
// The CommitStatus is owned by the parent gate object and labeled with the standard gate labels.
func UpsertCommitStatus(ctx context.Context, c client.Client, p UpsertCommitStatusParams) (*promoterv1alpha1.CommitStatus, error) {
	commitStatusName := CommitStatusResourceName(ctx, p.Parent, p.Branch)
	kind := commitStatusGateKind(p.Parent)
	gvk := promoterv1alpha1.GroupVersion.WithKind(kind)

	commitStatusApply := acv1alpha1.CommitStatus(commitStatusName, p.Parent.GetNamespace()).
		WithLabels(CommitStatusStandardLabels(p.Parent, p.Branch, p.Key)).
		WithOwnerReferences(acmetav1.OwnerReference().
			WithAPIVersion(gvk.GroupVersion().String()).
			WithKind(gvk.Kind).
			WithName(p.Parent.GetName()).
			WithUID(p.Parent.GetUID()).
			WithController(true).
			WithBlockOwnerDeletion(true)).
		WithSpec(acv1alpha1.CommitStatusSpec().
			WithRepositoryReference(acv1alpha1.ObjectReference().WithName(p.RepoRefName)).
			WithName(p.Key + "/" + p.Branch).
			WithDescription(p.Description).
			WithPhase(p.Phase).
			WithSha(p.Sha))

	commitStatus := &promoterv1alpha1.CommitStatus{}
	commitStatus.Name = commitStatusName
	commitStatus.Namespace = p.Parent.GetNamespace()
	if err := c.Patch(ctx, commitStatus, ApplyPatch{ApplyConfig: commitStatusApply}, client.FieldOwner(p.FieldOwner), client.ForceOwnership); err != nil {
		return nil, fmt.Errorf("failed to apply CommitStatus: %w", err)
	}

	return commitStatus, nil
}

// CleanupOrphanedCommitStatuses deletes CommitStatus resources labeled for the parent gate
// that are not in validCommitStatuses. The parent gate label key is derived from the owner's Kind.
func CleanupOrphanedCommitStatuses(
	ctx context.Context,
	c client.Client,
	recorder events.EventRecorder,
	owner client.Object,
	validCommitStatuses []*promoterv1alpha1.CommitStatus,
) error {
	logger := log.FromContext(ctx)
	parentLabelKey := CommitStatusGateLabelKeyForParent(owner)

	validCommitStatusNames := make(map[string]bool, len(validCommitStatuses))
	for _, cs := range validCommitStatuses {
		if cs != nil {
			validCommitStatusNames[cs.Name] = true
		}
	}

	var commitStatusList promoterv1alpha1.CommitStatusList
	err := c.List(ctx, &commitStatusList, client.InNamespace(owner.GetNamespace()), client.MatchingLabels{
		parentLabelKey: KubeSafeLabel(owner.GetName()),
	})
	if err != nil {
		return fmt.Errorf("failed to list CommitStatus resources: %w", err)
	}

	for i := range commitStatusList.Items {
		cs := &commitStatusList.Items[i]
		if validCommitStatusNames[cs.Name] {
			continue
		}
		if !metav1.IsControlledBy(cs, owner) {
			logger.V(4).Info("Skipping CommitStatus not owned by parent gate",
				"commitStatusName", cs.Name,
				"parent", owner.GetName())
			continue
		}

		logger.Info("Deleting orphaned CommitStatus",
			"commitStatusName", cs.Name,
			"parent", owner.GetName(),
			"namespace", owner.GetNamespace())

		if err := c.Delete(ctx, cs); err != nil {
			if k8serrors.IsNotFound(err) {
				logger.V(4).Info("CommitStatus already deleted", "commitStatusName", cs.Name)
				continue
			}
			return fmt.Errorf("failed to delete orphaned CommitStatus %q: %w", cs.Name, err)
		}

		recorder.Eventf(owner, nil, "Normal", constants.OrphanedCommitStatusDeletedReason, "CleaningOrphanedResources", constants.OrphanedCommitStatusDeletedMessage, cs.Name)
	}

	return nil
}
