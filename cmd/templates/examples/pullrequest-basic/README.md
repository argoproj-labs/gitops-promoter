# Example: pullrequest-basic

Renders a `PullRequestTemplate` using a `ChangeTransferPolicy` and a `PromotionStrategy`. The
template shows all the common pieces you'll need in a real PR template: truncated SHA in the title,
branch names, and strategy metadata in the description.

## Run

From the repo root:

```bash
go run ./cmd templates pullrequest \
  --pull-request-template cmd/templates/examples/pullrequest-basic/pr-template.yaml \
  --change-transfer-policy cmd/templates/examples/pullrequest-basic/ctp.yaml \
  --promotion-strategy    cmd/templates/examples/pullrequest-basic/ps.yaml
```

Try `--output yaml` or `--output json` to get structured output.

## Expected output (human)

```
Title:
  Promote 5d6f0a1 to `environment/production`

Description:
  Promote to environment/production
  From:     environment/staging
  Strategy: sample-ps
  Repo:     sample-repo
```
