# Dynamic Pull Request Reviewers

GitOps Promoter can request **SCM reviews** on promotion pull requests, using an [expr](https://github.com/expr-lang/expr) expression to decide who is asked. This puts the people responsible for approving a promotion on the pull request as soon as it is opened, and lets them find promotion PRs with their SCM's native "review requested" filters.

## Configure on PromotionStrategy

Configure reviewers at the top level of `PromotionStrategy`. The PromotionStrategy controller copies `spec.pullRequest` onto each generated `ChangeTransferPolicy`.

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: my-app
spec:
  gitRepositoryRef:
    name: my-repo
  pullRequest:
    reviewers:
      expression: |
        let autoMerge = Spec.AutoMerge ?? true;
        autoMerge ? [] :
          Spec.ActiveBranch == 'environment/production'
            ? ['alice', {group: 'release-managers'}]
            : ['charlie']
  environments:
    - branch: environment/development
      autoMerge: true
    - branch: environment/staging
      autoMerge: false
    - branch: environment/production
      autoMerge: false
```

> [!NOTE]
> There is no per-environment `reviewers` field. Reviewers are made environment-specific through the
> `Spec.ActiveBranch` expression variable, which is always the branch name for the environment under
> evaluation.

## Reviewer values

Each item the expression returns is either:

| Form | Meaning |
|------|---------|
| `'alice'` | a username; shorthand for `{user: 'alice'}` |
| `{user: 'alice'}` | a username |
| `{group: 'release-managers'}` | a group or team; on GitHub, an organization team slug |

The object form exists so other identifier kinds (numeric IDs, emails, UUIDs) can be added for providers that cannot resolve plain names. At most 10 reviewers may be returned, and each must be non-empty, at most 100 characters, and free of whitespace.

## autoMerge

Reviewers are not skipped automatically for auto-merged environments. Gate them in the expression, as above: `Spec.AutoMerge` is the `autoMerge` value for the environment being promoted, and is unset when the field is omitted (which defaults to `true`).

## How it works

1. **PromotionStrategy → ChangeTransferPolicy**: `spec.pullRequest` is copied to each CTP.
2. **ChangeTransferPolicy → PullRequest**: The CTP controller evaluates `pullRequest.reviewers.expression`, validates the result, and writes `PullRequest.spec.reviewers`.
3. **PullRequest → SCM**: The PullRequest controller compares `spec.reviewers` with `status.appliedReviewers`, requests reviews for anything added, withdraws requests for anything removed, and records the result in `status.appliedReviewers`.

Reviewers are kept in sync with the expression's result: dropping a reviewer withdraws the request. Only reviewers recorded in `status.appliedReviewers` are candidates for removal, so reviewers added out of band on the SCM are left alone. Whatever the SCM does by default when a reviewer is re-added applies unchanged — GitOps Promoter does not inspect or manipulate review state.

Because reviewers are re-evaluated on every reconcile, prefer expressions keyed on stable inputs such as `Spec.ActiveBranch` over ones keyed on commit status phases, which flip as gates run.

## Expression context

The expression is evaluated with the same variables as [pull request labels](pull-request-labels.md#expression-context): `Status`, `Spec`, and `PromotionStrategy`.

## Provider support

| Provider | Reviewers |
|----------|-----------|
| GitHub | supported; users and organization teams |
| GitLab | not yet implemented |
| Gitea | not yet implemented |
| Forgejo | not yet implemented |
| Azure DevOps | not yet implemented |
| Bitbucket Cloud | not yet implemented |

Configuring reviewers for a repository on a provider that does not implement them fails the PullRequest reconcile with a clear error rather than being silently ignored; the `Ready` condition on the `PullRequest` carries the message.