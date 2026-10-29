# Cutting a release

This page describes how to publish a new GitOps Promoter release. The process is mostly automated — you trigger it, review the draft, and publish.

Each minor series has its own `release-X.Y` branch. Minor releases (`X.Y.0`) are cut from `main`; patch releases (`X.Y.Z` with `Z > 0`) are cut from the matching release branch. Having a release branch does not imply any particular support window. See [Backporting fixes](backporting-fixes.md) for getting fixes onto a release branch.

## Prerequisites

- Maintainer access to the repository (merge to `main` and `release-*`).
- Repository secrets `QUAY_USERNAME` and `QUAY_TOKEN` configured for the Quay.io container registry.
- A `release` environment with variable `RELEASE_APP_ID` and secret `RELEASE_APP_PRIVATE_KEY`. The GitHub App needs Contents and Workflows write permission so it can create a `release-*` branch. Leave required reviewers off so branch creation is not paused.

## Steps

### 1. Dispatch the version-bump workflow

Go to the [**Bump Docs Manifests**](https://github.com/argoproj-labs/gitops-promoter/actions/workflows/bump-docs-manifests.yml) workflow, click **Run workflow**, and enter the new version number (e.g. `1.2.3`, without the `v` prefix).

This updates manifest version references in the docs and opens a pull request titled **`docs: bump manifest versions to v1.2.3`**.

The workflow picks the PR's base branch from the version:

- **Minor release** (e.g. `1.3.0`): the PR targets `main`. The workflow rejects the version if it is not newer than the latest release or if `release-1.3` already exists.
- **Patch release** (e.g. `1.2.4`): the PR targets `release-1.2`. The patch number must be greater than the latest `v1.2.Z` tag. If `release-1.2` does not exist yet (for a minor released before release branches were introduced), the workflow creates it from the latest `v1.2.Z` tag and creates the `cherry-pick/1.2` label.

### 2. Merge the version-bump PR

Review and merge the PR to its base branch (`main` or `release-X.Y`). The commit message must contain `docs: bump manifest versions to v<version>` — the automated PR already sets this.

### 3. Automated tag creation and release dispatch

Merging triggers [`create-release-tag.yaml`](https://github.com/argoproj-labs/gitops-promoter/blob/main/.github/workflows/create-release-tag.yaml), which:

1. Extracts the version from the commit message and verifies it matches the branch (minor releases on `main`, patch releases on `release-X.Y`).
2. For a minor release, creates the `release-X.Y` branch at the release commit and the `cherry-pick/X.Y` label.
3. Creates and pushes the git tag (e.g. `v1.2.3`).
4. Dispatches the [`release.yaml`](https://github.com/argoproj-labs/gitops-promoter/blob/main/.github/workflows/release.yaml) workflow **on the tag ref**, so the Sigstore OIDC signing identity is `refs/tags/v1.2.3`.

### 4. GoReleaser builds the release

The release workflow runs [GoReleaser](https://goreleaser.com/), which:

- Builds multi-arch binaries (linux/darwin/windows, amd64/arm64).
- Builds and pushes multi-arch Docker images to `quay.io/argoprojlabs/gitops-promoter:<tag>`.
- Signs container images and checksums with [cosign](https://docs.sigstore.dev/cosign/overview/) (keyless / Sigstore).
- Creates a **draft** GitHub Release with binaries, checksums, install manifest, and the Argo CD extension archive.

### 5. Review and publish the draft release

Go to **Releases** on GitHub. The new release appears as a draft. Review the generated changelog, then click **Publish release**.

> [!NOTE]
> RC / pre-release versions (e.g. `v1.2.3-rc.1`) are **not supported**. The release pipeline only handles stable `vX.Y.Z` versions.

> [!WARNING]
> If immutable releases are enabled on the repository, the release and its tag become permanently locked once published. Make sure everything looks right before publishing.

## Why `workflow_dispatch` instead of a tag trigger?

A simpler design would be: push the tag in the first workflow, and have the release workflow trigger on `push: tags: ['v*']`. The reason we don't do this is that GitHub suppresses most events created by `GITHUB_TOKEN` to prevent recursive workflow loops. A tag pushed by one workflow's `GITHUB_TOKEN` will not trigger a `push: tags` event in another workflow. The only events exempt from this restriction are `workflow_dispatch` and `repository_dispatch`.

We use `workflow_dispatch` (via `gh workflow run release.yaml --ref <tag>`) because it lets us specify the tag as the workflow ref. This means the release workflow runs in the context of `refs/tags/vX.Y.Z`, and the GitHub OIDC token — used by cosign for keyless Sigstore signing — carries that tag as the certificate identity. `repository_dispatch` cannot do this because it always runs on the default branch (`refs/heads/main`), which would produce the wrong signing identity.

## Retrying a failed release

If the release workflow fails partway through, you can re-trigger it without re-creating the tag:

```bash
gh workflow run release.yaml --ref v1.2.3
```

The draft release is mutable until published, so GoReleaser can overwrite partial artifacts.

If the generated changelog for a patch release uses the wrong starting point, set the repository variable `GORELEASER_PREVIOUS_TAG` to the previous release on that line (e.g. `v1.2.3`), re-run the workflow, then clear the variable.

## `latest` images

The [`release-latest.yaml`](https://github.com/argoproj-labs/gitops-promoter/blob/main/.github/workflows/release-latest.yaml) workflow runs on every push to `main` (independently of the release process) and pushes `quay.io/argoprojlabs/gitops-promoter:latest`. These images are signed with a certificate identity of `refs/heads/main`. Release branches never publish `latest`.

## Verifying a release

See the verification instructions in the footer of each GitHub Release. In short:

```bash
cosign verify \
  --certificate-identity 'https://github.com/argoproj-labs/gitops-promoter/.github/workflows/release.yaml@refs/tags/v1.2.3' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  quay.io/argoprojlabs/gitops-promoter:v1.2.3
```
