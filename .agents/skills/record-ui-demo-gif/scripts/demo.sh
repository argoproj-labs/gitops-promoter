#!/usr/bin/env bash
# Hydrator for the dashboard demo GIF: seed | promote <n> [--wait] | reset.
# See .agents/skills/record-ui-demo-gif/SKILL.md.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
DEMO_ROOT="${DEMO_ROOT:-/tmp/promoter-ui-demo}"
DEMO_GIT_URL="${DEMO_GIT_URL:-http://localhost:5001/acme/gitops-config}"
DEMO_WORKTREE="${DEMO_WORKTREE:-${DEMO_ROOT}/work}"
DEMO_NAMESPACE="${DEMO_NAMESPACE:-promoter-demo}"
DEMO_ENVS=(development staging production)
CONFIG_REPO_URL="${CONFIG_REPO_URL:-https://github.com/acme/gitops-config}"
CODE_REPO_URL="${CODE_REPO_URL:-https://github.com/acme/shop}"
DRY_AUTHOR="Priya Natarajan <priya@acme.dev>"
REF_AUTHOR="Sam Chen <sam@acme.dev>"

log() { printf '>> %s\n' "$*" >&2; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

fake_sha() { printf '%s' "$1" | shasum -a 1 | cut -c1-40; }

ago() {
  if date -v-1M >/dev/null 2>&1; then
    date -u -v-"$1"M +%Y-%m-%dT%H:%M:%SZ
  else
    date -u -d "$1 minutes ago" +%Y-%m-%dT%H:%M:%SZ
  fi
}

# load_change <n> sets IMAGE_TAG, DRY_*, REF_* from changes.json. 0 is the
# baseline, 1 is the history row, 2 is the take, 3 is a spare.
load_change() {
  local row
  row="$(jq -e --argjson n "$1" '.[$n]' "${HERE}/changes.json")" || die "unknown change ${1}; expected 0-3"
  IMAGE_TAG="$(jq -r .tag <<<"${row}")"
  DRY_SUBJECT="$(jq -r .subject <<<"${row}")"
  DRY_BODY="$(jq -r .body <<<"${row}")"
  DRY_DATE="$(ago "$(jq -r .minutesAgo <<<"${row}")")"
  REF_SUBJECT="$(jq -r .code.subject <<<"${row}")"
  REF_BODY="$(jq -r .code.body <<<"${row}")"
  REF_DATE="$(ago "$(jq -r .code.minutesAgo <<<"${row}")")"
  DRY_SHA="$(fake_sha "dry-${IMAGE_TAG}")"
  REF_SHA="$(fake_sha "code-${IMAGE_TAG}")"
}

require_git_server() {
  if ! curl -fsS -o /dev/null "${DEMO_GIT_URL}/info/refs?service=git-upload-pack" 2>/dev/null; then
    if ! curl -sS -o /dev/null "http://localhost:5001/" 2>/dev/null; then
      die "git server not reachable on localhost:5001. Start it with: go run ./.agents/skills/record-ui-demo-gif/scripts/gitserver"
    fi
  fi
}

ensure_worktree() {
  mkdir -p "${DEMO_ROOT}"
  if [ ! -d "${DEMO_WORKTREE}/.git" ]; then
    rm -rf "${DEMO_WORKTREE}"
    mkdir -p "${DEMO_WORKTREE}"
    git -C "${DEMO_WORKTREE}" init -q
    git -C "${DEMO_WORKTREE}" remote add origin "${DEMO_GIT_URL}"
  fi
  git -C "${DEMO_WORKTREE}" config user.name "argocd-hydrator[bot]"
  git -C "${DEMO_WORKTREE}" config user.email "hydrator@argoproj.io"
  git -C "${DEMO_WORKTREE}" fetch -q origin '+refs/heads/*:refs/remotes/origin/*' 2>/dev/null || true
}

# Writes hydrator.metadata (what the dashboard shows) plus a short per-environment
# manifest so the three branches are distinct.
write_hydrated_tree() {
  local env="$1"
  rm -rf "${DEMO_WORKTREE:?}/manifests"
  mkdir -p "${DEMO_WORKTREE}/manifests"
  cat >"${DEMO_WORKTREE}/manifests/shop.yaml" <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: shop-web
  namespace: shop-${env}
data:
  image: ghcr.io/acme/shop:${IMAGE_TAG}
  environment: ${env}
EOF
  cat >"${DEMO_WORKTREE}/hydrator.metadata" <<EOF
{
  "repoURL": "${CONFIG_REPO_URL}",
  "drySha": "${DRY_SHA}",
  "author": "${DRY_AUTHOR}",
  "date": "${DRY_DATE}",
  "subject": "${DRY_SUBJECT}",
  "body": "${DRY_BODY}",
  "references": [
    {
      "commit": {
        "repoURL": "${CODE_REPO_URL}",
        "sha": "${REF_SHA}",
        "author": "${REF_AUTHOR}",
        "date": "${REF_DATE}",
        "subject": "${REF_SUBJECT}",
        "body": "${REF_BODY}"
      }
    }
  ]
}
EOF
}

# hydrate_branch <branch> [bootstrap-branch] checks out the branch, writes the
# tree, commits, pushes, and prints the new commit SHA.
hydrate_branch() {
  local branch="$1"
  local bootstrap="${2:-}"
  local env
  env="$(printf '%s' "${branch}" | sed -E 's#^environment/([a-z]+).*$#\1#')"

  git -C "${DEMO_WORKTREE}" fetch -q origin '+refs/heads/*:refs/remotes/origin/*' 2>/dev/null || true

  if git -C "${DEMO_WORKTREE}" rev-parse -q --verify "refs/remotes/origin/${branch}" >/dev/null; then
    git -C "${DEMO_WORKTREE}" checkout -q -B "${branch}" "origin/${branch}"
  elif [ -n "${bootstrap}" ]; then
    git -C "${DEMO_WORKTREE}" checkout -q -B "${branch}" "origin/${bootstrap}"
  else
    git -C "${DEMO_WORKTREE}" checkout -q --orphan "${branch}"
    git -C "${DEMO_WORKTREE}" rm -rfq --cached . 2>/dev/null || true
    find "${DEMO_WORKTREE}" -mindepth 1 -maxdepth 1 ! -name .git -exec rm -rf {} +
  fi

  write_hydrated_tree "${env}"
  git -C "${DEMO_WORKTREE}" add -A
  git -C "${DEMO_WORKTREE}" commit -q --allow-empty \
    -m "${DRY_SUBJECT}" \
    -m "${DRY_BODY}" \
    -m "Hydrated from ${CONFIG_REPO_URL}/commit/${DRY_SHA}"
  git -C "${DEMO_WORKTREE}" push -q -u origin "${branch}:${branch}"
  git -C "${DEMO_WORKTREE}" rev-parse HEAD
}

push_change() {
  local env sha
  for env in "${DEMO_ENVS[@]}"; do
    sha="$(hydrate_branch "environment/${env}-next" "environment/${env}")"
    log "pushed environment/${env}-next @ ${sha:0:8} (dry ${DRY_SHA:0:8})"
  done
}

cmd_seed() {
  require_git_server
  ensure_worktree
  load_change 0
  log "seeding ${DEMO_GIT_URL} with ${IMAGE_TAG} on every environment"
  local env sha
  for env in "${DEMO_ENVS[@]}"; do
    sha="$(hydrate_branch "environment/${env}-next")"
    git -C "${DEMO_WORKTREE}" push -q -f origin "${sha}:refs/heads/environment/${env}"
    log "environment/${env} and environment/${env}-next @ ${sha:0:8}"
  done
  log "applying demo resources to namespace ${DEMO_NAMESPACE}"
  kubectl apply -f "${HERE}/manifests/demo.yaml"
}

cmd_promote() {
  local change="" wait_for=false arg
  for arg in "$@"; do
    case "${arg}" in
      --wait) wait_for=true ;;
      *) change="${arg}" ;;
    esac
  done
  [ -n "${change}" ] || die "usage: $0 promote <change-number> [--wait]"

  require_git_server
  ensure_worktree
  load_change "${change}"
  log "pushing change ${change}: ${DRY_SUBJECT} (${IMAGE_TAG}, dry ${DRY_SHA:0:8}, code ref ${REF_SHA:0:8})"
  push_change

  if [ "${wait_for}" = true ]; then
    log "waiting for ${DRY_SHA:0:8} to become active in every environment"
    local active
    for _ in $(seq 1 120); do
      active="$(kubectl -n "${DEMO_NAMESPACE}" get promotionstrategy shop \
        -o jsonpath='{range .status.environments[*]}{.active.dry.sha}{"\n"}{end}' 2>/dev/null || true)"
      if [ "$(printf '%s\n' "${active}" | grep -c "^${DRY_SHA}$")" -eq "${#DEMO_ENVS[@]}" ]; then
        log "promoted to every environment"
        return 0
      fi
      sleep 2
    done
    die "timed out waiting for promotion"
  fi
}

cmd_reset() {
  log "deleting namespace ${DEMO_NAMESPACE} (the controller releases finalizers)"
  kubectl delete namespace "${DEMO_NAMESPACE}" --ignore-not-found --wait=true --timeout=180s
  log "removing fake repo and worktree under ${DEMO_ROOT}"
  rm -rf "${DEMO_ROOT}/git/acme" "${DEMO_WORKTREE}"
  cmd_seed
  log "waiting for the PromotionStrategy to become Ready"
  kubectl -n "${DEMO_NAMESPACE}" wait promotionstrategy/shop --for=condition=Ready --timeout=180s
  cmd_promote 1 --wait
  log "reset complete; run: node ${HERE}/record.mjs --change 2"
}

case "${1:-}" in
  seed) shift; cmd_seed "$@" ;;
  promote) shift; cmd_promote "$@" ;;
  reset) shift; cmd_reset "$@" ;;
  *) die "usage: $0 seed | promote <n> [--wait] | reset" ;;
esac
