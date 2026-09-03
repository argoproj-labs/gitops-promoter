# DryShaValidationCommitStatus

`DryShaValidationCommitStatus` gates promotion on whether the dry commit an environment is
promoting **has already been promoted and observed healthy in a lower environment** — at any point,
not necessarily right now.

That "at any point" is the whole difference between this gate and
[`PreviousEnvironmentCommitStatus`](previous-environment-commit-status.md) or
[`DAGCommitStatus`](dag-commit-status.md). Those ask whether an upstream environment is running the
target dry commit *at this moment*. This one asks whether it ever did.

## The problem it solves

Every environment's proposed branch is hydrated independently from the dry branch, so when the dry
branch moves faster than promotions complete, the top of the pipeline starves:

1. `main` produces dry commit `D`. Every environment's proposed branch picks it up.
2. `dev` promotes `D` and becomes healthy — but by then `main` has produced `D+1`, so `prd`'s
   proposed dry commit has already moved on.
3. `prd`'s gate now compares `D+1` against `dev`'s current state, which is `D`. Pending.
4. `dev` promotes `D+1`. `main` produces `D+2`. Repeat.

`prd` is perpetually one step behind, and the pipeline effectively resets to the lowest environment
on every dry-branch commit. The equality check against an upstream's *current* dry commit can only
succeed if the dry branch holds still long enough for the whole chain to catch up.

This gate breaks that loop. `dev` having moved on to `D+2` does not erase the fact that it ran `D`
successfully, so `prd` is free to promote `D` whenever it gets there.

## Configuration

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: example
spec:
  activeCommitStatuses:
  - key: argocd-health
  proposedCommitStatuses:
  - key: promoter-dry-sha-validation
  environments:
  - branch: environments/dev
  - branch: environments/stg
  - branch: environments/prd
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: DryShaValidationCommitStatus
metadata:
  name: example
spec:
  promotionStrategyRef:
    name: example
  key: promoter-dry-sha-validation
  lookbackLimit: 10
  # environments is optional; when omitted the PromotionStrategy's environment order
  # is compiled into a chain (each environment depends on the one before it).
```

The `key` must also appear in the `PromotionStrategy`'s `proposedCommitStatuses`, or the gate this
controller produces is never enforced.

### Dependency graphs

Like `DAGCommitStatus`, this gate accepts an arbitrary acyclic graph rather than a straight line, so
it works with parallel and fan-in topologies:

```yaml
spec:
  environments:
    - branch: environments/dev
    - branch: environments/e2e
      dependsOn: [environments/dev]
    - branch: environments/perf
      dependsOn: [environments/dev]
    - branch: environments/prd
      dependsOn: [environments/e2e, environments/perf]
```

An environment with no `dependsOn` is a graph root and always passes — there is nothing below it to
validate against.

`dependsOn` is followed **transitively**, and **any** lower environment satisfies the gate. In the
graph above, `environments/prd` passes as soon as `e2e`, `perf` *or* `dev` has run the dry commit —
`dev` alone is enough. If you need a promotion to be blocked until specific environments have taken
a change, this is not the gate for that; use `DAGCommitStatus`, which requires the upstreams to be
on the target dry commit and healthy.

The graph's branches must be exactly the `PromotionStrategy`'s environments. Cycles, self-references
and unknown branches are rejected.

### `lookbackLimit`

The controller reconstructs each lower environment's promotion history from git, walking
`lookbackLimit` first-parent commits back from the tip of its active branch (default `10`, maximum
`100`). A dry commit that went live longer ago than that reads as unvalidated and the gate stays
pending. Raise it if your lower environments promote far more often than your upper ones.

## How validation is determined

For each lower environment, the controller walks its active branch and answers two questions per
commit:

**Which dry commit did this commit make live?** The `hydrator.metadata` file committed at that
revision is the ground truth. When it is unreadable, the promotion-history git note's proposed dry
SHA is used, then the hydrator git note.

**Was it healthy?** Health is not recorded on the commit that introduced a dry commit. The promoter
snapshots the *outgoing* active commit statuses when it updates a promotion pull request, so the
health of the dry commit introduced by commit `C` is carried by the trailers of the commit merged
after `C` — whose `Sha-dry-active` names the dry commit those statuses describe. The branch tip has
no successor yet, so its health comes from the live `PromotionStrategy` status instead.

Trailers are read from the promotion-history git note first and fall back to the commit message. A
squash merge or a merge performed on the SCM rewrites the message; the note survives.

Every check fails closed. A commit with no status trailers, or whose trailers describe a different
dry commit, reads as unvalidated — never as healthy.

An environment that configures no active commit statuses at all has nothing to be healthy about, so
for that environment having gone live is the whole signal. This matches how the
previous-environment gate treats the same case.

## Trade-offs

- **This gate is deliberately weaker than the previous-environment gate.** A lower environment that
  has since regressed does not re-block a dry commit it already validated. The gate records that the
  commit was good when it ran, not that the environment is healthy now. Pair it with
  `activeCommitStatuses` if you also want the current state to matter.
- **It depends on promoter-written history.** Dry commits promoted before the promoter managed the
  branch, or by pull requests that were merged before the promoter ever updated them with trailers,
  carry no health record and read as unvalidated.
- **It costs git reads.** Each reconcile fetches the repository and walks up to `lookbackLimit`
  commits per lower environment consulted. Nothing is cloned when no environment has an in-flight
  promotion.

## Status

`status.environments` records the most recent evaluation per environment, so you can see why a gate
is in the phase it is without digging through git:

```yaml
status:
  environments:
  - branch: environments/prd
    targetDrySha: 9f2c1ab4e7d05c3182a6b40fe9c7d8135a0b6e42
    phase: success
    validatedIn: environments/dev
    validatedAt: "2026-09-03T17:51:02Z"
    commitsScanned: 10
    lastEvaluationTime: "2026-09-03T18:04:11Z"
```

`commitsScanned` is what tells "not validated yet" apart from "validated, but it aged out of the
lookback window".
