# GitOps Promoter - GitHub Copilot Instructions

## Project Overview

GitOps Promoter is a Kubernetes operator that facilitates environment promotion for config managed via GitOps. It provides a drift-free promotion process with a robust gating system, complete integration with git and SCM tooling.

### Key Technologies
- **Backend**: Go 1.24+ (Kubernetes operator using controller-runtime)
- **Frontend**: TypeScript/React (Dashboard UI and Argo CD extension)
- **Infrastructure**: Kubernetes, Argo CD integration
- **SCM Support**: GitHub, GitHub Enterprise, GitLab, Forgejo (including Codeberg)

## Architecture

The project follows a standard Kubernetes operator pattern with:
- Custom Resource Definitions (CRDs) in `api/v1alpha1/`
- Controllers in `internal/controller/`
- SCM integrations in `internal/scms/`
- Webhook receiver for SCM events in `internal/webhookreceiver/`
- Dashboard UI in `ui/dashboard/`
- Argo CD extension in `ui/extension/`
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
make build-extension   # Build Argo CD extension
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
KUBEBUILDER_ASSETS="<fullpath>/bin/k8s/1.31.0-darwin-arm64" ./bin/ginkgo-v2.26.0 -v --focus "TimedCommitStatus Controller" internal/controller/ # To run a specific focused task replace the --focus flag with the correct test you want
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
4. **ClusterScmProvider** - Cluster-scoped SCM provider configuration
5. **PullRequest** - Represents a promotion pull request
6. **CommitStatus** - Commit status tracking
7. **ArgoCDCommitStatus** - Argo CD-specific commit status
8. **ChangeTransferPolicy** - Controls how changes are transferred between branches
9. **RevertCommit** - Represents a revert operation
10. **ControllerConfiguration** - Configuration for the GitOps Promoter controller

### Resource Relationships
- PromotionStrategy references GitRepository
- GitRepository references ScmProvider or ClusterScmProvider
- ScmProvider references a Secret for credentials
- ArgoCDCommitStatus references PromotionStrategy

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
- When adding a new documentation page, update `mkdocs.yml` to include it in the table of contents

## Best Practices

1. **Minimal Changes**: Make the smallest possible changes to achieve the goal
2. **Test First**: Write tests before implementing features
3. **Error Handling**: Always handle errors explicitly, don't ignore them
4. **Logging**: Use structured logging with appropriate levels
5. **Dependencies**: Avoid adding new dependencies unless necessary
6. **Documentation**: Update docs when changing behavior or APIs
7. **Backwards Compatibility**: While in v1alpha1, breaking changes are allowed but should be avoided if possible

## Common Tasks

### Adding a New CRD Field
1. Update type definition in `api/v1alpha1/`
2. Add proper Kubernetes validation:
   - For required strings, set a min length of at least 1
   - Set a max length if it makes sense for the field
   - Use regex validation for character restrictions
   - Use metav1 types for time and duration fields instead of custom types
   - For numbers where negative values don't make sense, enforce a minimum of zero
3. Update example files in `internal/controller/testdata/` for both spec and status changes
4. Run `make build-installer` to regenerate CRDs, DeepCopy methods, and install manifests
5. Update controller logic
6. Add tests
7. Update documentation

### Adding a Field to ControllerConfiguration
1. Update type definition in `api/v1alpha1/controllerconfiguration_types.go`
2. Set a default value in the code
3. Explicitly add the default value to `config/config/controllerconfiguration.yaml`
4. For this CR, defaults live in manifests and not in code whenever possible
5. Follow the same validation and testing steps as adding a CRD field

### Building Container Images
```bash
make docker-build
# Note: docker-push should only be used for releases, not automation
```

## Troubleshooting

### Common Issues
- If tests fail with "unable to find kubebuilder assets", run `make setup-envtest`
- If CRDs are out of sync, run `make manifests`
- If deepcopy methods are missing, run `make generate`
- If UI builds fail, check Node.js version and run `npm install` in the UI directory

## Test Debugging Protocol

### Critical: Always Capture stderr for Controller Logs

**Controller logs go to stderr.** Always use `2>&1` in test commands:

```bash
# ✅ CORRECT - Captures both stdout and stderr
go test -v ./pkg -ginkgo.focus="test" -ginkgo.v 2>&1 > /tmp/test.log

# ❌ WRONG - Misses controller logs
go test -v ./pkg > test.log
```

### Standard Test Investigation Pattern

1. **Capture everything**:
   ```bash
   go test -v ./package -ginkgo.focus="test name" -ginkgo.v -timeout 5m 2>&1 > /tmp/test.log
   ```

2. **Validate capture**:
   ```bash
   wc -l /tmp/test.log  # Should be 500+ lines, not ~20
   ```

3. **Search for expected behavior**:
   ```bash
   grep "expected log message" /tmp/test.log | wc -l
   # Returns count (0 = code path didn't execute)
   ```

### Grep Exit Codes

- Exit 0: Pattern found
- Exit 1: Pattern not found (NOT an error!)
- Exit 2: Grep syntax error

**Important**: `grep ... | wc -l` always succeeds even if grep found nothing.

### Validating Tests Actually Test What They Claim

Always verify expected code paths executed:

```bash
# Search for key log messages that prove the code ran
grep "Testing for conflicts between branches" /tmp/test.log | wc -l
grep "Conflicts detected, performing merge with 'ours' strategy" /tmp/test.log
```

**Red flag**: Test passes but expected log messages missing = false positive.

### Quick Investigation Commands

```bash
# Capture and validate in one step
go test -v ./pkg -ginkgo.v 2>&1 > /tmp/out.log && wc -l /tmp/out.log

# Find why test failed
grep -A20 "FAILED" /tmp/out.log

# Check if specific code executed
grep -c "key log message" /tmp/out.log

# See test result
tail -5 /tmp/out.log
```

### Running Multiple Iterations

```bash
# Run N times, stop on first failure
for i in {1..5}; do 
    echo "=== Run $i ==="
    go test ./pkg -ginkgo.focus="test" -timeout 5m || exit 1
done
```

### Using autoMerge for Deterministic Tests

The `autoMerge` field (on `ChangeTransferPolicy.spec.autoMerge` and `Environment.autoMerge`) prevents PRs from auto-merging:

```go
// Disable auto-merge during test setup
promotionStrategy.Spec.Environments[1].AutoMerge = ptr.To(false)

// Assert on PR states (PRs stay open)

// Re-enable to allow completion
ps.Spec.Environments[1].AutoMerge = ptr.To(true)
k8sClient.Update(ctx, &ps)
```

**Use for**: Preventing timing races in tests without needing commit status checks.

### Checklist Before Concluding Investigation

- [ ] Used `2>&1` to capture stderr
- [ ] Used `-ginkgo.v` for detailed output
- [ ] Validated log file has substantial content (`wc -l`)
- [ ] Searched for expected log messages from code being tested
- [ ] Verified test result (PASS/FAIL) in output
- [ ] If test passed, confirmed expected code paths executed (not false positive)

## Additional Resources

- Main documentation: https://gitops-promoter.readthedocs.io/
- Project repository: https://github.com/argoproj-labs/gitops-promoter
- Related video: "Space Age GitOps: The Rise of the Humble Pull Request"
- Test debugging protocol: `.ai/go-test-debugging-protocol.md`
