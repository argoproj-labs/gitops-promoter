# Renovate Setup Instructions

This repository uses **self-hosted Renovate via GitHub Workflows** to automatically update Go and golangci-lint versions. This document describes the setup and any manual steps required.

## Overview

Renovate has been configured to:
1. **Automatically detect and update Go versions** across:
   - `go.mod` (go directive)
   - GitHub Actions workflows (`.github/workflows/*.yaml`)
   - Dockerfiles (`Dockerfile`, `release.Dockerfile`)

2. **Automatically detect and update golangci-lint versions** in:
   - `Makefile` (`GOLANGCI_LINT_VERSION`)
   - GitHub Actions workflows (`.github/workflows/ci.yaml`)

3. **Automatically run `golangci-lint --fix`** after version updates to address auto-fixable linting issues

4. **Group Go and golangci-lint updates together** in a single PR when both have updates

## Why Self-Hosted Renovate?

We use self-hosted Renovate (via GitHub Workflows) instead of the hosted GitHub App because:
- **Post-upgrade tasks are only supported in self-hosted mode** - We need to run `go mod tidy` and `make lint-fix` automatically
- Full control over when and how Renovate runs
- No external dependencies on Renovate's hosted infrastructure

## Required Setup Steps

### 1. Create a GitHub App for Renovate

To run Renovate as a GitHub App with proper permissions, you need to create a dedicated GitHub App:

