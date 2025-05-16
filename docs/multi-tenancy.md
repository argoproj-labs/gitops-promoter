# Multi-Tenancy

## PromotionStrategy Tenancy

GitOps Promoter provides namespace-based tenancy for PromotionStrategies.

To enable environment promotion, a user must install these namespaced resources:

* PromotionStrategy
* GitRepository
* ScmProvider
* Secret (for SCM access)

To enable self-service PromotionStrategy management for multiple tenants, a GitOps Promoter admin can give each 
tenant write access to a namespace to manage these resources. As long as the GitOps Promoter controller has access to 
those namespaces, it will reconcile the resources. 

Secrets with SCM credentials may only be referenced by ScmProviders in the same namespace, which in turn may only be
referenced by GitRepositories in the same namespace, which may only be referenced by PromotionStrategies in the same
namespace. Limiting these references to a namespace prevents one tenant from referencing a Secret in another tenant's 
namespace and thereby gaining write access to another tenant's repositories.

**Important**: Provision Secrets securely!

We recommend using a GitOps-friendly Secret provisioning system that populates the Secret resource on-cluster, such as 
an external secrets operator or sealed secrets.

If an administrator does not want to use namespace-based tenancy, they must either fully manage GitOps Promoter 
resources themselves or build some other system to regulate Secret access among tenants (for example, by validating
that one tenant's resources do not reference another tenant's resources within the same namespace).

If there are no trust boundaries to be enforced among PromotionStrategy users, a GitOps Promoter admin may choose to 
host all resources in a single namespace, keeping in mind the need to avoid resource name collisions.

## CommitStatus Tenancy

As with PromotionStrategies, all references from CommitStatuses (to GitRepositories, then ScmProviders, and finally to
SCM Secrets) must resolve within the same namespace as the CommitStatus.

Various actors may want to manage CommitStatuses:

1. GitOps Promoter administrators
2. Special interest teams (for example, a compliance team)
3. PromotionStrategy users

A given PromotionStrategy may need to reference CommitStatuses from any or all of these actors.

To facilitate the cross-team communication, _PromotionStrategy references to CommitStatuses are cluster-scoped_. If any
CommitStatus on a cluster matches the key specified in a PromotionStrategy, then the PromotionStrategy controller will
take that CommitStatus into account for the promotion process. This allows different actors to host CommitStatuses in
their own namespaces, using their own SCM credentials.

This cluster-scoped reference is reasonably safe in a multi-tenant setup because:

1. The reference is read-only. When referencing a CommitStatus in another namespace, a PromotionStrategy does not leak
   any information about itself. It just reads the status.
2. A CommitStatus's commit SHA must match the SHA of a commit being promoted to affect promotion. In other
   words, the CommitStatus's creator must already have knowledge about the SHAs in the PromotionStrategy's repository.
3. The worst a malicious or faulty CommitStatus can do is block an environment's promotion. If a promotion is 
   erroneously blocked, the PromotionStrategy user can take advantage of an override mechanism (such as manually
   merging the blocked PR), and the GitOps Promoter's admin can investigate and remediate the faulty blocker.

# ScmProvider and ClusterScmProvider Tenancy

ScmProvider and ClusterScmProvider are the same resource but are available for different scopes and have different tenancy considerations:

- ScmProvider is a namespaced resource and must exist in the same namespace as the GitRepository referencing it. It is ideal for teams that want to manage the access to their SCM themselves and stay isolated from other tenants. 
  The secret referenced by the ScmProvider must be in the same namespace as the ScmProvider.
- ClusterScmProvider is a cluster-scoped resource and can be referenced by any GitRepository from all namespaces. This allows a centralized way to configure the SCM access for all tenants in the cluster. 
  The secret referenced by a ClusterScmProvider must be in the namespace where the promoter is deployed.
