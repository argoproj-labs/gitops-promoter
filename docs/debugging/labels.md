# Labels

GitOps Promoter controllers set Kubernetes labels to associate resources, filter `CommitStatus` objects during promotion checks, and tie child objects back to their owning gate or policy. Label **keys** for built-in behavior are defined as constants in [`api/v1alpha1/constants.go`](https://github.com/argoproj-labs/gitops-promoter/blob/main/api/v1alpha1/constants.go). Gate controllers also use **derived** parent-gate label keys (see below).

> [!NOTE]
> Branch names and other free-form strings are passed through `utils.KubeSafeLabel()` before they are stored as label **values** (slashes become hyphens, length is capped at 63 characters). When debugging, compare the label value to the sanitized form, not always the raw branch string from your spec.

## Label reference

### Promotion and change transfer

| Label key | Kind(s) | Set by | Value | Purpose |
| --------- | ------- | ------ | ----- | ------- |
| `promoter.argoproj.io/instance-id` | All Promoter CRs in a multi-install deployment; `Secret` objects referenced for SCM, HTTP auth, or kubeconfig | Operator on root resources (`PromotionStrategy`, `GitRepository`, `ScmProvider`, `ClusterScmProvider`, gate CRs), referenced secrets, and kubeconfig secrets; controllers propagate to children (`ChangeTransferPolicy`, `CommitStatus`, `PullRequest`) | Matches `ControllerConfiguration.spec.instanceID` for that install | Partitions which controller install caches and reconciles the object. Omit the label for the default install (when `instanceID` is unset). `GitRepository` and SCM providers are **not** propagated from parents — label them explicitly during migration. See [Multiple Controller Installs](../multi-install.md) and the [migration runbook](../multi-install.md#migration-runbook). After reconcile, Promoter CRs expose the applied value in `status.instanceID` (including when `Ready=False`). |
| `promoter.argoproj.io/promotion-strategy` | `ChangeTransferPolicy`, `PullRequest` | PromotionStrategy / ChangeTransferPolicy controllers | `KubeSafeLabel` of the owning `PromotionStrategy` name | Links CTPs and PRs to a promotion strategy; used to list and correlate objects per strategy. |
| `promoter.argoproj.io/environment` | `ChangeTransferPolicy`, `PullRequest` | PromotionStrategy, ChangeTransferPolicy controllers | `KubeSafeLabel` of the environment branch (for example `environment/development` → `environment-development`) | Identifies which environment branch the object applies to. (Also the second standard label on gate-created `CommitStatus`; see [CommitStatus gating](#commitstatus-gating).) |
| `promoter.argoproj.io/change-transfer-policy` | `PullRequest` | ChangeTransferPolicy controller | `KubeSafeLabel` of the owning `ChangeTransferPolicy` name | Finds PRs for a specific CTP. |

`ChangeTransferPolicy` objects receive `promotion-strategy` and `environment` when the PromotionStrategy controller creates or updates them. `PullRequest` objects receive all three labels when the ChangeTransferPolicy controller creates or updates them.

### CommitStatus gating

Built-in gate controllers set **three gating labels** on each `CommitStatus` via `utils.CommitStatusStandardLabels(parent, branch, key)`, and copy `promoter.argoproj.io/instance-id` from the parent gate when present:

| Label key | Value | Purpose |
| --------- | ----- | ------- |
| `promoter.argoproj.io/commit-status` | Gate `spec.key` (for example `argocd-health`, `timer`) | Must match the `key` in `PromotionStrategy` `activeCommitStatuses` or `proposedCommitStatuses`. ChangeTransferPolicy lists CommitStatuses by this label plus `.spec.sha`. |
| `promoter.argoproj.io/environment` | `KubeSafeLabel` of the environment branch | Identifies which environment branch this status applies to. |
| `promoter.argoproj.io/<gate-stem>-commit-status` | `KubeSafeLabel` of the parent gate CR name | Parent-gate label for orphan cleanup and ownership correlation. **Key is derived** from the parent Kind (see table below). |

Built-in parent-gate label keys (derived from Kind):

| Parent gate Kind | Label key |
| ---------------- | --------- |
| `DAGCommitStatus` | `promoter.argoproj.io/dag-commit-status` |
| `TimedCommitStatus` | `promoter.argoproj.io/timed-commit-status` |
| `ArgoCDCommitStatus` | `promoter.argoproj.io/argo-cd-commit-status` |
| `WebRequestCommitStatus` | `promoter.argoproj.io/web-request-commit-status` |
| `GitCommitStatus` | `promoter.argoproj.io/git-commit-status` |

The prefix `promoter.argoproj.io/` is `CommitStatusGateLabelPrefix`; the middle segment is a kebab-case stem from the Kind (for example `ArgoCDCommitStatus` → `argo-cd`). Custom gate controllers should use the same pattern; see [Developing a CommitStatus](../contributing/developing-a-commitstatus.md).

**Ordering gates:** Promotion ordering CommitStatuses (common keys `promoter-previous-environment` or `promoter-dag`) are created by the [DAGCommitStatus](../gating-promotions/built-in-gates/dag-commit-status.md) controller and carry all three standard labels, including `promoter.argoproj.io/dag-commit-status`. [PreviousEnvironmentCommitStatus](../gating-promotions/built-in-gates/previous-environment-commit-status.md) generates a chain-shaped `DAGCommitStatus`; it does not create CommitStatuses directly, so the parent-gate label points at the generated DAGCommitStatus name.

### Integration labels (user- or operator-set)

| Label key | Resource | Purpose |
| --------- | -------- | ------- |
| `promoter.argoproj.io/has-promotionstrategy` | Argo CD `Application` | When set to `"true"`, shows the GitOps Promoter UI extension tab even if no top-level `PromotionStrategy` appears in the application tree. Documented in [Integrating with Argo CD](../integrating-with-argocd/index.md). |

This label is **not** defined in `constants.go`; it is a convention for Argo CD Application metadata.

## How controllers use labels

- **ChangeTransferPolicy** evaluates `activeCommitStatuses` and `proposedCommitStatuses` by listing `CommitStatus` resources with `promoter.argoproj.io/commit-status=<key>` and field-selecting on `.spec.sha`.
- **Gate controllers** (DAG, Argo CD, timed, web request, git commit, scheduled) set all three standard labels via `utils.CommitStatusStandardLabels(parent, branch, key)` and use the parent-gate label when cleaning up orphaned CommitStatuses. PreviousEnvironmentCommitStatus maintains a child DAGCommitStatus that performs the CommitStatus writes.
- **PromotionStrategy** lists `ChangeTransferPolicy` objects with `promoter.argoproj.io/promotion-strategy=<strategy>` to remove orphaned policies when environments change.

## Useful queries

List CommitStatuses for a gate key and inspect labels:

```bash
kubectl get commitstatus -A -l promoter.argoproj.io/commit-status=argocd-health
kubectl get commitstatus <name> -n <namespace> -o jsonpath='{.metadata.labels}' | jq .
```

List CommitStatuses owned by a TimedCommitStatus gate:

```bash
kubectl get commitstatus -n <namespace> -l promoter.argoproj.io/timed-commit-status=<gate-name>
```

List CommitStatuses owned by a DAGCommitStatus gate (including one generated by PreviousEnvironmentCommitStatus):

```bash
kubectl get commitstatus -n <namespace> -l promoter.argoproj.io/dag-commit-status=<gate-name>
```

List ChangeTransferPolicies for a promotion strategy:

```bash
kubectl get changetransferpolicy -n <namespace> -l promoter.argoproj.io/promotion-strategy=<strategy-name>
```

List PullRequests for a change transfer policy:

```bash
kubectl get pullrequest -n <namespace> \
  -l promoter.argoproj.io/change-transfer-policy=<ctp-name>,promoter.argoproj.io/promotion-strategy=<strategy-name>
```

### Instance ID (multi-install)

List Promoter resources for a specific controller install:

```bash
# All Promoter CRs in a partition (roots + propagated children)
kubectl get promotionstrategy,gitrepository,scmprovider,clusterscmprovider,changetransferpolicy,commitstatus,pullrequest \
  -n team-a -l promoter.argoproj.io/instance-id=wave-0

# User-created roots only (not propagated from parents)
kubectl get promotionstrategy,gitrepository,scmprovider,clusterscmprovider \
  -n team-a -l promoter.argoproj.io/instance-id=wave-0

# CRs missing the label (default install scope)
kubectl get promotionstrategy,gitrepository,scmprovider,clusterscmprovider,changetransferpolicy,commitstatus,pullrequest \
  -n team-a -l '!promoter.argoproj.io/instance-id'

# Orphan instance-id (empty, typo, or decommissioned install — matches no running partition)
kubectl get promotionstrategy,gitrepository,scmprovider,clusterscmprovider,changetransferpolicy,commitstatus,pullrequest -A \
  -l 'promoter.argoproj.io/instance-id,promoter.argoproj.io/instance-id notin (wave-0,wave-1)'
```

If gating fails after enabling multi-install, confirm the gate CR and its `CommitStatus` children carry the same `instance-id` label as `ControllerConfiguration.spec.instanceID`. If promotion fails with `NotFound` on a `GitRepository` or `ScmProvider` that exists in the API, confirm those user-created roots are labeled — they are not propagated from `PromotionStrategy` or gate CRs.

## Troubleshooting

**Promotion blocked but CommitStatus looks correct**

1. Confirm `promoter.argoproj.io/commit-status` on the CommitStatus equals the `key` in the PromotionStrategy (or CTP) selector—not only `spec.name` in the SCM.
2. Confirm `.spec.sha` matches the hydrated SHA the ChangeTransferPolicy is checking (labels do not include SHA; the controller field-selects on spec).
3. For gate-created statuses, confirm all three standard labels are present (`commit-status`, `environment`, and the parent-gate key) and that the parent-gate value points at the expected gate CR name (upgrades from older releases may lack parent-gate labels until the gate reconciles; Argo CD has additional legacy orphan cleanup—see [Roadmap](../roadmap.md)).

**Label value does not match the branch in YAML**

Branch labels use `KubeSafeLabel` (for example `environment/staging` → `environment-staging`). Use the sanitized value in `-l` selectors.

**Missing labels on CommitStatus**

If a third party creates `CommitStatus` objects by hand, they must set at least `promoter.argoproj.io/commit-status` (and usually `promoter.argoproj.io/environment`) or the ChangeTransferPolicy controller will not find them. Built-in gates set all three standard labels automatically via `CommitStatusStandardLabels`.

**Orphan `instance-id` label value**

A Promoter CR labeled with `promoter.argoproj.io/instance-id=""`, a typo, or a decommissioned install ID matches no running controller partition. The object is never reconciled and may show no status or events. Remove the label (default install) or set a value served by a running install. See [Multiple Controller Installs — Troubleshooting](../multi-install.md#troubleshooting) for kubectl examples.

## Reporting a bug: labels missing or wrong

1. **Which object** — Kind, name, namespace, and controller version / chart version.
2. **Expected vs actual labels** — `kubectl get <kind> <name> -n <namespace> -o yaml` (redact secrets), focusing on `metadata.labels` and relevant spec (branch, key, sha).
3. **Owning gate or policy** — Name of the `DAGCommitStatus`, `PreviousEnvironmentCommitStatus`, `ArgoCDCommitStatus`, `TimedCommitStatus`, `WebRequestCommitStatus`, `GitCommitStatus`, `PromotionStrategy`, or `ChangeTransferPolicy` involved.
4. **Controller logs** — gitops-promoter manager logs around the reconcile window; related `kubectl get events`.
5. **Open an issue** on [argoproj-labs/gitops-promoter](https://github.com/argoproj-labs/gitops-promoter/issues) with the label keys/values you expected and what you see instead.

For implementing custom gate controllers, see [Developing a CommitStatus](../contributing/developing-a-commitstatus.md). For how commit statuses drive promotion, see [Gating Promotions](../gating-promotions/index.md).
