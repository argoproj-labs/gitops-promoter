# Contributing to GitOps Promoter

Thank you for your interest in contributing to GitOps Promoter! This document provides guidelines and instructions for setting up your development environment and contributing to the project.

## Development Environment Setup

### Prerequisites

- [Go](https://golang.org/doc/install) 1.23 or later
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [kind](https://kind.sigs.k8s.io/docs/user/quick-start/), [minikube](https://minikube.sigs.k8s.io/docs/start/), or another local Kubernetes cluster
- [Tilt](https://docs.tilt.dev/install.html) for local development
- [Docker](https://docs.docker.com/get-docker/)

### Local Development with Tilt

GitOps Promoter uses [Tilt](https://tilt.dev/) for local development. Tilt provides fast, iterative development with live updates and automatic rebuilds.

#### Setting up your cluster

1. **Create a local Kubernetes cluster** (if you don't have one already):

   Using kind:
   ```bash
   kind create cluster
   ```

   Using minikube:
   ```bash
   minikube start
   ```

2. **Install required CRDs**:

   GitOps Promoter requires the ArgoCD Application CRD for some of its controllers. Install it before starting Tilt:

   ```bash
   kubectl apply -f test/external_crds/argoproj.io_applications.yaml
   ```

   > **Note**: This CRD is required even if you're not using Argo CD, as the controller watches for Application resources. Without this CRD, the controller will crash with the error: `no matches for kind "Application" in version "argoproj.io/v1alpha1"`.

3. **Start Tilt**:

   ```bash
   tilt up
   ```

   Press `space` to open the Tilt UI in your browser, where you can view:
   - Build status for all components
   - Live logs from the controller
   - Resource status (pods, services, etc.)
   - Dashboard and UI extension build status

4. **Making changes**:

   Tilt will automatically:
   - Rebuild the controller when Go code changes
   - Rebuild the dashboard when UI code changes
   - Restart pods with the new code
   - Update manifests when CRDs change

#### Tilt CLI Commands

Useful Tilt commands for development:

```bash
# View all resources and their status
tilt get uiresource

# Describe a specific resource
tilt describe uiresource controller

# View logs for a specific resource
tilt logs controller

# Restart a resource
tilt trigger controller

# Stop Tilt and clean up resources
tilt down
```

#### Troubleshooting Tilt

**Controller keeps restarting with "Application CRD not found" error:**

Make sure you've installed the ArgoCD Application CRD:
```bash
kubectl apply -f test/external_crds/argoproj.io_applications.yaml
```

**Port conflicts:**

If you see port conflict errors, check what's already running:
```bash
kubectl get svc -n promoter-system
```

You may need to adjust ports in your Tiltfile or stop conflicting services.

**Cluster connection issues:**

Verify your cluster is running and accessible:
```bash
kubectl cluster-info
kubectl get nodes
```

If Tilt is trying to connect to an old cluster endpoint, restart Tilt:
```bash
tilt down
tilt up
```

## Code Generation

After modifying API types (files in `api/v1alpha1/`), regenerate code and manifests:

```bash
# Generate deepcopy functions and apply configurations
make generate

# Generate CRD manifests
make manifests
```

## Testing

### Running Unit Tests

```bash
make test
```

### Running E2E Tests

```bash
make test-e2e
```

## Code Style

- Follow standard Go conventions
- Run `go fmt` before committing
- Use meaningful variable and function names
- Add comments for exported functions and complex logic

## Pull Request Process

1. Fork the repository
2. Create a feature branch from `main`
3. Make your changes
4. Run tests and ensure they pass
5. Run code generation if you modified API types
6. Commit your changes with a clear commit message
7. Push to your fork and submit a pull request

### Commit Messages

Follow conventional commit format:

- `feat:` for new features
- `fix:` for bug fixes
- `docs:` for documentation changes
- `refactor:` for code refactoring
- `test:` for test changes
- `chore:` for maintenance tasks

Example: `feat: add description field to commit status tooltips`

## Documentation

- Update documentation when adding new features
- Documentation lives in the `docs/` directory
- Use clear, concise language
- Include examples where appropriate

## Questions?

If you have questions about contributing, feel free to:
- Open a GitHub issue
- Join our community discussions
- Reach out to the maintainers

Thank you for contributing to GitOps Promoter!
