# BlackShield Security Audit Report — `argoproj-labs/gitops-promoter`

- **Scanned Commit:** `b100d5c74619dc20ca1bf7291ad455d61430a8ec`
- **Actionable Findings:** `6` (`1 Critical`, `5 High`, `0 Medium`, `3 Low`)
- **BlackShield Platform & Continuous Zero-Cost PR Protection:** [https://blackshield.chaplau.com](https://blackshield.chaplau.com)

## Detailed Findings & Remediation Guide

### 1. [CRITICAL] `private-key` — Private Cryptographic Key Detected

- **Category:** `secret`
- **Location:** `RENOVATE_SETUP.md:74`
- **Details:** Potential hardcoded credential or secret detected in `RENOVATE_SETUP.md:74` (rule `private-key`). Raw value redacted (`***REDACTED***`) for safe disclosure.
- **Recommended Fix:** Revoke leaked credential immediately and rotate in secret manager.

### 2. [HIGH] `ci-github-script-injection-pr` — ci-github-script-injection-pr: GitHub Actions Script Injection via PR Title

- **Category:** `sast`
- **Location:** `.github/workflows/cherry-pick.yml:65`
- **Details:** CI/CD workflow or Dockerfile security vulnerability. at .github/workflows/cherry-pick.yml:65
- **Recommended Fix:** Pass untrusted GitHub context values via workflow environment variables instead of inline ${{ ... }} shell interpolation.

### 3. [HIGH] `CKV_DOCKER_1` — CKV_DOCKER_1: Container Runs as Root (Missing Non-Root USER)

- **Category:** `misconfiguration`
- **Location:** `.oss-scanner/Dockerfile:1`
- **Details:** Container does not specify a non-root USER, allowing processes to execute with root privileges.
- **Recommended Fix:** Add 'USER <non-root-uid>' instruction before container entrypoint.

### 4. [HIGH] `CVE-2022-25883` — CVE-2022-25883 in semver@6.3.1

- **Category:** `vulnerability`
- **Location:** `ui/dashboard/package-lock.json`
- **Affected Package:** `semver@6.3.1`
- **Details:** Regular Expression Denial of Service (ReDoS) in semver range parser.
- **Recommended Fix:** Upgrade `semver` to `7.5.2` or newer

### 5. [HIGH] `CVE-2022-25883` — CVE-2022-25883 in semver@6.3.1

- **Category:** `vulnerability`
- **Location:** `ui/extension/package-lock.json`
- **Affected Package:** `semver@6.3.1`
- **Details:** Regular Expression Denial of Service (ReDoS) in semver range parser.
- **Recommended Fix:** Upgrade `semver` to `7.5.2` or newer

### 6. [HIGH] `CVE-2022-25883` — CVE-2022-25883 in semver@6.3.1

- **Category:** `vulnerability`
- **Location:** `ui/storybook/package-lock.json`
- **Affected Package:** `semver@6.3.1`
- **Details:** Regular Expression Denial of Service (ReDoS) in semver range parser.
- **Recommended Fix:** Upgrade `semver` to `7.5.2` or newer