1. **Create the GitHub App**:
   - Go to your organization settings: https://github.com/organizations/argoproj-labs/settings/apps
   - Click "New GitHub App"
   - Fill in the details:
     - **GitHub App name**: `renovate-bot` (or similar)
     - **Homepage URL**: `https://github.com/argoproj-labs/gitops-promoter`
     - **Webhook**: Uncheck "Active" (we don't need webhooks)
   
2. **Set Repository Permissions**:
   - **Contents**: Read & Write (to push commits)
   - **Pull Requests**: Read & Write (to create/update PRs)
   - **Issues**: Read & Write (for onboarding PR)
   - **Metadata**: Read-only (required)
   - **Workflows**: Read & Write (to update workflow files if needed)

3. **Install the App**:
   - After creating, click "Install App"
   - Select the `argoproj-labs` organization
   - Choose "Only select repositories" and select `gitops-promoter`
   - Click "Install"

4. **Generate a Private Key**:
   - In the app settings, scroll to "Private keys"
   - Click "Generate a private key"
   - Save the downloaded `.pem` file securely

### 2. Configure Repository Secrets

Add the following secrets to the repository:

1. Go to repository settings: https://github.com/argoproj-labs/gitops-promoter/settings/secrets/actions
2. Click "New repository secret" and add:
   - **Name**: `RENOVATE_APP_ID`
   - **Value**: The App ID from your GitHub App (found in app settings)
3. Add another secret:
   - **Name**: `RENOVATE_APP_PRIVATE_KEY`
   - **Value**: The entire contents of the `.pem` file (including `-----BEGIN RSA PRIVATE KEY-----` and `-----END RSA PRIVATE KEY-----`)

### 3. Verify Configuration

After setup, the Renovate workflow will:
- Run automatically every weekend (Saturday and Sunday at 3 AM UTC)
- Can be triggered manually via "Actions" → "Renovate" → "Run workflow"
- Create an initial "Configure Renovate" PR on first run
- Create PRs for Go and golangci-lint updates when available

## How It Works

### Workflow Execution

The Renovate workflow (`.github/workflows/renovate.yaml`) runs:
- **Automatically**: Every weekend (Saturday and Sunday) at 3 AM UTC
- **Manually**: Via "Actions" → "Renovate" → "Run workflow" in GitHub UI
- **On config changes**: When the workflow or renovate.json5 files are updated

The workflow uses a GitHub App token for authentication, which provides better rate limits and permissions than a PAT.

### Update Detection

Renovate uses regex managers to detect version strings in various files:

- **Go version** in `go.mod`: `go 1.25.0`
- **Go version** in workflows: `go-version: "1.25"`
- **Go version** in Dockerfiles: `FROM golang:1.25`
- **golangci-lint** in Makefile: `GOLANGCI_LINT_VERSION ?= v2.5.0`
- **golangci-lint** in workflows: `version: v2.5.0`

### Automated Fixes

When Renovate creates a PR for Go or golangci-lint updates, it will:

1. Update all version references across the repository
2. Run `go mod tidy` to update dependencies
3. Run `make lint-fix` to automatically fix linting issues

The `lint-fix` command is run with `|| true` to allow the PR to be created even if there are linting errors that can't be auto-fixed.

**Note**: Post-upgrade tasks only work with self-hosted Renovate. The hosted GitHub App does not support this feature.

### Manual Intervention

If the automated `lint-fix` doesn't resolve all issues, the PR will still be created with:
- ✅ All version numbers updated
- ✅ Auto-fixable linting issues resolved
- ❌ Non-auto-fixable linting issues (visible in CI)

**You will need to:**
1. Review the CI failures in the Renovate PR
2. Make manual code changes to fix remaining linting errors
3. Push commits to the Renovate branch
4. Merge the PR once CI passes

This is the same workflow as PR #609, but with much less manual work upfront.

## Configuration Details

### Renovate Workflow (`.github/workflows/renovate.yaml`)

Key workflow features:

- **Schedule**: Runs every weekend (Saturday and Sunday at 3 AM UTC)
- **Manual trigger**: Can be run on-demand with optional dry-run mode
- **Authentication**: Uses GitHub App token (better than PAT)
- **Permissions**: Minimal permissions needed (contents, PRs, issues)
- **Environment variables**: 
  - `RENOVATE_ALLOWED_POST_UPGRADE_COMMANDS`: Whitelist of commands that can run
  - `RENOVATE_DRY_RUN`: Test mode without making changes
  - `LOG_LEVEL`: Configurable logging (default: info)

### Renovate Configuration (`renovate.json5`)

Key configuration options:

- **Schedule**: Updates are checked every weekend to minimize disruption
- **Labels**: PRs are labeled with `dependencies`
- **Concurrent Limit**: Maximum of 3 PRs at once
- **Automerge**: Disabled (requires manual review)
- **Post-upgrade tasks**: Runs `go mod tidy` and `make lint-fix`
  - These only work because we're using self-hosted Renovate
  - Commands must be whitelisted in the workflow

### Dependabot Configuration (`.github/dependabot.yaml`)

Dependabot continues to handle:
- ✅ Go module dependencies (via `gomod` package ecosystem)
- ✅ GitHub Actions updates (via `github-actions` package ecosystem)

Dependabot does NOT handle:
- ❌ Go version updates (handled by Renovate)
- ❌ golangci-lint version updates (handled by Renovate)

This avoids conflicts between the two tools.

## Troubleshooting

### Renovate workflow fails with authentication error

1. Verify the GitHub App is installed on the repository
2. Check that `RENOVATE_APP_ID` and `RENOVATE_APP_PRIVATE_KEY` secrets are set correctly
3. Ensure the private key includes the full PEM format (including headers)
4. Verify the GitHub App has the required permissions (Contents, PRs, Issues)

### Renovate isn't creating PRs

1. Check the workflow run logs in "Actions" → "Renovate"
2. Try running manually with debug logging: Set logLevel to "debug"
3. Verify the `renovate.json5` configuration is valid JSON5
4. Check if there are actually new versions available

### PRs are created but post-upgrade tasks don't run

1. Check if `make lint-fix` works locally
2. Verify commands are whitelisted in `RENOVATE_ALLOWED_POST_UPGRADE_COMMANDS`
3. Check the Renovate workflow logs for postUpgradeTasks execution errors
4. Ensure the workflow has write permissions for contents

### Manual fixes are needed for every update

This is expected behavior! The goal is to:
- ✅ Automate version updates and easy fixes
- ✅ Create PRs so humans can see what needs fixing
- ✅ Reduce toil, not eliminate human review

## Testing Renovate Locally

You can test the Renovate configuration locally before running it in the workflow:

```bash
# Install Renovate CLI
npm install -g renovate

# Run Renovate in dry-run mode (requires GitHub token with repo permissions)
export RENOVATE_TOKEN="your-github-personal-access-token"
renovate --dry-run=full argoproj-labs/gitops-promoter
```

### Testing the Workflow

To test the workflow without making real changes:

1. Go to "Actions" → "Renovate" → "Run workflow"
2. Set "Dry-Run" to `true`
3. Set "Log-Level" to `debug`
4. Click "Run workflow"
5. Check the logs to see what Renovate would do

## Security Considerations

### GitHub App Private Key

- The private key should be stored **only** in GitHub Secrets
- Never commit the `.pem` file to the repository
- Rotate the key if it's ever exposed
- The key gives write access to the repository, so protect it carefully

### Command Whitelisting

The workflow only allows specific commands in `RENOVATE_ALLOWED_POST_UPGRADE_COMMANDS`:
- `go mod tidy` - Safe, only updates dependency checksums
- `make lint-fix` - Runs golangci-lint with auto-fix

If you need to add more commands, update both:
1. The workflow file (`.github/workflows/renovate.yaml`)
2. The renovate.json5 configuration

## References

- [Renovate Documentation](https://docs.renovatebot.com/)
- [Running Renovate as a GitHub App](https://docs.renovatebot.com/modules/platform/github/#running-as-a-github-app)
- [Renovate Configuration Options](https://docs.renovatebot.com/configuration-options/)
- [Regex Managers](https://docs.renovatebot.com/modules/manager/regex/)
- [Post-upgrade Tasks](https://docs.renovatebot.com/configuration-options/#postupgradetasks)
- [Self-Hosted Renovate](https://docs.renovatebot.com/getting-started/running/)
