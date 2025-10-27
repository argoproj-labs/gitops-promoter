GitOps Promoter produces a variety of Kubernetes events to inform users about the status of its operations.

## All Resources

All resources may produce the following events:

| Event Type | Event Reason          | Description                                              |
|------------|-----------------------|----------------------------------------------------------|
| Normal     | ReconciliationSuccess | Reconciliation of the resource completed successfully.   |
| Warning    | ReconciliationError   | An error occurred during reconciliation of the resource. |

## ArgoCDCommitStatus

[ArgoCDCommitStatuses](../crd-specs.md#argocdcommitstatus) may produce the following events:

| Event Type | Event Reason           | Description                                                                                                                |
|------------|------------------------|----------------------------------------------------------------------------------------------------------------------------|
| Warning    | CommitStatusesNotReady | One or more of the [CommitStatus](../crd-specs.md#commitstatus) resources managed by this ArgoCDCommitStatus is not Ready. |


## ChangeTransferPolicy

[ChangeTransferPolicies](../crd-specs.md#changetransferpolicy) may produce the following events:

| Event Type | Event Reason        | Description                                                                                                      |
|------------|---------------------|------------------------------------------------------------------------------------------------------------------|
| Normal     | ResolvedConflict    | A git merge conflict was resolved for a ChangeTransferPolicy.                                                    |
| Normal     | PullRequestCreated  | A pull request was created for a ChangeTransferPolicy.                                                           |
| Normal     | PullRequestMerged   | A pull request was merged for a ChangeTransferPolicy.                                                            |
| Normal     | PullRequestUpdated  | A pull request was updated for a ChangeTransferPolicy.                                                           |
| Warning    | TooManyMatchingSha  | There is more than one CommitStatus for a given key and SHA. There must only be one CommitStatus per key/sha.    |
| Warning    | PullRequestNotReady | One or more of the [PullRequest](../crd-specs.md#pullrequest) managed by this ChangeTransferPolicy is not Ready. |

## CommitStatus

[CommitStatuses](../crd-specs.md#commitstatus) may produce the following events:

| Event Type | Event Reason    | Description                                       |
|------------|-----------------|---------------------------------------------------|
| Normal     | CommitStatusSet | The CommitStatus was successfully set in the SCM. |

## PromotionStrategy

[PromotionStrategies](../crd-specs.md#promotionstrategy) may produce the following events:

| Event Type | Event Reason                            | Description                                                                                                                               |
|------------|-----------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------|
| Warning    | ChangeTransferPolicyNotReady            | One or more of the [ChangeTransferPolicy](../crd-specs.md#changetransferpolicy) resources managed by this PromotionStrategy is not Ready. |
| Warning    | PreviousEnvironmentCommitStatusNotReady | One or more of the active [CommitStatus](../crd-specs.md#commitstatus) resources for the previous environment is not Ready.               |

## GitRepository

[GitRepositories](../crd-specs.md#gitrepository) may produce the following events:

| Event Type | Event Reason    | Description                                                                                                                                 |
|------------|-----------------|---------------------------------------------------------------------------------------------------------------------------------------------|
| Warning    | DeletionBlocked | The GitRepository cannot be deleted because it still has dependent [PullRequests](../crd-specs.md#pullrequest). Delete the PullRequests first. |

## ScmProvider

[ScmProviders](../crd-specs.md#scmprovider) may produce the following events:

| Event Type | Event Reason    | Description                                                                                                                                          |
|------------|-----------------|------------------------------------------------------------------------------------------------------------------------------------------------------|
| Warning    | DeletionBlocked | The ScmProvider cannot be deleted because it still has dependent [GitRepositories](../crd-specs.md#gitrepository). Delete the GitRepositories first. |

## ClusterScmProvider

[ClusterScmProviders](../crd-specs.md#clusterscmprovider) may produce the following events:

| Event Type | Event Reason    | Description                                                                                                                                                   |
|------------|-----------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Warning    | DeletionBlocked | The ClusterScmProvider cannot be deleted because it still has dependent [GitRepositories](../crd-specs.md#gitrepository). Delete the GitRepositories first. |
