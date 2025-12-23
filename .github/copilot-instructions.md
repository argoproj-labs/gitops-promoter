# GitOps Promoter - GitHub Copilot Instructions

## Project Overview

GitOps Promoter is a Kubernetes operator that facilitates environment promotion for config managed via GitOps. It provides a drift-free promotion process with a robust gating system, complete integration with git and SCM tooling.

### Key Technologies
- **Backend**: Go 1.25+ (Kubernetes operator using controller-runtime)
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

## Pull Request Titles

All pull request titles should follow the [Conventional Commits](https://www.conventionalcommits.org/) specification. Use one of the following prefixes:

- `feat:` - New features or enhancements
- `fix:` - Bug fixes
- `docs:` - Documentation only changes
- `chore:` - Maintenance tasks, dependency updates, or other non-feature/non-fix changes
- `test:` - Adding or updating tests
- `refactor:` - Code changes that neither fix bugs nor add features
- `perf:` - Performance improvements
- `ci:` - Changes to CI/CD configuration
- `build:` - Changes to build system or external dependencies
- `style:` - Code style changes (formatting, missing semicolons, etc.)

### Examples
- `feat: add support for multiple environments in promotion strategy`
- `fix: resolve race condition in pull request merge`
- `docs: update getting started guide with new configuration options`
- `chore: update golang dependencies`
- `test: add unit tests for git operations`

### Scopes (Optional)
You may optionally include a scope in parentheses after the type:
- `feat(api): add new CRD for deployment tracking`
- `fix(controller): handle nil pointer in reconciliation loop`
- `docs(architecture): clarify SCM integration patterns`

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

# Setting up MkDocs and Python Virtual Environment

To build and serve documentation locally, set up a Python virtual environment at the repository root:

```fish
python3 -m venv .venv
source .venv/bin/activate.fish
pip install -r docs/requirements.txt
mkdocs serve
```

- This will install all MkDocs dependencies, including plugins for GitHub-style alerts.

## Verifying Documentation Linting

To check that your documentation changes do not introduce any MkDocs warnings, run:

```bash
make lint-docs
```

This will build the documentation and fail if any warnings are present. The full MkDocs output will be shown if there are issues, making it easy to debug. This check is also run automatically in CI for every pull request.

Make sure you have activated your Python virtual environment and installed dependencies as described above before running this command.

## Additional Resources

- Main documentation: https://gitops-promoter.readthedocs.io/
- Project repository: https://github.com/argoproj-labs/gitops-promoter
- Related video: "Space Age GitOps: The Rise of the Humble Pull Request"

---

# AI Agent Protocol: Go Test Debugging

## Critical Rule: Always Capture stderr

**Controller logs go to stderr, not stdout.** Always use `2>&1` in test commands.

```bash
# ✅ CORRECT - Captures both stdout and stderr
go test -v ./pkg -ginkgo.focus="test" -ginkgo.v 2>&1 > test.log

# ❌ WRONG - Misses controller logs
go test -v ./pkg -ginkgo.focus="test" > test.log
```

## Standard Test Execution Pattern

### Initial Run
```bash
go test -v ./package -ginkgo.focus="test name" -ginkgo.v -timeout 5m 2>&1 > /tmp/test-output.log
```

### Validate Capture Success
```bash
wc -l /tmp/test-output.log
# Expect: 500+ lines for detailed logs
# If < 50 lines: Missing -ginkgo.v or logs weren't generated
```

### Search for Expected Behavior
```bash
grep "expected log message" /tmp/test-output.log | wc -l
# Returns: count of matches (0 = code path didn't execute)
```

## Grep Exit Code Interpretation

```
Exit 0: Pattern found (matches exist)
Exit 1: Pattern not found (NOT an error, just no matches)
Exit 2: Grep command error (syntax issue)
```

**Protocol**: When grep returns exit 1, this means "not found" - it is EXPECTED when searching for something that doesn't exist.

## Search Patterns for Log Validation

### Check if Reconciliation Occurred
```bash
grep -c "Reconciling ChangeTransferPolicy" /tmp/test.log
```

### Check if Specific Code Path Executed
```bash
# Function entry point log message
grep "Testing for conflicts between branches" /tmp/test.log | wc -l

# Function behavior log message  
grep "Conflicts detected, performing merge with 'ours' strategy" /tmp/test.log | wc -l
```

### Extract Key State Information
```bash
# Branch SHAs
grep "branchShas" /tmp/test.log

# Pull request states
grep "PullRequest.*state" /tmp/test.log
```

## Real-Time Filtering (No File Save)

When you need immediate feedback without saving full logs:

```bash
go test -v ./pkg -ginkgo.focus="test" 2>&1 | grep -E "ERROR|FAIL|specific-pattern"
```

**Use when**: Quick validation, looking for specific events  
**Don't use when**: Need to search multiple patterns or examine full context

## Running Multiple Iterations

### Stop on First Failure
```bash
for i in {1..5}; do 
    echo "=== Run $i ==="
    go test ./pkg -ginkgo.focus="test" -timeout 5m || exit 1
done
```

### Collect All Results
```bash
for i in {1..10}; do
    go test ./pkg -ginkgo.focus="test" -timeout 2m 2>&1 | tail -1 >> /tmp/results.txt
done
cat /tmp/results.txt
```

## Decision Tree: When to Save vs Stream

### Save to File First (Recommended for Investigation)
- Need to search multiple patterns
- Need context around matches (use grep -C)
- Output is large (>1000 lines)
- Will analyze multiple aspects of same run

### Stream/Filter Live  
- Only need one specific pattern
- Quick validation ("did X happen?")
- Output is manageable
- Know exactly what you're looking for

## Common Investigation Scenarios

### Scenario 1: Test Passes, Need to Verify Code Path Executed
```bash
# Step 1: Capture full run
go test -v ./pkg -ginkgo.focus="test" -ginkgo.v -timeout 5m 2>&1 > /tmp/test.log

# Step 2: Search for expected log message
grep "expected behavior log" /tmp/test.log | wc -l

# Step 3: If 0, code path didn't execute - test may be false positive
```

### Scenario 2: Test Fails, Need Root Cause
```bash
# Step 1: Run and capture
go test -v ./pkg -ginkgo.focus="test" -ginkgo.v -timeout 5m 2>&1 > /tmp/fail.log

# Step 2: Find failure point
grep -A20 "FAILED" /tmp/fail.log

# Step 3: Check what was happening before failure
grep -B20 "FAILED" /tmp/fail.log
```

### Scenario 3: Test is Flaky
```bash
# Step 1: Run multiple times quickly
for i in {1..5}; do go test ./pkg -ginkgo.focus="test" || break; done

# Step 2: If it failed, capture detailed run
go test -v ./pkg -ginkgo.focus="test" -ginkgo.v 2>&1 > /tmp/flaky.log

# Step 3: Search for timing indicators
grep -E "timeout|reconcile.*duration" /tmp/flaky.log
```

## grep Pattern Optimization

### Instead of This (Can Miss Matches)
```bash
grep "exact string with spaces" test.log
```

### Do This (More Reliable)
```bash
# Use -E for extended regex
grep -E "pattern1|pattern2" test.log

# Case-insensitive when appropriate
grep -i "error" test.log
```

## File Management Protocol

### Use /tmp for Test Output
```bash
# ✅ Do this
go test ... 2>&1 > /tmp/test-output.log

# ❌ Don't do this (clutters repo)
go test ... 2>&1 > test-output.log
```

## Validation Checklist

Before concluding a test investigation, verify:

- [ ] Captured logs to file using `2>&1`
- [ ] Confirmed log file has substantial content (`wc -l` > 100)
- [ ] Searched for expected log messages from code being tested
- [ ] Checked if expected code paths executed (found their log messages)
- [ ] Verified test result (PASS/FAIL) at end of log
- [ ] If test passed, confirmed it's not a false positive (code actually ran)

## Quick Reference: Essential Commands

```bash
# Capture everything
go test -v ./pkg -ginkgo.focus="test" -ginkgo.v -timeout 5m 2>&1 > /tmp/out.log

# Validate capture
wc -l /tmp/out.log

# Search (count matches)
grep "pattern" /tmp/out.log | wc -l

# Search with context
grep -C10 "pattern" /tmp/out.log

# Check test result
tail -5 /tmp/out.log
```

## Protocol for Unknown Test Failures

1. **Capture**: `go test -v ./pkg -ginkgo.v 2>&1 > /tmp/test.log`
2. **Validate**: `wc -l /tmp/test.log` (ensure logs captured)
3. **Find failure**: `grep -A10 "FAILED" /tmp/test.log`
4. **Check errors**: `grep -i error /tmp/test.log`
5. **Timeline**: `grep -E "Reconciling.*End" /tmp/test.log | tail -20`

## Remember

- **2>&1 is non-negotiable** for controller tests
- **-ginkgo.v is required** for detailed logs
- **wc -l before grep** to ensure logs exist
- **grep exit 1 is normal** when pattern not found
- **Save to /tmp** to avoid repo clutter
- **Always validate capture** before concluding "no matches"
