#!/usr/bin/env bash
# Smoke test with a real terraform or tofu binary. The example repository uses
# only the built-in terraform_data resource and a local backend, so this runs
# offline. Skipped when neither binary is installed.
# shellcheck source-path=SCRIPTDIR
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# Put the stub directory last so a real binary wins when present.
PATH="${PATH#"${TESTS_DIR}/stubs:"}:${TESTS_DIR}/stubs"
export PATH

if [ -n "${REAL_TF_BINARY}" ]; then
  bin="${REAL_TF_BINARY}"
elif command -v terraform >/dev/null 2>&1 && [ "$(command -v terraform)" != "${TESTS_DIR}/stubs/terraform" ]; then
  bin=terraform
elif command -v tofu >/dev/null 2>&1 && [ "$(command -v tofu)" != "${TESTS_DIR}/stubs/tofu" ]; then
  bin=tofu
else
  echo "  SKIP  no real terraform or tofu binary on PATH"
  exit 0
fi
echo "  using $("${bin}" version | head -1)"

test_real_hydrate_then_apply() {
  local dry tip
  dry="$(git rev-parse HEAD)"
  run_hydrate ENVIRONMENTS=dev TF_BINARY="${bin}"
  tip="$(origin_sha environment/dev-next)"
  [ -n "${tip}" ] || fail "dev-next missing"

  origin_file environment/dev-next plan.txt >"${TMP}/plan.txt"
  assert_contains "${TMP}/plan.txt" 'terraform_data.greeting will be created'
  assert_contains "${TMP}/plan.txt" '^Plan: 1 to add, 0 to change, 0 to destroy\.'
  assert_not_contains "${TMP}/plan.txt" 'Refreshing state'
  origin_ls environment/dev-next | grep -q '\.terraform/' && fail ".terraform must not be committed"
  assert_eq "${dry}" "$(origin_file environment/dev-next hydrator.metadata | jq -r .drySha)"

  checkout_active dev
  run_apply TF_BINARY="${bin}" GITHUB_STEP_SUMMARY="${TMP}/summary.md"
  assert_contains "${LOG}" 'changes detected; applying'
  assert_contains "${TMP}/summary.md" '\| Applied \| yes \|'
  assert_eq "Hello from dev, developers!" "$(cd environments/dev && "${bin}" output -raw message)"

  # Second apply is a no-op.
  run_apply TF_BINARY="${bin}"
  assert_contains "${LOG}" 'no changes'
}

test_real_plan_false_validates_without_backend() {
  run_hydrate ENVIRONMENTS=staging TF_BINARY="${bin}" PLAN=false
  origin_ls environment/staging-next | grep -q '^plan.txt$' && fail "plan.txt must not exist"
  origin_ls environment/staging-next | grep -q '^environments/staging/main.tf$' || fail "root module missing"
}

run_tests
