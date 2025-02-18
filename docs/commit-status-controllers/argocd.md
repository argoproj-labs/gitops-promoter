# Argo CD Commit Status Controller

The Argo CD Commit Status controller is a controller that enables environment gating 
based on Argo CD's concept of a healthy application. The controller listens for updates on 
Applications based on a common label on the Application resources managed by a particular 
PromotionStrategy.

!!! important
    Currently this controller only works with Argo CD Applications that are configured to use the hydrator.

!!! important
    Currently the repo URL configured in the PromotionStrategy must be exactly the same as the repo URL configured in the Argo CD Application.


## Example Configurations

In this example we see an ArgoCDCommitStatus resource that is configured to select all Argo CD Applications
that have the label `app: webservice-tier-1`. These Applications must also be associated with the PromotionStrategy
named `webservice-tier-1`. Once this is configured, the controller will create a CommitStatus resource for each application
that matches the selector.

```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: ArgoCDCommitStatus
metadata:
  name: webservice-tier-1
spec:
  promotionStrategyRef:
    name: webservice-tier-1
  applicationSelector:
    matchLabels:
      app: webservice-tier-1
```

To configure the PromotionStrategy, we need to specify the active commit statuses that are required for the promotion to proceed.
You can see this in the example below with the `activeCommitStatuses` field, having a key of `argocd-health`. This key is the
same key that the Argo CD commit status controller will use when it creates its CommitStatus resources.


```yaml
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: argocon-demo
spec:
  activeCommitStatuses:
    - key: argocd-health
  environments:
    - branch: environments/development
    - branch: environments/staging
    - branch: environments/production
  gitRepositoryRef:
    name: webservice-tier-1
```