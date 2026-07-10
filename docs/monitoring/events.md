GitOps Promoter produces a variety of Kubernetes events to inform users about the status of its operations.

## All Resources

All resources may produce the following events. They are emitted only when the Ready condition
actually transitions (its status or reason changes compared to the previous reconcile), not on
every reconcile. For a persistently failing resource that means one Warning event on the first
failure; the up-to-date failure message stays visible on the resource's Ready condition.

| Event Type | Event Reason          | Description                                              |
|------------|-----------------------|----------------------------------------------------------|
| Normal     | ReconciliationSuccess | Reconciliation of the resource completed successfully.   |
| Warning    | ReconciliationError   | An error occurred during reconciliation of the resource. |

## ArgoCDCommitStatus

[ArgoCDCommitStatuses](../crd-specs.md#argocdcommitstatus) may produce the following events:

| Event Type | Event Reason            | Description                                                                                                                |
|------------|-------------------------|----------------------------------------------------------------------------------------------------------------------------|
| Warning    | CommitStatusesNotReady  | One or more of the [CommitStatus](../crd-specs.md#commitstatus) resources managed by this ArgoCDCommitStatus is not Ready. |
| Normal/Warning | CommitStatusPhaseChanged | The aggregated Argo CD application health for an environment changed phase. Warning when the new phase is `failure`.   |
| Normal     | OrphanedCommitStatusDeleted | An orphaned [CommitStatus](../crd-specs.md#commitstatus) was deleted after it no longer applied (e.g., branch removed). |


## ChangeTransferPolicy

[ChangeTransferPolicies](../crd-specs.md#changetransferpolicy) may produce the following events:

| Event Type | Event Reason        | Description                                                                                                      |
|------------|---------------------|------------------------------------------------------------------------------------------------------------------|
| Normal     | PromotionStarted    | A new dry sha was detected on the proposed branch and its promotion to the environment started.                  |
| Normal/Warning | PromotionBlocked | A pending promotion is blocked by a proposed commit status that is not in the `success` phase. Warning when the gate phase is `failure`. |
| Normal     | PromotionCompleted  | The environment's active branch advanced to a new dry sha.                                                       |
| Normal     | ResolvedConflict    | A git merge conflict was resolved for a ChangeTransferPolicy.                                                    |
| Normal     | PullRequestCreated  | A pull request was created for a ChangeTransferPolicy.                                                           |
| Normal     | PullRequestMerged   | A pull request was merged for a ChangeTransferPolicy.                                                            |
| Warning    | TooManyMatchingSha  | There is more than one CommitStatus for a given key and SHA. There must only be one CommitStatus per key/sha.    |
| Warning    | MissingProposedHydratorMetadata | The proposed branch has hydration output but no dry SHA was found at `<activePath>/hydrator.metadata`. Check that the hydrator writes metadata under `activePath`. |
| Warning    | PullRequestNotReady | One or more of the [PullRequest](../crd-specs.md#pullrequest) managed by this ChangeTransferPolicy is not Ready. |
| Warning    | PromotionHistoryNoteFailed | Writing the promotion-history git note for a merged pull request failed. The PullRequest finalizer is kept so the write is retried. |

## PullRequest

[PullRequests](../crd-specs.md#pullrequest) may produce the following events:

| Event Type | Event Reason                         | Description                                                                                                                  |
|------------|--------------------------------------|--------------------------------------------------------------------------------------------------------------------------------|
| Normal     | PullRequestUpdated                   | The pull request was updated on the SCM (e.g., title or description changed).                                                |
| Normal     | PullRequestClosed                    | The pull request was closed on the SCM without being merged.                                                                 |
| Warning    | PullRequestExternallyMergedOrClosed  | The pull request was merged or closed directly on the SCM, outside the controller.                                          |
| Warning    | PullRequestCreateFailed              | Creating the pull request on the SCM failed. Emitted on the first failure after a healthy reconcile, not on every retry.     |
| Warning    | PullRequestMergeFailed               | Merging the pull request on the SCM failed. Emitted on the first failure after a healthy reconcile, not on every retry.      |

## TimedCommitStatus

[TimedCommitStatuses](../crd-specs.md#timedcommitstatus) may produce the following events:

| Event Type | Event Reason             | Description                                                                                                              |
|------------|--------------------------|----------------------------------------------------------------------------------------------------------------------------|
| Normal/Warning | CommitStatusPhaseChanged | The time gate's phase changed for an environment (e.g., the configured duration elapsed and the gate went from `pending` to `success`). |
| Warning    | CommitStatusesNotReady   | One or more of the [CommitStatus](../crd-specs.md#commitstatus) resources managed by this TimedCommitStatus is not Ready. |
| Normal     | OrphanedCommitStatusDeleted | An orphaned [CommitStatus](../crd-specs.md#commitstatus) was deleted after it no longer applied (e.g., environment removed). |

## GitCommitStatus

[GitCommitStatuses](../crd-specs.md#gitcommitstatus) may produce the following events:

| Event Type | Event Reason             | Description                                                                                                              |
|------------|--------------------------|----------------------------------------------------------------------------------------------------------------------------|
| Normal/Warning | CommitStatusPhaseChanged | The expression evaluation result changed phase for an environment. Warning when the new phase is `failure`.          |
| Warning    | CommitStatusesNotReady   | One or more of the [CommitStatus](../crd-specs.md#commitstatus) resources managed by this GitCommitStatus is not Ready.  |
| Normal     | OrphanedCommitStatusDeleted | An orphaned [CommitStatus](../crd-specs.md#commitstatus) was deleted after it no longer applied (e.g., environment removed). |

## WebRequestCommitStatus

[WebRequestCommitStatuses](../crd-specs.md#webrequestcommitstatus) may produce the following events:

| Event Type | Event Reason             | Description                                                                                                                   |
|------------|--------------------------|-----------------------------------------------------------------------------------------------------------------------------------|
| Normal/Warning | CommitStatusPhaseChanged | The validation result changed phase for an environment. Warning when the new phase is `failure`.                          |
| Warning    | WebRequestFailed         | The HTTP request could not be made (e.g., connection refused, timeout).                                                       |
| Warning    | CommitStatusesNotReady   | One or more of the [CommitStatus](../crd-specs.md#commitstatus) resources managed by this WebRequestCommitStatus is not Ready. |
| Normal     | OrphanedCommitStatusDeleted | An orphaned [CommitStatus](../crd-specs.md#commitstatus) was deleted after it no longer applied (e.g., environment removed). |

## CommitStatus

[CommitStatuses](../crd-specs.md#commitstatus) may produce the following events:

| Event Type | Event Reason    | Description                                       |
|------------|-----------------|---------------------------------------------------|
| Normal     | CommitStatusSet | The CommitStatus was successfully set in the SCM. |

## PromotionStrategy

[PromotionStrategies](../crd-specs.md#promotionstrategy) may produce the following events:

| Event Type | Event Reason                            | Description                                                                                                                               |
|------------|-----------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------|
| Normal     | OrphanedChangeTransferPolicyDeleted     | An orphaned [ChangeTransferPolicy](../crd-specs.md#changetransferpolicy) was deleted after environment changes (e.g., branch rename).     |
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
