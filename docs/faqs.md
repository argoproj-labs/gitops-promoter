# Frequently Asked Questions

## Does GitOps Promoter use branches for environments?

You've probably read that [using branches for environments is an anti-pattern](https://medium.com/containers-101/stop-using-branches-for-deploying-to-different-gitops-environments-7111d0632402).
GitOps Promoter does not use branches for environments in the way that causes problems. In fact, GitOps Promoter 
_requires_ that all environments are represented in a single DRY branch.

The problems with using branches for environments are all related to the user experience of managing those branches.
When using GitOps promoter, the user only pushes to a single branch. Hydrated manifests are automatically managed in
environment-specific branches, but the user never has to interact with them directly.

## How should I bump image tags?

However you want! GitOps Promoter doesn't know how you structure your manifests, so it can't bump image tags, change 
resource limits, or anything else. It's up to you to decide how to manage your manifests.

What GitOps Promoter _does_ do is make sure that your changes are applied to all environments in a consistent way. So if
you bump an image tag in a Kustomize base or in a global Helm values file, GitOps Promoter will make sure that change is
applied to all environments.

By focusing on the promotion part and leaving manifest manipulation to other tools, GitOps Promoter is able to reliably
handle whatever manifest structure your organization prefers.

## Do I need Argo CD to use GitOps Promoter?
No, GitOps Promoter works with any tools that can provide hydrated manifests in Git.
Check the [Custom Hydration](./custom-hydrator.md) documentation for more details.

## Why Should I use Webhooks?

Webhooks allow GitOps Promoter to react to events from your Git repository in real-time. This means that when you push
a new commit, GitOps Promoter can start the promotion process immediately, rather than waiting for a scheduled poll.
Using webhooks can lead to faster deployments and a more responsive development process. Additionally, webhooks can
reduce the load on your Git server by eliminating the need for frequent polling.

## How does GitOps Promoter handle concurrent releases?

GitOps Promoter always works on releasing the latest DRY commit. If a new commit is pushed while another commit is still
moving through environments, GitOps Promoter stops working on the old commit and waits for the new commit to work its
way through the environments.

This model has some major advantages.

1. **Simplicity**: The user never has to think about the progress of commits other than the most recent one. After 
   pushing a commit, they know that higher environments will hold their current state while the new change works its way
   through the environments.

2. **Ease of use**: The pending change is always represented as a simple PR from the HEAD of the proposed branch to the 
   HEAD of the active branch. Hotfixes require simply clicking "Merge" on the PR to promote the change to a given 
   environment.

3. **Reliability**: Concurrent releases would require somehow queueing commits (in a series of PRs or some other state
   store). The implementation would be far more complex and error-prone.

The "release latest" model has some drawbacks.

1. **Delays in high-churn applications**: Frequent changes could delay releases to higher environments. An environment
   must wait the sum of all previous environments' delays before receiving a change. If commits arrive faster than that
   sum, the environment will wait at an old state until a change can clear all prior environments.

2. **Skipped commits**: There's no guarantee that every change will be released to every environment. For example, if 
   the second environment is running commit A, and active commit statuses never pass for commits B and C, then the 
   second environment will never see commits B and C. It will skip straight to D. This could confuse for users who are
   used to a queue-based deployment. It may make it more difficult to diagnose which commit caused a problem in a given
   environment.

For most use cases, these tradeoffs are worth it.

To mitigate the downsides, try to speed up active commit statuses (maybe by shifting more of those validations left) or
adopting a "slower" release model, such as by batching multiple changes in a single DRY commit.

Once the "release latest" model is validated in production environments, we may consider adding a queue-based model for
users who need it.
