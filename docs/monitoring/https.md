## Https

By default, GitOps Promoter exposes its metrics over HTTPS with self-served certificates.
This means that the metrics endpoint is secured with TLS, and Prometheus will need to be configured to scrape it over HTTPS.

### Self-Signed Certificates (Default)

The controller automatically generates self-signed TLS certificates in-memory using `controller-runtime`. No additional configuration is needed in the deployment.

For self-signed certificates, you can configure the ServiceMonitor to skip TLS verification:

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

### Custom Certificates

For production environments, it is recommended to use custom certificates that are signed by a trusted Certificate Authority (CA).

#### Create Server Certificate Secret

```yaml
  kubectl create secret generic promoter-server-certs \
    --namespace=promoter-system \
    --from-file=tls.crt=./server.crt \
    --from-file=tls.key=./server.key \
    --from-file=ca.crt=./ca.crt
```

#### Mount the certificate in the controller deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: controller-manager
  namespace: promoter-system
spec:
  template:
    spec:
      containers:
        - name: manager
          args:
            - "--metrics-bind-address=:8443"
            - "--metrics-cert-dir=/etc/prometheus-certs"
            - "--metrics-secure=true"
            - "--leader-elect"
          ports:
            - containerPort: 8443
              name: https
              protocol: TCP
          volumeMounts:
            - name: metrics-certs
              mountPath: /etc/prometheus-certs
              readOnly: true
      volumes:
        - name: metrics-certs
          secret:
            secretName: promoter-server-certs
      serviceAccountName: controller-manager
```

#### Create prometheus client certificate secret

```yaml
kubectl create secret generic prometheus-certs \
  --namespace=monitoring \
  --from-file=ca.crt=./ca.crt \
  --from-file=tls.crt=./client.crt \
  --from-file=tls.key=./client.key
```


#### Configure ServiceMonitor

Service Monitor:
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
        ca:
          secret:
            name: prometheus-certskey: ca.crt
        cert:
          secret:
            name: prometheus-certs
            key: tls.crt
        keySecret:
          name: prometheus-certs
          key: tls.key
      authorization:
        type: Bearer
        credentials:
          name: prometheus-metrics-token
          key: token
```

Mount the certificate secret in the controller deployment:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: controller-manager
  namespace: promoter-system
spec:
  template:
    spec:
      containers:
        - name: manager
          args:
            - "--metrics-bind-address=:8443"
            - "--metrics-cert-dir=/etc/prometheus-certs"
            - "--metrics-secure=true"
            - "--leader-elect"
          ports:
            - containerPort: 8443
              name: https
              protocol: TCP
          volumeMounts:
            - name: metrics-certs
              mountPath: /etc/prometheus-certs
              readOnly: true
      volumes:
        - name: metrics-certs
          secret:
            secretName: promoter-server-certs
      serviceAccountName: controller-manager
```