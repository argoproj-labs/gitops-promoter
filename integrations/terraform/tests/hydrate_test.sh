#!/usr/bin/env bash
# Tests for hydrate.sh against the recording terraform stub.
# shellcheck source-path=SCRIPTDIR
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

test_first_hydration_creates_proposed_and_bootstraps_active() {
  local dry tip
  dry="$(git rev-parse HEAD)"
  run_hydrate ENVIRONMENTS=dev

  tip="$(origin_sha environment/dev-next)"
  [ -n "${tip}" ] || fail "environment/dev-next was not created"
  assert_eq "${tip}" "$(origin_sha environment/dev)" "active branch should be bootstrapped from the proposed tip"

  assert_eq "$(printf '%s\n' \
    .terraform.lock.hcl environments/dev/backend.tf environments/dev/main.tf environments/dev/terraform.tfvars \
    hydrator.metadata modules/greeting/main.tf modules/greeting/outputs.tf modules/greeting/variables.tf plan.txt \
    | sed 's#^\.terraform.lock.hcl#environments/dev/.terraform.lock.hcl#' | sort)" \
    "$(origin_ls environment/dev-next | sort)" "hydrated tree"

  origin_file environment/dev-next hydrator.metadata >"${TMP}/meta.json"
  jq -e . "${TMP}/meta.json" >/dev/null || fail "hydrator.metadata is not valid JSON"
  assert_eq "${dry}" "$(jq -r .drySha "${TMP}/meta.json")" "drySha"
  assert_eq "Test Author <test@example.com>" "$(jq -r .author "${TMP}/meta.json")" "author"
  assert_eq "Initial infrastructure" "$(jq -r .subject "${TMP}/meta.json")" "subject"
  assert_eq "Seed the example repository." "$(jq -r .body "${TMP}/meta.json")" "body"
  [ "$(jq -r .repoURL "${TMP}/meta.json")" = "null" ] || fail "file:// remote must not produce a repoURL"
  jq -r .date "${TMP}/meta.json" | grep -q -E '^[0-9]{4}-[0-9]{2}-[0-9]{2}T' || fail "date is not RFC3339"

  origin_file environment/dev-next plan.txt >"${TMP}/plan.txt"
  assert_contains "${TMP}/plan.txt" '^Plan: 1 to add'
  assert_eq "${dry}" "$(origin_note "${tip}" | jq -r .drySha)" "git note on the proposed tip"
  git -C "${ORIGIN}" log -1 --format=%B "${tip}" | grep -q "^Hydrated-From: ${dry}$" || fail "missing Hydrated-From trailer"

  assert_contains "${TF_STUB_LOG}" '/environments/dev init -input=false -no-color$'
  assert_contains "${TF_STUB_LOG}" ' plan -input=false -no-color -out=.* -lock=false$'
  assert_contains "${TF_STUB_LOG}" ' show -no-color '
  assert_contains "${LOG}" 'bootstrapped environment/dev'
}

test_rerun_same_dry_commit_is_a_noop() {
  run_hydrate ENVIRONMENTS=dev
  local tip note_count
  tip="$(origin_sha environment/dev-next)"
  note_count="$(git -C "${ORIGIN}" rev-list --count refs/notes/hydrator.metadata)"
  : >"${TF_STUB_LOG}"

  run_hydrate ENVIRONMENTS=dev
  assert_eq "${tip}" "$(origin_sha environment/dev-next)" "no new commit expected"
  assert_eq "${note_count}" "$(git -C "${ORIGIN}" rev-list --count refs/notes/hydrator.metadata)" "no new note expected"
  assert_contains "${LOG}" 'already hydrated'
  [ ! -s "${TF_STUB_LOG}" ] || fail "terraform must not run when the note already matches"
}

test_unrelated_dry_change_updates_note_only() {
  run_hydrate ENVIRONMENTS=dev
  local tip dry2
  tip="$(origin_sha environment/dev-next)"
  dry_commit README.md "docs only" "docs: tweak readme"
  dry2="$(git rev-parse HEAD)"

  run_hydrate ENVIRONMENTS=dev
  assert_eq "${tip}" "$(origin_sha environment/dev-next)" "README-only change must not create a hydrated commit"
  assert_eq "${dry2}" "$(origin_note "${tip}" | jq -r .drySha)" "note must advance to the new DRY sha"
  assert_contains "${LOG}" 'unchanged; recording'
}

test_module_change_creates_new_commit_on_top() {
  run_hydrate ENVIRONMENTS=dev
  local tip1 tip2 dry2
  tip1="$(origin_sha environment/dev-next)"
  dry_commit modules/greeting/extra.tf 'locals { extra = true }' "feat: add extra local"
  dry2="$(git rev-parse HEAD)"

  run_hydrate ENVIRONMENTS=dev
  tip2="$(origin_sha environment/dev-next)"
  [ "${tip1}" != "${tip2}" ] || fail "expected a new hydrated commit"
  assert_eq "${tip1}" "$(git -C "${ORIGIN}" rev-parse "${tip2}^")" "new commit must build on the previous tip"
  origin_ls environment/dev-next | grep -q '^modules/greeting/extra.tf$' || fail "new module file missing"
  assert_eq "${dry2}" "$(origin_file environment/dev-next hydrator.metadata | jq -r .drySha)"
  assert_eq "${tip1}" "$(origin_sha environment/dev)" "active branch must not move after bootstrap"
}

