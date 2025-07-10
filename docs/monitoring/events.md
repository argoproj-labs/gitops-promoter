GitOps Promoter produces a variety of Kubernetes events to inform users about the status of its operations.

| Resource                                                     | Event Type | Event Reason       | Description                                                                                                   |
|--------------------------------------------------------------|------------|--------------------|---------------------------------------------------------------------------------------------------------------|
| [ChangeTransferPolicy](../crd-specs.md#changetransferpolicy) | Normal     | ResolvedConflict   | A git merge conflict was resolved for a ChangeTransferPolicy.                                                 |
| [ChangeTransferPolicy](../crd-specs.md#changetransferpolicy) | Normal     | PullRequestCreated | A pull request was created for a ChangeTransferPolicy.                                                        |
| [ChangeTransferPolicy](../crd-specs.md#changetransferpolicy) | Normal     | PullRequestMerged  | A pull request was merged for a ChangeTransferPolicy.                                                         |
| [ChangeTransferPolicy](../crd-specs.md#changetransferpolicy) | Warning    | TooManyMatchingSha | There is more than one CommitStatus for a given key and SHA. There must only be one CommitStatus per key/sha. |
