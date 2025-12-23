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

// ChangeTransferPolicyLabel the change transfer policy which the proposed commit is associated with.
const ChangeTransferPolicyLabel = "promoter.argoproj.io/change-transfer-policy"

// TimedCommitStatusLabel the timed commit status which the commit status is associated with.
const TimedCommitStatusLabel = "promoter.argoproj.io/timed-commit-status"

// PreviousEnvironmentCommitStatusKey the commit status key name used to indicate the previous environment health
const PreviousEnvironmentCommitStatusKey = "promoter-previous-environment"

// CommitStatusPreviousEnvironmentStatusesAnnotation is the label used to identify commit statuses that make up the aggregated active commit status
const CommitStatusPreviousEnvironmentStatusesAnnotation = "promoter.argoproj.io/previous-environment-statuses"

// Finalizer constants for preventing premature resource deletion

// PullRequestFinalizer prevents deletion of PullRequest until the PR is closed in the SCM
const PullRequestFinalizer = "pullrequest.promoter.argoproj.io/finalizer"

// ChangeTransferPolicyPullRequestFinalizer prevents deletion of PullRequest until ChangeTransferPolicy copies its status
const ChangeTransferPolicyPullRequestFinalizer = "changetransferpolicy.promoter.argoproj.io/pullrequest-finalizer"

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
