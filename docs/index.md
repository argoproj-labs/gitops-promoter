# GitOps Promoter

GitOps Promoter facilitates environment promotion for config managed via GitOps.

## Key Features

* Drift-free promotion process
* Robust promotion gating system
* Complete integration with git and SCM tooling
* No fragile automated changes to user-facing files

## Key Terms

* DRY (don't repeat yourself) Branch: a git branch containing config for all environments, often formatted for "DRY"
  config tools like Helm or Kustomize.
* Staging Branches: branches for "hydrated," environment-specific config awaiting promotion.
* Live Branches: branches for "hydrated," environment-specific config changes applied to the live system.
* Promotion PRs: Pull Requests from the staging branches to the live branches, automatically opened and closed by GitOps
  Promoter during promotion. There is never more than one open PR per environment.
* Hydrator: a tool that automatically pushes changes from the DRY Branch to each environment's Staging Branch.
* Hydrated Commit: a git commit pushed to Staging or Live Branches based on a DRY Branch commit.

## Why GitOps Promoter?

### Minimal Drift

One of the key features of GitOps is that it minimizes drift: the live state of a running system should always match the
desired state stored in git.

Environment promotion systems usually break the GitOps principle of minimal drift. After a user makes a change to desired
state in git, the promotion system will leave higher environments out of sync while lower environments are validated.
The state of the promotion system is usually presented via a specialized user interface outside of git.

GitOps Promoter eliminates drift and ensures that the complete system state is visible in your SCM (such as GitHub).
When the user changes desired state in git, GitOps Promoter automatically opens Pull Requests corresponding to each
environment. Once all configured promotion gates pass for the first environment, the PR for that environment will be
automatically merged. Subsequent checks run, and PRs are merged until the change is live in all environments.

If a new change arrives during the promotion process, pending PRs are updated to include the new changes, and new PRs
are opened for any other affected environments. Then the deployment process starts again from the first environment.

By using branches and PRs to represent environment promotion, we make it easy for users to find what they need to know:

* The desired state of all environments: look at the DRY branch
* The live state of an environment: look at the environment's live branch
* The state of change promotion: look at open PRs

### Complete SCM Integration

Since all promotion operations are managed via git and your SCM, the robust tooling ecosystems of those tools are
available to customize your promotion system. For example, you can use existing GitHub Actions to gate promotion by
blocking PR merges, or you could use branch protection rules to require manual approval processes. Anything you can do
with a git branch or a git PR, you can use to interact with the GitOps Promoter.

If you choose to use GitOps Promoter's CommitStatus API for promotion gating, the status of your custom checks will 
automatically appear in your SCM user interface, making the user experience feel trustworthy and polished.

### No Automated DRY Branch Commits

Many promotion systems work by automatically pushing changes to a DRY Branch. There are two downsides to this strategy.

First, automatically committing to the DRY Branch may confuse users. If a user is accustomed to doing their own manual
work in the DRY Branch (for example, modifying Kubernetes manifests), then they may be surprised when an automation
pushes a change (such as an image tag bump) or might find the automated interference disruptive to their work.

Second, automated changes to files in the DRY Branch are often fragile. The promotion system must understand the format
of the DRY Branch contents and avoid making unsafe changes. The system must also understand subtle differences between
environments and avoid promoting changes in a way that works for one environment but not for another. For example, a 
Kubernetes manifest promotion system could easily mishandle the promotion of Ingress manifest changes if different 
environments have different route configurations or different Ingress apiVersions.

Rather than operating on the DRY Branch, GitOps Promoter leaves those concerns entirely to the user. The user makes
their changes, the hydrator transforms the changes appropriately for each environment, and GitOps Promoter promotes the
hydrated changes to each environment according to the promotion strategy.

## Prerequisites

GitOps Promoter requires a hydration system to be configured in order to promote your changes.

A hydration system has two jobs:

1. Monitor a DRY Branch for new commits.
2. When a commit arrives, push an environment-specific Hydrated Commit to the Staging Branch for each environment. The 
   contents of the commit are up to the hydrator. For example, if the DRY Branch contains a Helm chart, the hydrator
   might push a file with the output of `helm template`.
3. A `hydrator.metadata` JSON file containing: `{"drySha": "<commit SHA from DRY Branch>"}`

That's the whole contract! GitOps Promoter will handle opening, updating, and merging PRs as newly-hydrated commits
arrive.

"Hydration" could be any transformation you can imagine. For Kubernetes manifests, it could be the output of 
`helm template` or `kustomize build`. For JavaScript, it could be the output of a minifier. For JPG images, it could be
the output of a compression tool. Or it could just be a simple copy of the source file(s).
