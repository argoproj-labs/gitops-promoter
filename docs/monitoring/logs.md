GitOps Promoter produces structured logs. All log messages emitted by controllers use the default controller-runtime log 
fields. Non-controller components (such as the webhook handler) use the same log format, but will not include 
controller-specific fields.

Additional standard fields for things like rate limiting logs will be added in the future and documented here.

## Log Verbosity

The controller uses [controller-runtime's zap logger](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/log/zap), 
which supports configurable log verbosity via the `--zap-log-level` flag.

The default log level is `info`. For debugging, it is common to increase the log level to `5`, which enables verbose 
debug logging throughout the controller.

### Increasing the log level in Kubernetes

To increase the log level, edit the controller's `Deployment` and add `--zap-log-level=5` to the container's `args`:

```yaml
containers:
  - command:
      - /usr/bin/tini
      - '--'
      - /gitops-promoter
      - controller
    args:
      - --leader-elect
      - --zap-log-level=5
```

You can patch an existing deployment with:

```bash
kubectl patch deployment controller-manager -n gitops-promoter \
  --type='json' \
  -p='[{"op": "add", "path": "/spec/template/spec/containers/0/args/-", "value": "--zap-log-level=5"}]'
```

### Log level values

The `--zap-log-level` flag accepts the following values:

| Value | Description |
|-------|-------------|
| `info` | Default level. Logs informational messages and errors. |
| `debug` | Logs additional debug messages. Equivalent to level `1`. |
| `5` | Highly verbose output useful for diagnosing bugs. |

Any positive integer can be used as a log level; higher values produce more output. The most commonly used value for 
diagnosing bugs is `5`.
