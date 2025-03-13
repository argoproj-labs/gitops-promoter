# Comparison of GitOps Promotion Tools

A GitOps user defines their desired state as files in a git repo. To deploy a change, the user modifies a file and 
pushes a commit. To automatically promote that change, you need a system that knows these things:

1. What to promote

    Some stuff makes sense to promote, and some does not. You generally want to promote image tag upgrades. But you 
    generally don't want to promote environment-specific config like resource requests/limits. The tool needs a way of 
    knowing what's promotable and what is not.

2. How to "hold" a change before promotion

    The promotion tool needs somewhere to keep a change while it's waiting to be qualified for the next higher 
    environment.

3. When to make the change live

    For "promotion" to be useful, there must be rules about when a change proceeds to the next step. The promotion tool 
    needs a way to determine what qualifying rules apply to which environments.

Different tools have different ways of handling these three concepts. Below we summarize the tools, in the order they 
were introduced.

## The Options at a Glance

| Tool                       | What to promote                                 | How to "hold" the change                              | When to promote                                     |
|----------------------------|-------------------------------------------------|-------------------------------------------------------|-----------------------------------------------------|
| **GitOps Promoter**        | Environment hydrated manifests                  | Automated PR (hydrated branch)                        | When commit status and branch protection rules pass |
| **Home-Grown CI Solution** | Whatever you want                               | Whatever you want                                     | Whatever you want                                   |
| **Telefonistka**           | Environment directory contents                  | Automated PR (DRY branch)                             | When branch protection rules pass                   |
| **Kargo**                  | git commits, image tags, or Helm chart versions | A variety of options (PRs, commits, out-of-sync apps) | Defined by a DAG and a variety of rules             |
| **Codefresh GitOps**       | json-paths in Helm/Kustomize/or other manifests | Automated PR or commit (DRY) in folder or branch      | When promotion policies are met                     |

## GitOps Promoter

Where other tools mostly operate on "DRY," pre-hydrated manifests, GitOps Promoter doesn't touch those files.
Instead, the user is expected to make a manifest change to affect all target environments (e.g. an image tag bump in a
shared Kustomize base or a global Helm values file), and the Promoter will open PRs to environment-specific "hydrated 
manifest" branches.

1. What to promote: hydrated manifests for each environment
2. How to "hold" the change: open a PR against the hydrated manifest branch
3. When to promote: when specified commit status checks pass, and when branch protection rules pass

## Home-Grown CI Solution

The original solution was to write your own promoter using whatever CI tool you already used (Jenkins, GitHub Actions, 
Argo Workflows, etc.). It's a lot of work, and it's totally on you to make the design.

## Telefonistka

[Telefonistka](https://github.com/commercetools/telefonistka) relies on the opinion that all the 
stuff-to-be-promoted ought to be stored in an environment-specific directory. The promotion process is a simple 
copy/paste of the contents of one environment directory to the next environment directory, represented as an automated 
PR. The PR may optionally be auto-merged.

1. What to promote: files in an environment-specific directory
2. How to "hold" the change: open a PR copying files to next environment
3. When to promote: when PR branch protection conditions pass

## Kargo

[Kargo](https://github.com/akuity/kargo) introduces its own terminology to abstract away common GitOps concepts 
(git/artifact repos are "warehouses", git commits and image tags are "freight," etc.). It's a relatively unopinionated 
toolkit of GitOps promotion-related functionality (opening PRs, pushing commits, syncing Argo CD apps, etc.) which may 
be assembled in a DAG. You can interact with the DAG via a custom UI/CLI.

1. What to promote: "freight," i.e. git commits, image tags, or Helm chart versions (optionally selected by semver range)
2. How to "hold" the change: a variety of options are available such as opening a PR, waiting to sync an Argo CD app, 
   pushing a commit, leaving "freight" as "unqualified" (represented as status fields on CRs)
3. When to promote: defined by a DAG and a variety of rules such as manual approval or Argo CD app health

## Codefresh GitOps

[Codefresh GitOps' promotion feature](https://codefresh.io/docs/docs/promotions/promotions-overview/) uses file selectors and json paths. You can structure your git repo however you want. Then you write file 
selectors and json paths to determine which parts of the repo should be moved from file to file. For example, if your 
app is structured as a Helm chart with environment-specific values files, you can define your promotion rules to copy 
the .image.tag field from the values-dev.yaml file to the values-prod.yaml file.

This tool tracks the relationship between Argo CD applications by using a "product" name and then groups those products by "environment". An Environment can be any combination of clusters, namespaces, or Application annotations. Changes are coordinated by the GitOps control plane across any number of Argo instances, clusters, or applications. 

Currently, diffing is limited to DRY manifests with [hydrated server-side rendered diffs planned](https://roadmap.codefresh.io/c/128-promotion-preview). 

1. What to promote: json-path-specified fields from lower-env yaml files to higher-env files between Argo CD applications linked by a "product" name"
2. How to "hold" the change: Pull Request or Commit
3. When to promote: when promotion policies are met, for example, tests pass, approvals are given, pull request requirements are met, environment has certain tags, or manual promotion is allowed (drag and drop).

