#!/usr/bin/env bash
# Shared helpers for the Terraform integration script tests. Plain bash, no
# external test framework. Source this file from a *_test.sh file and define
# functions named test_*; call run_tests at the end.
set -euo pipefail

TESTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INTEGRATION_DIR="$(cd "${TESTS_DIR}/.." && pwd)"
FIXTURE_DIR="${INTEGRATION_DIR}/examples/repo"
HYDRATE="${INTEGRATION_DIR}/hydrate.sh"
APPLY="${INTEGRATION_DIR}/apply.sh"

export PATH="${TESTS_DIR}/stubs:${PATH}"
export GIT_AUTHOR_NAME="Test Author"
export GIT_AUTHOR_EMAIL="test@example.com"
export GIT_COMMITTER_NAME="Test Author"
export GIT_COMMITTER_EMAIL="test@example.com"
# Keep the scripts' GitHub detection off unless a test turns it on.
unset GITHUB_ACTIONS GITHUB_SHA GITHUB_STEP_SUMMARY GITHUB_SERVER_URL GITHUB_REPOSITORY
# Stub tests always exercise the default binary name; e2e_real_binary.sh reads
# REAL_TF_BINARY to pick the real one.
export REAL_TF_BINARY="${TF_BINARY:-}"
unset TF_BINARY

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  if [ -n "${LOG:-}" ] && [ -s "${LOG}" ]; then
    echo "--- script output ---" >&2
    cat "${LOG}" >&2
  fi
  exit 1
}

assert_eq() { # expected actual [message]
  if [ "$1" != "$2" ]; then
    fail "${3:-values differ}: expected '$1', got '$2'"
  fi
}

assert_file() { [ -f "$1" ] || fail "${2:-expected file to exist}: $1"; }
assert_no_file() { [ ! -e "$1" ] || fail "${2:-expected file to be absent}: $1"; }
assert_dir() { [ -d "$1" ] || fail "${2:-expected directory to exist}: $1"; }

assert_contains() { # file pattern [message]
  grep -q -E -- "$2" "$1" || fail "${3:-pattern not found}: '$2' in $1"
}
assert_not_contains() { # file pattern [message]
  ! grep -q -E -- "$2" "$1" || fail "${3:-unexpected pattern}: '$2' in $1"
}

# setup_repo creates a bare origin seeded from examples/repo and a clone of it,
# then cds into the clone. Sets ORIGIN, CLONE, TF_STUB_LOG, LOG.
setup_repo() {
  TMP="$(mktemp -d)"
  ORIGIN="${TMP}/origin.git"
  CLONE="${TMP}/clone"
  export TF_STUB_LOG="${TMP}/terraform.log"
  LOG="${TMP}/script.log"
  : >"${TF_STUB_LOG}"

  git init -q --bare -b main "${ORIGIN}"
  git init -q -b main "${CLONE}"
  cp -a "${FIXTURE_DIR}"/. "${CLONE}"/
  git -C "${CLONE}" add -A
  git -C "${CLONE}" commit -q -m "Initial infrastructure" -m "Seed the example repository."
  git -C "${CLONE}" remote add origin "${ORIGIN}"
  git -C "${CLONE}" push -q -u origin main
  cd "${CLONE}"
}

# dry_commit <path> <content> <subject> commits a change to the DRY branch and pushes it.
dry_commit() {
  mkdir -p "$(dirname "$1")"
  printf '%s\n' "$2" >"$1"
  git add -A
  git commit -q -m "$3"
  git push -q origin main
}

# origin_sha <ref> prints the SHA of <ref> on the origin, or nothing.
origin_sha() { git -C "${ORIGIN}" rev-parse -q --verify "refs/heads/$1" 2>/dev/null || true; }

# origin_file <ref> <path> prints a file from a branch on the origin.
origin_file() { git -C "${ORIGIN}" show "refs/heads/$1:$2"; }

# origin_ls <ref> lists the files on a branch on the origin.
origin_ls() { git -C "${ORIGIN}" ls-tree -r --name-only "refs/heads/$1"; }

# origin_note <sha> prints the hydrator note for <sha> on the origin.
origin_note() { git -C "${ORIGIN}" notes --ref=refs/notes/hydrator.metadata show "$1" 2>/dev/null || true; }

# run_hydrate [VAR=value ...] runs hydrate.sh with the given env, capturing output in LOG.
run_hydrate() { env "$@" "${HYDRATE}" >"${LOG}" 2>&1; }

# run_apply [VAR=value ...] runs apply.sh with the given env, capturing output in LOG.
run_apply() { env "$@" "${APPLY}" >"${LOG}" 2>&1; }

# checkout_active <env> clones the active branch into a fresh directory and cds into it.
checkout_active() {
  local dir="${TMP}/active-$1"
  git clone -q --branch "environment/$1" "${ORIGIN}" "${dir}"
  cd "${dir}"
}

# run_tests runs every test_* function in a subshell with a fresh repo and
# reports a one-line result each. Exits non-zero if any test failed.
run_tests() {
  local name failed=0 total=0 out rc
  for name in $(declare -F | awk '{print $3}' | grep '^test_' | sort); do
    total=$((total + 1))
    out="$(mktemp)"
    # Run outside an `if` so errexit stays active inside the subshell.
    set +e
    (
      set -e
      setup_repo
      "${name}"
    ) >"${out}" 2>&1
    rc=$?
    set -e
    if [ "${rc}" -eq 0 ]; then
      printf '  PASS  %s\n' "${name}"
    else
      failed=$((failed + 1))
      printf '  FAIL  %s\n' "${name}"
      sed 's/^/        /' "${out}"
    fi
    rm -f "${out}"
  done
  printf '%s: %d passed, %d failed\n' "$(basename "$0")" "$((total - failed))" "${failed}"
  [ "${failed}" -eq 0 ]
}
