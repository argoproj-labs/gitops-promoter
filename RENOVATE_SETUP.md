# Renovate Setup Instructions

This repository uses Renovate to automatically update Go and golangci-lint versions. This document describes the setup and any manual steps required.

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

## Required Setup Steps

### 1. Install Renovate Bot

You need to install the Renovate GitHub App or set up self-hosted Renovate.

#### Option A: GitHub App (Recommended for most teams)

1. Go to https://github.com/apps/renovate
2. Click "Install" or "Configure"
3. Select the `argoproj-labs` organization
4. Choose either:
   - **All repositories** (if you want Renovate for all repos), or
   - **Only select repositories** and choose `gitops-promoter`
5. Click "Install" or "Save"

#### Option B: Self-Hosted Renovate

If you prefer self-hosted Renovate:

1. Follow the [Renovate self-hosting documentation](https://docs.renovatebot.com/getting-started/running/)
2. Configure Renovate to run on a schedule (e.g., weekly)
3. Ensure Renovate has access to create branches and PRs in this repository

### 2. Verify Configuration

After installation, Renovate should:
- Create an initial "Configure Renovate" PR to activate itself
- Start scanning for updates on the configured schedule (every weekend)
- Create PRs for Go and golangci-lint updates when available

## How It Works

### Update Detection

Renovate uses regex managers to detect version strings in various files:

- **Go version** in `go.mod`: `go 1.24.0`
- **Go version** in workflows: `go-version: "1.24"`
- **Go version** in Dockerfiles: `FROM golang:1.25`
- **golangci-lint** in Makefile: `GOLANGCI_LINT_VERSION ?= v2.5.0`
- **golangci-lint** in workflows: `version: v2.5.0`

### Automated Fixes

When Renovate creates a PR for Go or golangci-lint updates, it will:

1. Update all version references across the repository
2. Run `go mod tidy` to update dependencies
3. Run `make lint-fix` to automatically fix linting issues

The `lint-fix` command is run with `|| true` to allow the PR to be created even if there are linting errors that can't be auto-fixed.

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

### Renovate Configuration (`renovate.json5`)

Key configuration options:

- **Schedule**: Updates are checked every weekend to minimize disruption
- **Labels**: PRs are labeled with `dependencies`
- **Concurrent Limit**: Maximum of 3 PRs at once
- **Automerge**: Disabled (requires manual review)
- **Post-upgrade tasks**: Runs `go mod tidy` and `make lint-fix`

### Dependabot Configuration (`.github/dependabot.yaml`)

Dependabot continues to handle:
- ✅ Go module dependencies (via `gomod` package ecosystem)
- ✅ GitHub Actions updates (via `github-actions` package ecosystem)

Dependabot does NOT handle:
- ❌ Go version updates (handled by Renovate)
- ❌ golangci-lint version updates (handled by Renovate)

This avoids conflicts between the two tools.

## Troubleshooting

### Renovate isn't creating PRs

1. Check if Renovate is installed and has repository access
2. Check the Renovate dashboard: https://app.renovatebot.com/dashboard
3. Look for Renovate's debug logs in the repository's "Issues" or "Pull Requests" tabs
4. Verify the `renovate.json5` configuration is valid JSON5

### PRs are created but lint-fix doesn't run

1. Check if `make lint-fix` works locally
2. Verify Renovate has sufficient permissions to push to branches
3. Check the Renovate logs for postUpgradeTasks execution errors

### Manual fixes are needed for every update

This is expected behavior! The goal is to:
- ✅ Automate version updates and easy fixes
- ✅ Create PRs so humans can see what needs fixing
- ✅ Reduce toil, not eliminate human review

## Testing Renovate Locally

You can test Renovate configuration changes locally:

```bash
# Install Renovate CLI
npm install -g renovate

# Run Renovate in dry-run mode
export RENOVATE_TOKEN="your-github-token"
renovate --dry-run argoproj-labs/gitops-promoter
```

## References

- [Renovate Documentation](https://docs.renovatebot.com/)
- [Renovate Configuration Options](https://docs.renovatebot.com/configuration-options/)
- [Regex Managers](https://docs.renovatebot.com/modules/manager/regex/)
- [Post-upgrade Tasks](https://docs.renovatebot.com/configuration-options/#postupgradetasks)
