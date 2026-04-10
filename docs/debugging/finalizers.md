# Finalizers

GitOps Promoter uses Kubernetes `metadata.finalizers` to enforce ordering and external cleanup (for example, closing a pull request in your Git provider) before objects disappear from the cluster. Finalizers are normal Kubernetes machinery; the promoter controllers add and remove them during reconciliation.

> [!WARNING]
> Clearing finalizers by hand (`kubectl edit` / `patch` to strip `metadata.finalizers`) should be a **last resort**. It tells the API server “forget this object’s cleanup obligations,” not “run the cleanup successfully.” Use the guidance below before removing anything.

## Finalizer reference

All promoter-defined finalizer strings live in the API package as constants (see [`api/v1alpha1/constants.go`](https://github.com/argoproj-labs/gitops-promoter/blob/main/api/v1alpha1/constants.go)). They are summarized here.

| Finalizer string | Kind(s) | Purpose |
| ---------------- | ------- | ------- |
| `pullrequest.promoter.argoproj.io/finalizer` | `PullRequest` | Blocks removal of the `PullRequest` CR until the controller has closed (or otherwise reconciled) the corresponding pull request in the SCM, when a real SCM ID exists. |
| `changetransferpolicy.promoter.argoproj.io/pullrequest-finalizer` | `PullRequest` | Ensures the owning `ChangeTransferPolicy` can observe pull request status (for example ID and state) on the CR before the `PullRequest` is deleted, so promotion state stays consistent. |
| `changetransferpolicy.promoter.argoproj.io/pullrequest-finalizer-cleanup` | `ChangeTransferPolicy` | On policy deletion, forces a reconcile pass that strips the CTP-owned finalizer from related `PullRequest`s (and related cleanup) before the policy object can finish deleting. |
| `gitrepository.promoter.argoproj.io/finalizer` | `GitRepository` | Prevents deleting a `GitRepository` while non-deleting `PullRequest`s still reference that repository. |
| `scmprovider.promoter.argoproj.io/finalizer` | `ScmProvider` | Prevents deleting an `ScmProvider` while `GitRepository`s in the same namespace still reference it. |
| `clusterscmprovider.promoter.argoproj.io/finalizer` | `ClusterScmProvider` | Same dependency idea as `ScmProvider`, for cluster-scoped SCM configuration. |
| `scmprovider.promoter.argoproj.io/secret-finalizer` | `Secret` | Placed on the credentials `Secret` referenced by an `ScmProvider` so the secret cannot be removed while the provider still exists (or until the controller clears it when safe). |
| `clusterscmprovider.promoter.argoproj.io/secret-finalizer` | `Secret` | Same pattern for secrets referenced by a `ClusterScmProvider`. |

No separate finalizer constant is defined for `PromotionStrategy`; RBAC may still mention `promotionstrategies/finalizers` for generic metadata updates. Behavior you care about for promotions is mostly on `ChangeTransferPolicy` and `PullRequest` as in the table above.

## Risks of manually removing finalizers

Removing a finalizer **does not run** the controller logic that would have run on a normal delete. Effects depend on which finalizer you strip:

- **`PullRequest` (`pullrequest.promoter.argoproj.io/finalizer`)**  
  **Risk:** The Kubernetes object is gone while the real pull request may still be **open** in GitHub/GitLab/etc. You lose a single place to drive closure and can strand automation or humans on a live PR.

- **`PullRequest` (`changetransferpolicy.promoter.argoproj.io/pullrequest-finalizer`)**  
  **Risk:** The `ChangeTransferPolicy` may never record the final PR identity/state from that object. Downstream status, history, or “externally closed” handling can be wrong or racy.

- **`ChangeTransferPolicy` (`changetransferpolicy.promoter.argoproj.io/pullrequest-finalizer-cleanup`)**  
  **Risk:** The policy CR can be removed from etcd while related `PullRequest`s still carry the CTP finalizer or are not cleaned up the way the controller expects. You can leave policies “gone” but PR objects stuck terminating or inconsistent with Git.

- **`GitRepository` / `ScmProvider` / `ClusterScmProvider`**  
  **Risk:** You delete configuration or repo metadata while `PullRequest` or `GitRepository` objects still depend on it. Controllers may error, leak logical references, or leave PRs pointing at repositories or providers that no longer exist in the API.

- **`Secret` (SCM provider secret finalizers)**  
  **Risk:** Credentials disappear while `ScmProvider` / `ClusterScmProvider` / `GitRepository` still reference them, causing failing reconciles and hard-to-debug SCM auth errors.

In all cases, prefer fixing the **underlying** problem (permissions, SCM outage, bad spec, stuck reconcile) so the controller can clear finalizers itself.

## Reporting a bug: finalizer stuck

If a resource stays in `Terminating` for a long time with a promoter finalizer that never clears:

1. **Confirm which finalizer**  
   `kubectl get <kind> <name> -n <namespace> -o jsonpath='{.metadata.finalizers}'`  
   or `kubectl describe` and copy the `Finalizers` list.

2. **Capture controller signal** (adjust deployment name/namespace to your install):  
   - Logs from the **gitops-promoter** controller manager around the time deletion was requested.  
   - `kubectl get events -n <namespace> --field-selector involvedObject.name=<resource-name>` (and controller namespace if different).

3. **Resource state**  
   A redacted `kubectl get <kind> <name> -n <namespace> -o yaml` (remove secrets and tokens). Note `metadata.deletionTimestamp`, `metadata.generation`, and relevant **status** (for `PullRequest`: ID, state, conditions).

4. **Versioning**  
   Promoter / Helm chart version, Kubernetes version, and (if relevant) which SCM provider (GitHub, GitLab, …).

5. **Open an issue**  
   On [argoproj-labs/gitops-promoter](https://github.com/argoproj-labs/gitops-promoter/issues), include the finalizer string, the steps that led to delete (or scale-down), and whether removing the finalizer was required as an emergency workaround.

That gives maintainers enough to distinguish “controller never saw delete,” “SCM call failing,” “dependency ordering,” and “genuine bug in finalizer removal.”
