# Security

## Reporting a vulnerability

**Do not** open public GitHub issues for security vulnerabilities.

Report through [GitHub Security Advisories](https://github.com/argoproj-labs/gitops-promoter/security/advisories/new) for this repository. The maintainers will treat reports as confidential and coordinate a fix and disclosure.

See also the short policy in [`SECURITY.md`](https://github.com/argoproj-labs/gitops-promoter/blob/main/SECURITY.md) at the repository root.

## Fuzzing

We use Go’s native fuzzing (`testing.F`). Add `Fuzz…` functions in `*_test.go` next to the code they exercise (same package as the implementation, or an external test package such as `foo_test` for `package foo`).

Checked-in fuzz inputs live under each package’s `testdata/fuzz/<FuzzName>/` directory. Extend **`FUZZ_PACKAGES`** in the `Makefile` when you add fuzz targets in a new import path so `make test-fuzz-seed` and `make test-fuzz` pick them up.

**Local commands:**

```bash
make test-fuzz-seed          # replay seeds + corpus (same as PR CI)
make test-fuzz FUZZ_TIME=60s # exploratory run per target
```

**CI:** pull requests run `make test-fuzz-seed`. A scheduled workflow runs bounded `make test-fuzz`.

**Practice:** bound inputs (`t.Skip` on very large values), use `f.Add` for hand-picked cases, and assert properties the code is supposed to guarantee. When `go test -fuzz=…` writes a minimized failure, commit it under `testdata/fuzz/<FuzzName>/`. For behavior you also want spelled out in prose, add or extend a [unit test](contributing.md#testing-expectations).
