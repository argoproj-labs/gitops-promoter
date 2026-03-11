## Https

In order to enable HTTPS support for git operations, you will need the following.

### 1.Enable HTTPS in Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: controller-manager
  namespace: system
spec:
  template:
    spec:
      containers:
        - name: manager
          args:
            - "--health-probe-bind-address=:8081"
            - "--metrics-bind-address=:8443"
            - "--metrics-secure=true"
            - "--leader-elect"
```

### 2. Create Service Account

The service account will authenticate the Prometheus server to access the metrics endpoint.

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: prometheus-metrics
  namespace: monitoring
```
A cluster role and cluster role binding will also need to be created to allow the service account to access the metrics endpoint.

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: prometheus-metrics-reader-binding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: promoter-metrics-reader
subjects:
  - kind: ServiceAccount
    name: prometheus-metrics
    namespace: monitoring
```

A k8s Secret is also needed to create long-lived authentication credentials for Prometheus to scrape the metrics endpoint.

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: prometheus-metrics-token
  namespace: monitoring
  annotations:
    kubernetes.io/service-account.name: prometheus-metrics
type: kubernetes.io/service-account-token
```

Here is an example of a ServiceMonitor for scraping the metrics endpoint with Prometheus Operator:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: gitops-promoter
  namespace: monitoring
spec:
  namespaceSelector:
    matchNames:
      - promoter-system
  selector:
    matchLabels:
      control-plane: controller-manager
  endpoints:
    - port: https
      interval: 30s
      scheme: https
      tlsConfig:
        insecureSkipVerify: true
      authorization:
        type: Bearer
        credentials:
          name: prometheus-metrics-token
          key: token
```
