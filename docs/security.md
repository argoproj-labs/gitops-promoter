# Security policy

## Supported versions

This project is experimental. We will release security fixes as quickly as possible, but we provide no guarantees on supporting old versions.

## Reporting a vulnerability

**Do not** open public GitHub issues for security vulnerabilities.

Report through [GitHub Security Advisories](https://github.com/argoproj-labs/gitops-promoter/security/advisories/new) for this repository. The maintainers will treat reports as confidential and coordinate a fix and disclosure.

See also the short policy in [`SECURITY.md`](https://github.com/argoproj-labs/gitops-promoter/blob/main/SECURITY.md) at the repository root.

## Dependencies and advisory response

We monitor dependency and supply-chain advisories using **GitHub Dependabot** and **Renovate** (automated update PRs), and we run **CodeQL** static analysis on pull requests. When a **publicly known** vulnerability affects this project's dependencies or our own code at **medium severity or higher**, we aim to ship a fix or mitigation within **60 days** of confirmation. **Critical** issues are prioritized and addressed as quickly as practical.

This policy describes intent; timelines can vary with maintainer capacity and upstream fixes.

## Fuzzing

We use Go’s native fuzzing (`testing.F`). Add `Fuzz…` functions in `*_test.go` next to the code they exercise (same package as the implementation, or an external test package such as `foo_test` for `package foo`).

Checked-in **corpus** files (inputs discovered or minimized earlier) live under each package’s `testdata/fuzz/<FuzzName>/` directory. Extend **`FUZZ_PACKAGES`** in the `Makefile` when you add fuzz targets in a new import path so `make fuzz-replay` and `make fuzz-explore` pick them up.

**Local commands:**

```bash
make fuzz-replay    # replay seeds (`f.Add`) + corpus (`testdata/fuzz`) only (same as pull request CI)
make fuzz-explore   # exploratory run per target; duration is FUZZ_TIME in the Makefile (default 30s)
```

**CI:** pull requests run **`make fuzz-replay`** (regression: seeds + corpus, no `-fuzz`). The scheduled workflow runs **`make fuzz-explore`** (same **`FUZZ_TIME`** default from the Makefile).

**Practice:** bound inputs (`t.Skip` on very large values), register **seeds** with `f.Add` (fixed inputs in source), and assert properties the code is supposed to guarantee. When `go test -fuzz=…` writes a minimized failure, commit it under `testdata/fuzz/<FuzzName>/` (**corpus**: replayed like seeds, but as files). For behavior you also want spelled out in prose, add or extend a [unit test](contributing/index.md#testing-expectations).
