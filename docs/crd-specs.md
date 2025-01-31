## PromotionStrategy

The PromotionStrategy is the user's interface to controlling how changes are promoted through their environments. In 
this CR, the user configures the list of live hydrated environment branches in their order of promotion. They'll also
configure the checks which must pass between promotion steps.

```yaml
{!docs/example-resources/PromotionStrategy.yaml!}
```

## ChangeTransferPolicy

A ChangeTransferPolicy represents a pair hydrated environment branch pair: the proposed environment branch and the live
environment branch. When a new commit appears in the proposed branch, the ChangeTransferPolicy will open a PR against 
the live branch. When all the configured checks pass, the ChangeTransferPolicy will merge the PR.

A PromotionStrategy will create a ChangeTransferPolicy for each configured environment. For each environment besides the
first one, the PromotionStrategy controller will inject a `proposedCommitStatus` to represent the active status of the
previous environment. This is how the PromotionStrategy ensures that the environment PRs are merged in order, respecting
the previous environments' active commit statuses.


```yaml
{!docs/example-resources/ChangeTransferPolicy.yaml!}
```

## PullRequest

A PullRequest is a thin wrapper around the SCM's pull request API. ChangeTransferPolicies use PullRequests to manage
promotions.

```yaml
{!docs/example-resources/PullRequest.yaml!}
```

## CommitStatus

A CommitStatus is a thin wrapper for the SCM's commit status API. CommitStatuses are the primary source of truth for
promotion gates. In the ideal case, the CommitStatus will write its state to the SCM's API so that the appropriate
checkmarks/failures appear in the SCM's UI. But even if the SCM API calls fail, the ChangeTransferPolicy controller will
use the contents of the CommitStatuses `spec` fields.

```yaml
{!docs/example-resources/CommitStatus.yaml!}
```

## GitRepository

A GitRepository represents a single git repository. It references an ScmProvider to enable access via some configured
auth mechanism.

```yaml
{!docs/example-resources/GitRepository.yaml!}
```

## ScmProvider

An ScmProvider represents a SCM instance (such as GitHub). It references a Secret to enable access via some configured
auth mechanism.

```yaml
{!docs/example-resources/ScmProvider.yaml!}
```

## ArgoCDCommitStatus

An ArgoCDCommitStatus is used as a way to aggregate all the ArgoCD Application's that are being used in the promotion strategy. It is used
to check the status of the ArgoCD Application's that are being used in the promotion strategy.

```yaml
{!docs/example-resources/ArgoCDCommitStatus.yaml!}
```
