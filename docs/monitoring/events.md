GitOps Promoter produces a variety of Kubernetes events to inform users about the status of its operations.

## ChangeTransferPolicy

[ChangeTransferPolicies](../crd-specs.md#changetransferpolicy) may produce the following events:

| Event Type | Event Reason       | Description                                                                                                   |
|------------|--------------------|---------------------------------------------------------------------------------------------------------------|
| Normal     | ResolvedConflict   | A git merge conflict was resolved for a ChangeTransferPolicy.                                                 |
| Normal     | PullRequestCreated | A pull request was created for a ChangeTransferPolicy.                                                        |
| Normal     | PullRequestMerged  | A pull request was merged for a ChangeTransferPolicy.                                                         |
| Warning    | TooManyMatchingSha | There is more than one CommitStatus for a given key and SHA. There must only be one CommitStatus per key/sha. |
