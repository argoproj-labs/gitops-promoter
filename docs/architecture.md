# Architecture

GitOps Promoter enables developers to "make a change and forget it." The change is made once in the "DRY branch" for all
environments, then GitOps Promoter does the work of moving the change through the environment-specific "hydrated 
branches."

This diagram shows a hypothetical setup where the "hydrator" is Helm, and the tool syncing the promoted changes is Argo
CD.

Commit `3f7e` is the user's "DRY" change that applied to all environments. The SHAs of the hydrated commits are
represented as `3f7e` with an environment-specific subscript. But in reality, the hydrated commits are different SHAs
since they are on different branches and represent environment-specific contents.

[![GitOps Promoter Architecture](./assets/architecture.png)](./assets/architecture.png)

# Controller Flow

# Controller Flow

The image illustrates the general flow of how the GitOps Promoter controller operates. The diagram 
outlines the steps involved in promoting changes through different environments in a GitOps workflow. It shows the 
interaction between various components such as the Git repository, the GitOps Promoter controller, and the environment-specific 
branches.

[![GitOps Promoter Architecture](./assets/architecture-diag.png)](./assets/architecture-diag.png)
