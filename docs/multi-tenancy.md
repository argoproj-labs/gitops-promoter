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

PromotionStrategies, GitRepositories, and ScmProviders may only reference resources in the same namespace. This prevents
one tenant from referencing a Secret in another tenant's namespace and gaining write access to another tenant's 
repositories.

**Important**: Provision Secrets securely!

We recommend using a GitOps-friendly Secret provisioning system that populates the Secret resource on-cluster, such as 
an external secrets operator or sealed secrets.

If an administrator does not want to use namespace-based tenancy, they must either fully manage GitOps Promoter 
resources themselves or build some other system to regulate Secret access among tenants (for example, by validating
that one tenant's resources do not reference another tenant's resources within the same namespace).

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
