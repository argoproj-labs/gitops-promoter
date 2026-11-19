#!/usr/bin/env bash
# Tests for apply.sh against the recording terraform stub.
# shellcheck source-path=SCRIPTDIR
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

# hydrate_and_checkout hydrates dev and leaves the shell in a checkout of the
# active branch, which is what the apply workflow sees.
hydrate_and_checkout() {
  run_hydrate ENVIRONMENTS=dev
  : >"${TF_STUB_LOG}"
  checkout_active dev
}

test_missing_metadata_fails() {
  local rc=0
  run_apply || rc=$?
  [ "${rc}" -ne 0 ] || fail "apply must fail without hydrator.metadata"
  assert_contains "${LOG}" 'hydrator.metadata not found'
  [ ! -s "${TF_STUB_LOG}" ] || fail "terraform must not run"
}

test_invalid_dry_sha_fails() {
  hydrate_and_checkout
  echo '{"drySha": "not-a-sha"}' >hydrator.metadata
  local rc=0
  run_apply || rc=$?
  [ "${rc}" -ne 0 ] || fail "apply must fail with a bad drySha"
  assert_contains "${LOG}" 'not a hex SHA'
}

test_no_changes_skips_apply() {
  hydrate_and_checkout
  run_apply
  assert_contains "${TF_STUB_LOG}" '/environments/dev init -input=false -no-color -lockfile=readonly$'
  assert_contains "${TF_STUB_LOG}" ' plan -input=false -no-color -detailed-exitcode -out=tfplan$'
  assert_not_contains "${TF_STUB_LOG}" ' apply '
  assert_contains "${LOG}" 'no changes'
  assert_contains "${LOG}" '\| Applied \| no \|'
}

test_changes_are_applied_from_saved_plan() {
  hydrate_and_checkout
  run_apply TF_STUB_CHANGES=1
  assert_contains "${TF_STUB_LOG}" ' apply -input=false -no-color tfplan$'
  assert_contains "${LOG}" '\| Applied \| yes \|'
}

test_plan_error_fails() {
  hydrate_and_checkout
  local rc=0
  run_apply TF_STUB_PLAN_EXIT=1 || rc=$?
  [ "${rc}" -ne 0 ] || fail "apply must fail when plan fails"
  assert_not_contains "${TF_STUB_LOG}" ' apply '
  assert_contains "${LOG}" 'terraform plan failed'
}

test_apply_false_stops_after_plan() {
  hydrate_and_checkout
  run_apply TF_STUB_CHANGES=1 APPLY=false
  assert_not_contains "${TF_STUB_LOG}" ' apply '
  assert_contains "${LOG}" 'APPLY=false'
}

test_step_summary_and_plan_args_and_backend_config() {
  hydrate_and_checkout
  run_apply GITHUB_STEP_SUMMARY="${TMP}/summary.md" GITHUB_SHA=deadbeef PLAN_ARGS='-refresh=false' \
    BACKEND_CONFIG=backend.hcl
  assert_file "${TMP}/summary.md"
  assert_contains "${TMP}/summary.md" '### Terraform apply'
  assert_contains "${TMP}/summary.md" "\\| Hydrated commit \\| \`deadbeef\` \\|"
  assert_contains "${TMP}/summary.md" "\\| Root module \\| \`environments/dev\` \\|"
  assert_contains "${TF_STUB_LOG}" ' -lockfile=readonly -backend-config=backend.hcl$'
  assert_contains "${TF_STUB_LOG}" ' -out=tfplan -refresh=false$'
}

test_stale_plan_warning() {
  hydrate_and_checkout
  # plan.txt was rendered with "1 to add"; the fresh plan now reports 2.
  run_apply TF_STUB_CHANGES=1 TF_STUB_PLAN_SUMMARY='Plan: 2 to add, 0 to change, 0 to destroy.' GITHUB_ACTIONS=true
  assert_contains "${LOG}" '::warning::plan differs from the plan.txt reviewed'
  assert_contains "${TF_STUB_LOG}" ' apply '

  run_apply TF_STUB_CHANGES=1 TF_STUB_PLAN_SUMMARY='Plan: 2 to add, 0 to change, 0 to destroy.' WARN_ON_STALE_PLAN=false
  assert_not_contains "${LOG}" 'plan differs'
}

test_root_dir_detection_errors() {
  hydrate_and_checkout
  mkdir -p environments/other
  local rc=0
  run_apply || rc=$?
  [ "${rc}" -ne 0 ] || fail "ambiguous root must fail"
  assert_contains "${LOG}" 'several root modules'

  run_apply ROOT_DIR=environments/dev
  assert_contains "${TF_STUB_LOG}" '/environments/dev init'
}

test_tofu_binary_is_used_when_configured() {
  hydrate_and_checkout
  run_apply TF_BINARY=tofu
  assert_contains "${TF_STUB_LOG}" ' init '
}

run_tests
