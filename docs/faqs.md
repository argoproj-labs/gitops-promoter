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
