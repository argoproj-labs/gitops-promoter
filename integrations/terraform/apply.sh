#!/usr/bin/env bash
# Terraform applier for GitOps Promoter.
#
# Runs on a checkout of an environment's active branch (the commit GitOps
# Promoter merged) and converges infrastructure to it: init with the reviewed
# provider lock file, plan, and apply when the plan has changes. It never
# commits; state lives in the configured backend.
#
# The workflow that runs this script is the health signal for the
# environment: its GitHub check run on the active commit is read by a
# WebRequestCommitStatus (see examples/promoter/webrequestcommitstatus.yaml)
# and turned into the `terraform-apply` active commit status.
#
# Configuration is via environment variables; see README.md in this directory.
set -euo pipefail

log() { printf '>> %s\n' "$*" >&2; }
warn() {
  printf 'WARNING: %s\n' "$*" >&2
  if [ -n "${GITHUB_ACTIONS:-}" ]; then printf '::warning::%s\n' "$*"; fi
}
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

TF_BINARY="${TF_BINARY:-terraform}"
ENV_DIR="${ENV_DIR:-environments}"
ROOT_DIR="${ROOT_DIR:-}"
APPLY="${APPLY:-true}"
PLAN_ARGS="${PLAN_ARGS:-}"
BACKEND_CONFIG="${BACKEND_CONFIG:-}"
WARN_ON_STALE_PLAN="${WARN_ON_STALE_PLAN:-true}"
REPO_ROOT="${REPO_ROOT:-${PWD}}"

command -v "${TF_BINARY}" >/dev/null 2>&1 || die "${TF_BINARY} not found on PATH"
command -v jq >/dev/null 2>&1 || die "jq not found on PATH"
cd "${REPO_ROOT}"

# ---------------------------------------------------------------------------
# Validate the hydrated commit
# ---------------------------------------------------------------------------
[ -f hydrator.metadata ] \
  || die "hydrator.metadata not found in ${REPO_ROOT}; this does not look like a hydrated commit (see https://gitops-promoter.readthedocs.io/en/latest/advanced-usage/custom-hydrator/)"
DRY_SHA="$(jq -er '.drySha' hydrator.metadata 2>/dev/null)" \
  || die "hydrator.metadata has no drySha field"
case "${DRY_SHA}" in
  *[!0-9a-f]*) die "hydrator.metadata drySha is not a hex SHA: ${DRY_SHA}" ;;
esac
if [ "${#DRY_SHA}" -ne 40 ] && [ "${#DRY_SHA}" -ne 64 ]; then
  die "hydrator.metadata drySha has an unexpected length: ${DRY_SHA}"
fi

if [ -z "${ROOT_DIR}" ]; then
  candidates=()
  for d in "${ENV_DIR}"/*/; do
    [ -d "${d}" ] && candidates+=("${d%/}")
  done
  case "${#candidates[@]}" in
    1) ROOT_DIR="${candidates[0]}" ;;
    0) die "no root module found under ${ENV_DIR}/; set ROOT_DIR" ;;
    *) die "several root modules found under ${ENV_DIR}/ (${candidates[*]}); set ROOT_DIR" ;;
  esac
fi
[ -d "${ROOT_DIR}" ] || die "ROOT_DIR ${ROOT_DIR} does not exist"
log "applying ${DRY_SHA:0:7} (hydrated ${GITHUB_SHA:-$(git rev-parse HEAD 2>/dev/null || echo unknown)}) from ${ROOT_DIR}"

# ---------------------------------------------------------------------------
# init / plan / apply
# ---------------------------------------------------------------------------
cd "${ROOT_DIR}"

init_args=(init -input=false -no-color -lockfile=readonly)
if [ -n "${BACKEND_CONFIG}" ]; then
  init_args+=("-backend-config=${BACKEND_CONFIG}")
fi
"${TF_BINARY}" "${init_args[@]}"

read -r -a plan_args <<<"${PLAN_ARGS}"
plan_log="$(mktemp)"
trap 'rm -f "${plan_log}"' EXIT

set +e
"${TF_BINARY}" plan -input=false -no-color -detailed-exitcode -out=tfplan "${plan_args[@]}" | tee "${plan_log}"
plan_rc=${PIPESTATUS[0]}
set -e

plan_summary="$(grep -m1 -E '^(Plan: |No changes\.)' "${plan_log}" || true)"
applied=no
case "${plan_rc}" in
  0) log "no changes; infrastructure already matches ${DRY_SHA:0:7}" ;;
  2)
    if [ "${APPLY}" = "true" ]; then
      log "changes detected; applying"
      "${TF_BINARY}" apply -input=false -no-color tfplan
      applied=yes
    else
      log "changes detected; APPLY=false so stopping after plan"
    fi
    ;;
  *) die "terraform plan failed (exit ${plan_rc})" ;;
esac

# ---------------------------------------------------------------------------
# Compare against the plan reviewed in the pull request (informational only)
# ---------------------------------------------------------------------------
committed_summary=""
if [ "${WARN_ON_STALE_PLAN}" = "true" ] && [ -f "${REPO_ROOT}/plan.txt" ]; then
  committed_summary="$(grep -m1 -E '^(Plan: |No changes\.)' "${REPO_ROOT}/plan.txt" || true)"
  if [ -n "${committed_summary}" ] && [ "${committed_summary}" != "${plan_summary}" ]; then
    warn "plan differs from the plan.txt reviewed in the pull request (reviewed: '${committed_summary}', now: '${plan_summary}'); state changed between hydration and apply"
  fi
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
summary="$(cat <<EOF
### Terraform apply

| | |
|---|---|
| DRY commit | \`${DRY_SHA}\` |
| Hydrated commit | \`${GITHUB_SHA:-unknown}\` |
| Root module | \`${ROOT_DIR}\` |
| Plan | ${plan_summary:-n/a} |
| Reviewed plan | ${committed_summary:-n/a} |
| Applied | ${applied} |
EOF
)"
printf '%s\n' "${summary}"
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  printf '%s\n' "${summary}" >>"${GITHUB_STEP_SUMMARY}"
fi