test_plan_false_skips_backend_and_plan() {
  run_hydrate ENVIRONMENTS=dev PLAN=false
  origin_ls environment/dev-next | grep -q '^plan.txt$' && fail "plan.txt must not exist with PLAN=false"
  assert_contains "${TF_STUB_LOG}" ' init -backend=false -input=false -no-color$'
  assert_contains "${TF_STUB_LOG}" ' validate -no-color$'
  assert_not_contains "${TF_STUB_LOG}" ' plan '
}

test_plan_args_backend_config_and_lock_platforms_are_forwarded() {
  run_hydrate ENVIRONMENTS=dev PLAN_ARGS='-lock=false -refresh=false' BACKEND_CONFIG=backend.hcl \
    LOCK_PLATFORMS='linux_amd64 darwin_arm64'
  assert_contains "${TF_STUB_LOG}" ' init -input=false -no-color -backend-config=backend.hcl$'
  assert_contains "${TF_STUB_LOG}" ' -lock=false -refresh=false$'
  assert_contains "${TF_STUB_LOG}" ' providers lock -platform=linux_amd64 -platform=darwin_arm64$'
}

test_tofu_binary_is_used_when_configured() {
  run_hydrate ENVIRONMENTS=dev TF_BINARY=tofu
  assert_contains "${TF_STUB_LOG}" ' init '
  [ -n "$(origin_sha environment/dev-next)" ] || fail "branch not created with tofu"
}

test_quoted_subject_and_body_produce_valid_json() {
  dry_commit modules/greeting/extra.tf 'locals { extra = true }' \
    "$(printf 'feat: "quoted" subject with \\backslash and ünïcödé\n\nBody line "one"\n\nSigned-off-by: Tëst <t@example.com>')"
  run_hydrate ENVIRONMENTS=dev
  origin_file environment/dev-next hydrator.metadata >"${TMP}/meta.json"
  jq -e . "${TMP}/meta.json" >/dev/null || fail "hydrator.metadata is not valid JSON"
  assert_eq 'feat: "quoted" subject with \backslash and ünïcödé' "$(jq -r .subject "${TMP}/meta.json")"
  assert_eq "$(printf 'Body line "one"\n\nSigned-off-by: Tëst <t@example.com>')" "$(jq -r .body "${TMP}/meta.json")"
}

test_multiple_environments_and_comma_separation() {
  run_hydrate ENVIRONMENTS=dev,staging
  [ -n "$(origin_sha environment/dev-next)" ] || fail "dev-next missing"
  [ -n "$(origin_sha environment/staging-next)" ] || fail "staging-next missing"
  [ -n "$(origin_sha environment/staging)" ] || fail "staging missing"
  origin_ls environment/staging-next | grep -q '^environments/staging/main.tf$' || fail "staging root module missing"
  origin_ls environment/staging-next | grep -q '^environments/dev/' && fail "staging must not include dev"
  assert_contains "${TF_STUB_LOG}" '/environments/staging init'
}

test_one_failing_environment_does_not_stop_the_others() {
  local rc=0
  run_hydrate ENVIRONMENTS='missing dev' || rc=$?
  [ "${rc}" -ne 0 ] || fail "expected non-zero exit when an environment fails"
  [ -n "$(origin_sha environment/dev-next)" ] || fail "dev should still be hydrated"
  [ -z "$(origin_sha environment/missing-next)" ] || fail "missing env must not create a branch"
  assert_contains "${LOG}" 'environments/missing does not exist'
  assert_contains "${LOG}" 'hydration failed for: missing'
}

test_bootstrap_can_be_disabled() {
  run_hydrate ENVIRONMENTS=dev BOOTSTRAP_ACTIVE=false
  [ -n "$(origin_sha environment/dev-next)" ] || fail "dev-next missing"
  [ -z "$(origin_sha environment/dev)" ] || fail "active branch must not be created"
  assert_contains "${LOG}" 'does not exist; GitOps Promoter requires it'
}

test_repo_url_from_github_env() {
  run_hydrate ENVIRONMENTS=dev GITHUB_SERVER_URL=https://github.com GITHUB_REPOSITORY=acme/infra
  assert_eq "https://github.com/acme/infra" "$(origin_file environment/dev-next hydrator.metadata | jq -r .repoURL)"
}

test_repo_url_is_sanitized() {
  run_hydrate ENVIRONMENTS=dev REPO_URL="https://x-access-token:secret@github.com/acme/infra.git"
  assert_eq "https://github.com/acme/infra" "$(origin_file environment/dev-next hydrator.metadata | jq -r .repoURL)" \
    "credentials and .git suffix must be stripped"

  dry_commit modules/greeting/extra.tf 'locals { extra = true }' "feat: extra"
  run_hydrate ENVIRONMENTS=dev REPO_URL="git@github.com:acme/infra.git"
  assert_eq "https://github.com/acme/infra" "$(origin_file environment/dev-next hydrator.metadata | jq -r .repoURL)" \
    "ssh remotes must be rewritten to https"
}

test_existing_proposed_without_note_is_rehydrated() {
  run_hydrate ENVIRONMENTS=dev
  local tip
  tip="$(origin_sha environment/dev-next)"
  git -C "${ORIGIN}" update-ref -d refs/notes/hydrator.metadata
  git update-ref -d refs/notes/hydrator.metadata 2>/dev/null || true
  : >"${TF_STUB_LOG}"

  run_hydrate ENVIRONMENTS=dev
  assert_eq "${tip}" "$(origin_sha environment/dev-next)" "identical content must not create a commit"
  assert_contains "${TF_STUB_LOG}" ' plan ' "terraform must run again when the note is missing"
  [ -n "$(origin_note "${tip}")" ] || fail "note must be restored"
}

run_tests
