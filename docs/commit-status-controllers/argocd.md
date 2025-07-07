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

## Multi-Cluster Support

GitOps promoter can monitor Argo CD Applications across multiple Kubernetes clusters. This is particularly useful when managing promotions across environments that use different Argo CD instances.

### Multi-Cluster Prerequisites

To enable multi-cluster support, you need to configure two components:

#### Kubeconfig Secret
   - Create a secret with key `kubeconfig` containing a standard `~/.kube/config` file
   - The secret must be created in the same namespace where gitops-promoter runs
   - The controller uses the `current context` from the kubeconfig to determine which cluster to uses
     
    !!! note
        Remove any additional clusters from the `kubeconfig` as they will be ignored

#### RBAC Configuration
   - Create a ClusterRole and binding in the external cluster for the service account associated with the kubeconfig
   - The service account requires the following permissions:

   ```yaml
   rules:
   - apiGroups:
     - argoproj.io
     resources:
     - applications
     verbs:
     - get
     - list
     - watch
   ```

#### Helper Script
  Use this [helper script](https://github.com/FourFifthsCode/gitops-promoter/blob/multi-cluster-support/hack/create-kubeconfig-secret-rbac.sh) to automate the secret creation and RBAC setup process
  ```
    Usage: ./create-kubeconfig-secret-rbac.sh [options]
    --secret-name NAME      Name for the secret (defaults to target context name)
    --secret-context CONTEXT Context where the secret should be created (defaults to target context)
    --secret-namespace NS    Namespace where the secret should be created (defaults to target namespace)
    --target-context CONTEXT  Kubeconfig context to use for service account (required)
    --target-namespace NS    Namespace to create the service account in (default namespace: default)
    --service-account NAME  Name of the service account to create (default: argocd-app-viewer)
    -h, --help              Show this help message

    Example: ./create-kubeconfig-secret-rbac.sh --target-context remote-cluster --target-namespace argocd --secret-context promoter-cluster --secret-namespace promoter
  ```