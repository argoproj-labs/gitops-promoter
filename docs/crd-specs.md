### PromotionStrategy

The PromotionStrategy is the user's interface to controlling how changes are promoted through their environments. In 
this CR, the user configures the list of live hydrated environment branches in their order of promotion. They'll also
configure the checks which must pass between promotion steps.

```yaml
{!internal/controller/testdata/PromotionStrategy.yaml!}
```

### ChangeTransferPolicy

A ChangeTransferPolicy represents a pair hydrated environment branch pair: the proposed environment branch and the live
environment branch. When a new commit appears in the proposed branch, the ChangeTransferPolicy will open a PR against 
the live branch. When all the configured checks pass, the ChangeTransferPolicy will merge the PR.

A PromotionStrategy will create a ChangeTransferPolicy for each configured environment. For each environment besides the
first one, the PromotionStrategy controller will inject a `proposedCommitStatus` to represent the active status of the
previous environment. This is how the PromotionStrategy ensures that the environment PRs are merged in order, respecting
the previous environments' active commit statuses.

The [Events](monitoring/events.md#changetransferpolicy) page documents the Kubernetes events produced by 
ChangeTransferPolicies.

```yaml
{!internal/controller/testdata/ChangeTransferPolicy.yaml!}
```

### PullRequest

A PullRequest is a thin wrapper around the SCM's pull request API. ChangeTransferPolicies use PullRequests to manage
promotions.

```yaml
{!internal/controller/testdata/PullRequest.yaml!}
```

### CommitStatus

A CommitStatus is a thin wrapper for the SCM's commit status API. CommitStatuses are the primary source of truth for
promotion gates. In the ideal case, the CommitStatus will write its state to the SCM's API so that the appropriate
checkmarks/failures appear in the SCM's UI. But even if the SCM API calls fail, the ChangeTransferPolicy controller will
use the contents of the CommitStatuses `spec` fields.

```yaml
{!internal/controller/testdata/CommitStatus.yaml!}
```

### GitRepository

A GitRepository represents a single git repository. It references an ScmProvider to enable access via some configured
auth mechanism.

```yaml
{!internal/controller/testdata/GitRepository.yaml!}
```

### ScmProvider

An ScmProvider represents a scm instance (such as github). It references a Secret to enable access via some configured
auth mechanism.

```yaml
{!internal/controller/testdata/ScmProvider.yaml!}
```

### ClusterScmProvider

A ClusterScmProvider represents a SCM instance (such as GitHub). ClusterScmProvider is the cluster-scoped alternative to the ScmProvider. It references a Secret in the same namespace where the promoter is running to enable access via some configured
auth mechanism. A ClusterScmProvider can be referenced by any GitRepository in the cluster, regardless of namespace.

```yaml
{!internal/controller/testdata/ClusterScmProvider.yaml!}
```

### ArgoCDCommitStatus

An ArgoCDCommitStatus is used as a way to aggregate all the Argo CD Applications that are being used in the promotion strategy. It is used
to check the status of the Argo CD Applications that are being used in the promotion strategy.

```yaml
{!internal/controller/testdata/ArgoCDCommitStatus.yaml!}
```

### TimedCommitStatus

A TimedCommitStatus provides time-based gating for environment promotions. It monitors how long commits have been running
in specified environments and creates CommitStatus resources (as active commit statuses) based on configured duration requirements.

This enables "soak time" or "bake time" policies where changes must run successfully in environments for a minimum
duration before being promoted.

```yaml
{!internal/controller/testdata/TimedCommitStatus.yaml!}
```

### WebRequestCommitStatus

A WebRequestCommitStatus provides HTTP-based gating for environment promotions. It makes configurable HTTP requests to external endpoints and evaluates the response using expressions to determine if a promotion should proceed.

This enables integration with external approval systems, monitoring platforms, feature flag services, or any HTTP-accessible endpoint to gate promotions.

```yaml
{!internal/controller/testdata/WebRequestCommitStatus.yaml!}
```

### ControllerConfiguration

A ControllerConfiguration is used to configure the behavior of the promoter.

A global ControllerConfiguration is deployed alongside the controller and applies to all promotions.

All fields are required, but defaults are provided in the installation manifests.

```yaml
{!internal/controller/testdata/ControllerConfiguration.yaml!}
```

## Status Conditions

Every CRD which is reconciled has a `status.conditions` field. Each CRD currently only populates a single `Ready` 
condition. If the `Ready` condition is `True`, then it means that 1) reconciliation of the resource has completed 
successfully, and 2) all child resources also had a `Ready` condition of `True`.

### Condition Reasons

All CRDs may have the following condition reasons:

* `ReconciliationSuccess`
* `ReconciliationFailed`

#### `ArgoCDCommitStatus`

The `ArgoCDCommitStatus` CRD may also have the following condition reasons:

* `CommitStatusesNotReady`

#### `ChangeTransferPolicy`

The `ChangeTransferPolicy` CRD may also have the following condition reasons:

* `PullRequestNotReady`

#### `PromotionStrategy`

The `PromotionStrategy` CRD may also have the following condition reasons:

* `PreviousEnvironmentCommitStatusNotReady`
* `ChangeTransferPolicyNotReady`

## Finalizers

GitOps Promoter uses Kubernetes finalizers to ensure resources are deleted in the correct order, preventing orphaned 
resources and ensuring proper cleanup of external resources (like pull requests in the SCM).

**All finalizers are managed automatically by the controllers. You do not need to set them manually via GitOps.**

### PullRequest Finalizer

**Finalizer**: `pullrequest.promoter.argoporoj.io/finalizer`

When a PullRequest is deleted, the finalizer ensures that the pull request is properly closed on the SCM before the 
Kubernetes resource is removed. This prevents orphaned pull requests in your SCM.

### GitRepository Finalizer

**Finalizer**: `gitrepository.promoter.argoproj.io/finalizer`

The GitRepository finalizer prevents deletion of the GitRepository while any PullRequest resources still reference it. 
This ensures that PullRequests can authenticate to the SCM to close themselves properly before the GitRepository is 
removed.

### ScmProvider and ClusterScmProvider Finalizers

**ScmProvider Finalizer**: `scmprovider.promoter.argoproj.io/finalizer`  
**ClusterScmProvider Finalizer**: `clusterscmprovider.promoter.argoproj.io/finalizer`

These finalizers prevent deletion of the SCM provider while any GitRepository resources still reference it. Additionally, 
the ScmProvider and ClusterScmProvider controllers manage finalizers on the Secret resources they reference:

**Secret Finalizer (ScmProvider)**: `scmprovider.promoter.argoproj.io/secret-finalizer`  
**Secret Finalizer (ClusterScmProvider)**: `clusterscmprovider.promoter.argoproj.io/secret-finalizer`

This ensures that the Secret containing authentication credentials is not deleted while it's still needed by the SCM 
provider.

### Deletion Order

When you delete a PromotionStrategy and its associated resources, the finalizers ensure deletion happens in this order:

1. **PullRequest** - Closes the PR on the SCM
2. **GitRepository** - Can be deleted once all PullRequests referencing the GitRepository are gone
3. **ScmProvider/ClusterScmProvider** - Can be deleted once all GitRepositories referencing the Provider are gone
4. **Secret** - Can be deleted once all ScmProviders/ClusterScmProviders referencing the Secret are gone

If you attempt to delete resources out of order, Kubernetes will mark them for deletion but they will remain in a 
"Terminating" state until their dependent resources are removed. This is normal and expected behavior.
