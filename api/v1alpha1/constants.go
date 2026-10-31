package v1alpha1

// CommitStatusLabel is the label used to identify commit statuses, this is used to look up commit statuses configured in the
// PromotionStrategy CR
const CommitStatusLabel = "promoter.argoproj.io/commit-status"

// PreviousEnvProposedCommitPrefixNameLabel is the prefix name for copied proposed commits
const PreviousEnvProposedCommitPrefixNameLabel = "promoter-previous-env-"

// PromotionStrategyLabel the promotion strategy which the proposed commit is associated with
const PromotionStrategyLabel = "promoter.argoproj.io/promotion-strategy"

// EnvironmentLabel the environment branch for the proposed commit
const EnvironmentLabel = "promoter.argoproj.io/environment"

// CommitStatusGateLabelPrefix is the prefix for parent-gate label keys on CommitStatus resources
// (for example promoter.argoproj.io/timed-commit-status for TimedCommitStatus).
const CommitStatusGateLabelPrefix = "promoter.argoproj.io/"

// ChangeTransferPolicyLabel the change transfer policy which the proposed commit is associated with.
const ChangeTransferPolicyLabel = "promoter.argoproj.io/change-transfer-policy"

// ArgoCDCommitStatusDefaultKey is the default commit status key for ArgoCDCommitStatus when spec.key is omitted.
const ArgoCDCommitStatusDefaultKey = "argocd-health"

// TimedCommitStatusDefaultKey is the default commit status key for TimedCommitStatus when spec.key is omitted.
const TimedCommitStatusDefaultKey = "timer"

// DependentsSuccessfulCommitStatusKey is the commit status key name used to indicate that an environment's
// dependent environments are promoted and successful.
const DependentsSuccessfulCommitStatusKey = "dependents-successful"

// DefaultOrderCommitStatusGroup is the default API group for PromotionStrategy.spec.orderCommitStatusRef.
const DefaultOrderCommitStatusGroup = "promoter.argoproj.io"

// DefaultOrderCommitStatusKind is the default kind for PromotionStrategy.spec.orderCommitStatusRef.
const DefaultOrderCommitStatusKind = "DependentsSuccessfulCommitStatus"

// LegacyPreviousEnvironmentCommitStatusKey is the commit status key used by PromotionStrategy ≤ 0.37 for
// linear ordering. DependentsSuccessfulCommitStatus deletes orphaned CommitStatuses with this label once
// the replacement gate is configured.
//
// TODO(v1.0): Remove with cleanupLegacyPreviousEnvironmentCommitStatuses.
const LegacyPreviousEnvironmentCommitStatusKey = "promoter-previous-environment"

// Finalizer constants for preventing premature resource deletion

// PullRequestFinalizer prevents deletion of PullRequest until the PR is closed in the SCM
const PullRequestFinalizer = "pullrequest.promoter.argoproj.io/finalizer"

// ChangeTransferPolicyPullRequestFinalizer prevents deletion of PullRequest until ChangeTransferPolicy copies its status
const ChangeTransferPolicyPullRequestFinalizer = "changetransferpolicy.promoter.argoproj.io/pullrequest-finalizer"

// ChangeTransferPolicyPullRequestCleanupFinalizer is set on ChangeTransferPolicy while PullRequests may carry
// ChangeTransferPolicyPullRequestFinalizer. On ChangeTransferPolicy deletion, the controller removes the PR finalizers
// and clears this finalizer so the policy can finish deleting without waiting for per-PR status propagation.
const ChangeTransferPolicyPullRequestCleanupFinalizer = "changetransferpolicy.promoter.argoproj.io/finalizer"

// GitRepositoryFinalizer prevents deletion of GitRepository while PullRequests reference it
const GitRepositoryFinalizer = "gitrepository.promoter.argoproj.io/finalizer"

// ScmProviderFinalizer prevents deletion of ScmProvider while GitRepositories reference it
const ScmProviderFinalizer = "scmprovider.promoter.argoproj.io/finalizer"

// ClusterScmProviderFinalizer prevents deletion of ClusterScmProvider while GitRepositories reference it
const ClusterScmProviderFinalizer = "clusterscmprovider.promoter.argoproj.io/finalizer"

// ScmProviderSecretFinalizer prevents deletion of Secret while ScmProvider references it
const ScmProviderSecretFinalizer = "scmprovider.promoter.argoproj.io/secret-finalizer"

// ClusterScmProviderSecretFinalizer prevents deletion of Secret while ClusterScmProvider references it
const ClusterScmProviderSecretFinalizer = "clusterscmprovider.promoter.argoproj.io/secret-finalizer"

// RestoreActiveCommitFinalizer holds deletion until, when spec.blockEnvironment is false, the controller
// has stamped Promoter-restore-unblocked-at on the restore commit. A delete while the field is still
// true (for example garbage collection after the PromotionStrategy is deleted) removes the finalizer
// without writing the note.
const RestoreActiveCommitFinalizer = "restoreactivecommit.promoter.argoproj.io/finalizer"

// ShaLabel records the hydrated commit SHA a resource was created for. Set by KubernetesResourceCommitStatus
// on the real resources it creates.
const ShaLabel = "promoter.argoproj.io/sha"

// ResourceCreatedAtAnnotation records, in RFC 3339 with nanoseconds, when KubernetesResourceCommitStatus created a
// resource. metadata.creationTimestamp only has second granularity, so history pruning orders resources by this
// annotation first.
const ResourceCreatedAtAnnotation = "promoter.argoproj.io/created-at"

// TemplateGroupSuffix is appended to a source API group to form the API group of the wrapped template kinds
// generated by the KubernetesResourceCommitStatus controller (for example batch.templates.promoter.argoproj.io).
const TemplateGroupSuffix = "templates.promoter.argoproj.io"

// TemplateCoreGroup stands in for the core (empty) API group in wrapped template group names
// (core.templates.promoter.argoproj.io).
const TemplateCoreGroup = "core"

// TemplateCRDLabel marks CustomResourceDefinitions generated by the KubernetesResourceCommitStatus controller.
const TemplateCRDLabel = "promoter.argoproj.io/template-crd"

// TemplateCRDSourceAnnotation records the source group/version/kind a wrapped template CRD was generated from.
const TemplateCRDSourceAnnotation = "promoter.argoproj.io/template-source"

// InstanceIDLabel partitions resources between multiple controller installs sharing an API server.
// Each install configures ControllerConfiguration.spec.instanceID (exact label match) or leaves it
// unset (only unlabeled resources). Labeled and unlabeled resources are never reconciled together.
const InstanceIDLabel = "promoter.argoproj.io/instance-id"

// WebRequestCommitStatusAccessLabel grants WebRequestCommitStatuses access to the labeled object. Today the
// controller requires it on Secrets referenced by httpRequest.authentication secretRefs (basic, bearer, oauth2,
// tls) and refuses to read a Secret without it set to AccessAllowed, because it sends the credentials to a URL
// the WebRequestCommitStatus author controls.
const WebRequestCommitStatusAccessLabel = "promoter.argoproj.io/webrequestcommitstatus-access"

// Access is the value of a <kind>-access label or annotation, such as WebRequestCommitStatusAccessLabel, that
// grants resources of that kind access to the object it is set on.
type Access string

const (
	// AccessAllowed lets every resource of the label's kind in the object's namespace use the object.
	AccessAllowed Access = "allowed"
)
