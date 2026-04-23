# `promoter templates` examples

Ready-to-run fixture directories for the `promoter templates` subcommands. Each example has its own
`README.md` explaining what it demonstrates and the exact command to run.

| Example | Subcommand | Demonstrates |
|---|---|---|
| [pullrequest-basic](./pullrequest-basic) | `pullrequest` | Minimal PR title/description rendering with a `ChangeTransferPolicy` and `PromotionStrategy`. |
| [webrequest-approval](./webrequest-approval) | `webrequest` | External approval workflow using `mode.context: environments` and `trigger` mode with `response.output`. |
| [webrequest-shared-gate](./webrequest-shared-gate) | `webrequest` | Shared deployment gate using `mode.context: promotionstrategy` with per-branch phases returned from `success.when`. |
| [webrequest-change-management](./webrequest-change-management) | `webrequest` | Real-world production CM-record workflow: `when.variables` + `when.output` + `response.output` + `success.when.variables`, fingerprint-based carry-forward, complex body template with Markdown description. |
| [webrequest-change-management-approval](./webrequest-change-management-approval) | `webrequest` | Trigger-mode approval workflow with time-window checks (`shouldTriggerByTime`, `date(start) <= now() <= date(end)`). Demonstrates the status-seed mechanism for simulating warm reconciles and cooldown-elapsed scenarios without any time override. |

Run any example from the repo root, e.g.:

```bash
go run ./cmd templates webrequest \
  --web-request cmd/templates/examples/webrequest-approval/wrcs.yaml \
  --promotion-strategy cmd/templates/examples/webrequest-approval/ps.yaml \
  --namespace-labels cmd/templates/examples/webrequest-approval/namespace-labels.yaml \
  --response cmd/templates/examples/webrequest-approval/response.yaml
```

Each example's `README.md` has the exact command.
