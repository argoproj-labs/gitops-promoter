There are two general approaches to making changes across environments: one commit (driftless) or multiple commits
(intentional drift).

## One DRY Commit (Driftless)

The simplest way to make changes, and the way that works best with GitOps Promoter's default behavior, is to
_make one commit applying the change to all applicable environments_. This could be bumping an image tag, changing
Ingress resources to Gateway resources, etc.

Just because the change is made to all environments doesn't necessarily mean it looks the same for all environments. For
example, the production environment may serve three endpoints instead of one, and therefore the new Gateway resource may
look different from lower environments. It's up to the user to decide what constitutes a single coherent "change."

Similarly, not every change will apply to every environment. For example, a change to add a fourth endpoint to
production might not apply to other environments.

By making each change for all applicable environments in a single DRY commit, you fit nicely into GitOps Promoter's
view of a change. Promoter doesn't look into the contents of your DRY branch, so its unit of change is just a DRY
commit.

This approach comes with an important limitation: **environments must always be aligned.** For ideal continuous-delivery
style deployments, this is fine. You always _want_ to be shipping the latest changes to production as quickly as
possible.

But for some use cases, you need more control. For example, you might ship changes immediately to a `dev` and a `test`
environment. But then you might wait a few days for the QA team to "bless" a specific version to ship to production. In
this case, you want to keep moving new changes to `dev` and `test` while "holding back" production to special pinned
versions. For this use case, you need a different model.

## Multiple DRY Commits (Intentional Drift)

In cases where you need more granular control over what changes ship to individual environments and when, you need to
introduce intentional environmental drift to your DRY branch by spreading changes across multiple commits.

GitOps Promoter generally copes well with this approach. If you change only the production environment to pin a new,
well-tested version, and if there are no pending changes in lower environments, Promoter will happily promote just that
change.

However, by manually setting a specific version in a particular environment, you lose the "protection" Promoter would
have otherwise provided by verifying active commit statuses pass in each lower environment for that change. You
are basically taking responsibility for confirming that your changes are verified and safe to deploy, as well as
actually making the change in the DRY branch (or automating it).

Splitting changes across commits also makes it impossible for Promoter to differentiate between "a change that must be
verified" and "a change that has already been verified for a single target environment." All Promoter knows is "deploy
and verify the latest commit." So if you make changes to lower environments in commit A and then immediately another
change to production in commit B, Promoter will block the production promotion waiting for the pending change to clear
the lower environments. If there are highly frequent changes in lower environments, this runs the risk of delaying
production promotions for a very long time (as covered in
[FAQs > How does GitOps Promoter handle concurrent releases?](faqs.md#how-does-gitops-promoter-handle-concurrent-releases)).

## Other Alternatives

While these two approaches (single commit and multiple commit) should satisfy most use cases reasonably well, GitOps
Promoter will likely support additional options in the future.

One promising idea is to use single commits (driftless) to represent each change, but use environment-specific tags to
advance environments to specific DRY commits instead of always tracking HEAD. While not solving the problem of actually
advancing the tags (human or automation will still need to intervene), this _would_ allow Promoter to apply its active
commit status protections, since each change is uniquely identified by a single DRY commit hash.

This mode would require GitOps Promoter to maintain historical data about past commits' success/failure so that gates
can be applied potentially much later when higher environments' tags are advanced to newer commits. But the storage
problem should be solvable in a way that is transparent to the user. Keep an eye on the [roadmap](roadmap.md) for
updates about the progress of this approach.