## Https

By default, GitOps Promoter exposes its metrics over HTTPS with self-served certificates.
This means that the metrics endpoint is secured with TLS, and Prometheus will need to be configured to scrape it over HTTPS.

For production environments, it's recommended to use custom certificates that are signed by a trusted Certificate Authority (CA) to avoid issues with self-signed certificates.
To do so, you can create a Secret with the certificate and reference it in the ServiceMonitor's tlsConfig:

Secret:
```yaml
kubectl create secret generic prometheus-certs \
  --from-file=ca.crt=./ca.crt \
  --from-file=tls.crt=./client.crt \
  --from-file=tls.key=./client.key
```

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
            name: prometheus-certs
            key: ca.crt
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