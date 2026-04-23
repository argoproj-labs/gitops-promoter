# Example: webrequest-approval

External-approval workflow using `mode.context: environments` and `mode.trigger`. Each environment
hits an approval service, reads `approved` / `sha` out of the JSON response, and marks the commit
status success only when approved.

This is the canonical WebRequestCommitStatus shape for an "external gate" — a useful starting point
for any approval / dashboard / ticketing integration.

## What the simulation shows

1. **reconcile** — fresh resource. The trigger expression `Phase != "success"` is true, so the
   mock approval body `{ approved: true, sha: d3adb33f }` is injected:
   - URL / body / headers templates are rendered
   - `response.output` extracts `{ approved, sha }` into `ResponseOutput`
   - `success.when` flips to `Phase=success`
   - CommitStatus description becomes `"approved (d3adb33f)"`
2. **next-reconcile** — `Response=nil` because the trigger is now false (`Phase` is success).
   `ResponseOutput` is carried forward from reconcile, and
   `Phase=success` is preserved by the carry-forward branch of `success.when`.

Flip `approved: false` in `response.yaml` to see the failure-path carry-forward behavior.

## Run

From the repo root:

```bash
go run ./cmd templates webrequest \
  --web-request         cmd/templates/examples/webrequest-approval/wrcs.yaml \
  --promotion-strategy  cmd/templates/examples/webrequest-approval/ps.yaml \
  --namespace-labels    cmd/templates/examples/webrequest-approval/namespace-labels.yaml \
  --response            cmd/templates/examples/webrequest-approval/response.yaml
```

Try `--branch environment/production` to restrict to a single environment. Try `--output yaml` for
structured output suitable for diffs.
