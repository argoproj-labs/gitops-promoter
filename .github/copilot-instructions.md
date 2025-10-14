# GitOps Promoter - GitHub Copilot Instructions

## Project Overview

GitOps Promoter is a Kubernetes operator that facilitates environment promotion for config managed via GitOps. It provides a drift-free promotion process with a robust gating system, complete integration with git and SCM tooling.

### Key Technologies
- **Backend**: Go 1.24+ (Kubernetes operator using controller-runtime)
- **Frontend**: TypeScript/React (Dashboard UI and ArgoCD extension)
- **Infrastructure**: Kubernetes, ArgoCD integration
- **SCM Support**: GitHub, GitHub Enterprise, GitLab, Forgejo (including Codeberg)

## Architecture

The project follows a standard Kubernetes operator pattern with:
- Custom Resource Definitions (CRDs) in `api/v1alpha1/`
- Controllers in `internal/controller/`
- SCM integrations in `internal/scms/`
- Webhook receiver for SCM events in `internal/webhookreceiver/`
- Dashboard UI in `ui/dashboard/`
- ArgoCD extension in `ui/extension/`
- Shared UI components in `ui/components-lib/`

## Development Setup

### Prerequisites
- Go 1.24+
- Node.js (for UI development)
- kubectl
- Docker or compatible container tool
- Kind (for local testing)

### Build Commands
```bash
# Go backend
make build              # Build manager binary
make build-all         # Build UI components and manager binary
make test              # Run tests
make test-parallel     # Run tests in parallel
make lint              # Run linters
make lint-fix          # Run linters with auto-fix

# UI development
make build-dashboard   # Build dashboard UI
make build-extension   # Build ArgoCD extension
make lint-dashboard    # Lint dashboard
make lint-extension    # Lint extension
make lint-ui           # Lint all UI components

# Running locally
make run               # Run controller locally
make run-dashboard     # Run dashboard locally
```

## Code Style and Standards

### Go Code
- Use `go fmt` for formatting
- Follow golangci-lint rules (see `.golangci.yml`)
- Use Ginkgo/Gomega for testing
- Tests should use table-driven tests where appropriate
- Use controller-runtime patterns for Kubernetes interactions
- Avoid naked returns and follow error wrapping conventions

### TypeScript/React Code
- Use TypeScript for all new code
- Follow ESLint rules
- Run type-checking with `tsc --noEmit`
- Use functional components with hooks

### File Headers
All Go files should include the Apache 2.0 license header:
```go
/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
```

## Directory Structure

### Key Directories
- `api/v1alpha1/` - Kubernetes CRD types (PromotionStrategy, GitRepository, ScmProvider, etc.)
- `internal/controller/` - Kubernetes controllers
- `internal/scms/` - SCM provider implementations (GitHub, GitLab, Forgejo)
- `internal/git/` - Git operations
- `internal/utils/` - Shared utilities
- `internal/webhookreceiver/` - Webhook handling for SCM events
- `internal/webserver/` - Dashboard web server
- `config/` - Kubernetes manifests and Kustomize configurations
- `docs/` - Documentation (MkDocs format)
- `hack/` - Development scripts
- `test/` - Test fixtures and e2e tests
- `ui/` - Frontend code

### Generated Files
These files are auto-generated and should not be edited manually:
- `api/v1alpha1/zz_generated.deepcopy.go`
- `config/crd/bases/*.yaml`
- Files in `dist/`

## Testing Guidelines

### Unit Tests
- Use Ginkgo/Gomega for Go tests
- Test files should be named `*_test.go`
- Use `Context` and `It` blocks for test organization
- Mock external dependencies
- Use `envtest` for controller testing

### Running Tests
```bash
make test              # Run all tests
make test-parallel     # Run tests in parallel (faster)
make test-e2e          # Run end-to-end tests
```

### Test Patterns
- Use table-driven tests for multiple similar test cases
- Test both success and error paths
- Use `Eventually` for async operations in controller tests
- Clean up resources in `AfterEach` blocks

## Custom Resources

### Core CRDs
1. **PromotionStrategy** - Defines promotion flow between environments
2. **GitRepository** - Represents a Git repository
3. **ScmProvider** - SCM provider configuration (GitHub, GitLab, Forgejo)
4. **PullRequest** - Represents a promotion pull request
5. **CommitStatus** - Commit status tracking
6. **ArgoCDCommitStatus** - ArgoCD-specific commit status

### Resource Relationships
- PromotionStrategy references GitRepository
- GitRepository references ScmProvider
- ScmProvider references a Secret for credentials

## Common Patterns

### Controller Patterns
- Use structured logging with `logr`
- Return `ctrl.Result{RequeueAfter: duration}` for retries
- Use conditions to track resource status
- Handle resource not found errors gracefully
- Use finalizers for cleanup operations

### Git Operations
- All git operations are in `internal/git/`
- Use bare repositories for efficiency
- Handle authentication via SCM provider

### SCM Integration
- SCM providers implement interfaces in `internal/scms/`
- Support for GitHub Apps, GitLab tokens, Forgejo tokens
- Webhook support for real-time updates

## Environment Variables and Configuration

Key environment variables:
- `KUBEBUILDER_ASSETS` - Path to test binaries (for tests)
- Image configuration via Makefile variables

## Documentation

- Documentation is in `docs/` using MkDocs
- Update `docs/getting-started.md` for setup changes
- Update `docs/architecture.md` for architectural changes
- Keep version numbers in sync using `hack/bump-docs-manifests.sh`

## Best Practices

1. **Minimal Changes**: Make the smallest possible changes to achieve the goal
2. **Test First**: Write tests before implementing features
3. **Error Handling**: Always handle errors explicitly, don't ignore them
4. **Logging**: Use structured logging with appropriate levels
5. **Dependencies**: Avoid adding new dependencies unless necessary
6. **Documentation**: Update docs when changing behavior or APIs
7. **Backwards Compatibility**: Maintain compatibility with existing CRDs and APIs

## Common Tasks

### Adding a New Controller
1. Create controller file in `internal/controller/`
2. Add controller setup in `cmd/main.go`
3. Create corresponding test file
4. Update RBAC if needed

### Adding a New CRD Field
1. Update type definition in `api/v1alpha1/`
2. Run `make manifests` to regenerate CRDs
3. Run `make generate` to regenerate DeepCopy methods
4. Update controller logic
5. Add tests
6. Update documentation

### Updating Dependencies
```bash
go get <package>@<version>
go mod tidy
```

### Building Container Images
```bash
make docker-build
make docker-push
```

## Troubleshooting

### Common Issues
- If tests fail with "unable to find kubebuilder assets", run `make setup-envtest`
- If CRDs are out of sync, run `make manifests`
- If deepcopy methods are missing, run `make generate`
- If UI builds fail, check Node.js version and run `npm install` in the UI directory

## Additional Resources

- Main documentation: https://gitops-promoter.readthedocs.io/
- Project repository: https://github.com/argoproj-labs/gitops-promoter
- Related video: "Space Age GitOps: The Rise of the Humble Pull Request"
