#!/usr/bin/env bash
# Terraform hydrator for GitOps Promoter.
#
# Renders one DRY commit into a hydrated commit per environment on that
# environment's proposed branch (`<active-branch>-next`). Each hydrated commit
# contains the environment's root module, the shared modules it references,
# a `hydrator.metadata` file naming the DRY commit, and (optionally) a
# human-readable `plan.txt` so the promotion pull request shows what will
# change.
#
# The contract this script implements is documented at
# https://gitops-promoter.readthedocs.io/en/latest/advanced-usage/custom-hydrator/
# and summarized in ../README.md. Configuration is via environment variables;
# see README.md in this directory for the full list.
set -euo pipefail

log() { printf '>> %s\n' "$*" >&2; }
warn() {
  printf 'WARNING: %s\n' "$*" >&2
  if [ -n "${GITHUB_ACTIONS:-}" ]; then printf '::warning::%s\n' "$*"; fi
}
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------
ENVIRONMENTS="${ENVIRONMENTS:-}"
TF_BINARY="${TF_BINARY:-terraform}"
REMOTE="${REMOTE:-origin}"
# The `-next` suffix is hard-coded in GitOps Promoter and cannot be changed.
BRANCH_PREFIX="${BRANCH_PREFIX:-environment/}"
ENV_DIR="${ENV_DIR:-environments}"
INCLUDE_PATHS="${INCLUDE_PATHS:-modules}"
PLAN="${PLAN:-true}"
PLAN_ARGS="${PLAN_ARGS:--lock=false}"
BACKEND_CONFIG="${BACKEND_CONFIG:-}"
LOCK_PLATFORMS="${LOCK_PLATFORMS:-}"
BOOTSTRAP_ACTIVE="${BOOTSTRAP_ACTIVE:-true}"
NOTES_REF="${NOTES_REF:-refs/notes/hydrator.metadata}"

export GIT_AUTHOR_NAME="${GIT_AUTHOR_NAME:-github-actions[bot]}"
export GIT_AUTHOR_EMAIL="${GIT_AUTHOR_EMAIL:-41898282+github-actions[bot]@users.noreply.github.com}"
export GIT_COMMITTER_NAME="${GIT_COMMITTER_NAME:-${GIT_AUTHOR_NAME}}"
export GIT_COMMITTER_EMAIL="${GIT_COMMITTER_EMAIL:-${GIT_AUTHOR_EMAIL}}"

[ -n "${ENVIRONMENTS}" ] || die "ENVIRONMENTS must list at least one environment (e.g. 'dev staging prod')"
command -v "${TF_BINARY}" >/dev/null 2>&1 || die "${TF_BINARY} not found on PATH"
command -v jq >/dev/null 2>&1 || die "jq not found on PATH"
command -v git >/dev/null 2>&1 || die "git not found on PATH"

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || die "not inside a git repository"
cd "${REPO_ROOT}"

DRY_SHA="$(git rev-parse --verify "${DRY_SHA:-HEAD}^{commit}")" || die "cannot resolve DRY_SHA"

# sanitize_url rewrites ssh remotes to https, strips credentials and a trailing
# .git, and prints nothing for anything that is not an http(s) URL. repoURL in
# hydrator.metadata must be empty or an http(s) URL.
sanitize_url() {
  local url
  url="$(printf '%s' "$1" \
    | sed -E -e 's#^ssh://git@#https://#' -e 's#^git@([^:]+):#https://\1/#' -e 's#^(https?://)[^@/]+@#\1#')"
  url="${url%.git}"
  case "${url}" in
    http://*|https://*) printf '%s' "${url}" ;;
    *) ;;
  esac
}

# Prefer an explicit REPO_URL, then the GitHub-provided values, then the remote URL.
if [ -z "${REPO_URL:-}" ]; then
  if [ -n "${GITHUB_SERVER_URL:-}" ] && [ -n "${GITHUB_REPOSITORY:-}" ]; then
    REPO_URL="${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}"
  else
    REPO_URL="$(git remote get-url "${REMOTE}" 2>/dev/null || true)"
  fi
fi
REPO_URL="$(sanitize_url "${REPO_URL}")"

WORK="$(mktemp -d)"
cleanup() {
  local wt
  for wt in "${WORK}"/*/wt; do
    if [ -d "${wt}" ]; then
      git worktree remove --force "${wt}" >/dev/null 2>&1 || true
    fi
  done
  rm -rf "${WORK}"
  git worktree prune >/dev/null 2>&1 || true
  git for-each-ref --format='%(refname:short)' 'refs/heads/_hydrate/' 2>/dev/null \
    | xargs -r -n1 git branch -q -D >/dev/null 2>&1 || true
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# fetch_notes pulls the hydrator notes ref from the remote, ignoring a missing ref.
fetch_notes() {
  git fetch -q "${REMOTE}" "+${NOTES_REF}:${NOTES_REF}" >/dev/null 2>&1 || true
}

# note_dry_sha <sha> prints the drySha recorded in the hydrator note on <sha>, or nothing.
note_dry_sha() {
  git notes --ref="${NOTES_REF}" show "$1" 2>/dev/null | jq -r '.drySha // ""' 2>/dev/null || true
}

# push_note_with_retry <sha> records DRY_SHA in a note on <sha> and pushes the
# notes ref, retrying on concurrent writers (other environments or hydrator runs).
push_note_with_retry() {
  local sha="$1" attempt note
  note="$(jq -nc --arg d "${DRY_SHA}" '{drySha: $d}')"
  for attempt in 1 2 3 4 5 6 7 8; do
    fetch_notes
    git notes --ref="${NOTES_REF}" add -f -m "${note}" "${sha}" >/dev/null 2>&1
    if git push -q "${REMOTE}" "${NOTES_REF}:${NOTES_REF}" >/dev/null 2>&1; then
      return 0
    fi
    log "note push attempt ${attempt} failed, retrying"
    sleep 0.5
  done
  return 1
}

# run_tf <dir> <args...> runs the configured Terraform binary in <dir>.
run_tf() {
  local dir="$1"
  shift
  (cd "${dir}" && "${TF_BINARY}" "$@")
}

# render_env <env> <out> exports the DRY sources for <env> into <out>, runs
# Terraform to produce the lock file and plan.txt, and writes hydrator.metadata.
render_env() {
  local env="$1" out="$2"
  local src="${ENV_DIR}/${env}" root paths=() p init_args=() plan_args=() lock_args=() platform

  git cat-file -e "${DRY_SHA}:${src}" 2>/dev/null \
    || die "${src} does not exist in ${DRY_SHA:0:7}; set ENV_DIR or check ENVIRONMENTS"

  paths=("${src}")
  for p in ${INCLUDE_PATHS}; do
    if git cat-file -e "${DRY_SHA}:${p}" 2>/dev/null; then
      paths+=("${p}")
    else
      warn "include path '${p}' does not exist in ${DRY_SHA:0:7}; skipping"
    fi
  done

  mkdir -p "${out}"
  git archive "${DRY_SHA}" -- "${paths[@]}" | tar -x -C "${out}"
  root="${out}/${src}"

  if [ -n "${BACKEND_CONFIG}" ]; then
    init_args+=("-backend-config=${BACKEND_CONFIG}")
  fi

  if [ "${PLAN}" = "true" ]; then
    log "[${env}] terraform init + plan"
    run_tf "${root}" init -input=false -no-color "${init_args[@]}" >/dev/null
    read -r -a plan_args <<<"${PLAN_ARGS}"
    run_tf "${root}" plan -input=false -no-color -out="${WORK}/${env}/tfplan" "${plan_args[@]}" >/dev/null
    # `show` on the saved plan omits the "Refreshing state..." progress chatter
    # that `plan` prints, which keeps plan.txt readable in the pull request.
    run_tf "${root}" show -no-color "${WORK}/${env}/tfplan" >"${out}/plan.txt"
  else
    log "[${env}] terraform init (no backend) + validate"
    run_tf "${root}" init -backend=false -input=false -no-color >/dev/null
    run_tf "${root}" validate -no-color >/dev/null
  fi

  if [ -n "${LOCK_PLATFORMS}" ]; then
    for platform in ${LOCK_PLATFORMS}; do
      lock_args+=("-platform=${platform}")
    done
    run_tf "${root}" providers lock "${lock_args[@]}" >/dev/null
  fi

  # Keep .terraform.lock.hcl; drop provider binaries, module caches and any state.
  find "${out}" -type d -name .terraform -prune -exec rm -rf {} +
  find "${out}" -type f -name '*.tfstate*' -delete

  jq -n \
    --arg drySha "${DRY_SHA}" \
    --arg repoURL "${REPO_URL}" \
    --arg author "$(git log -1 --format='%an <%ae>' "${DRY_SHA}")" \
    --arg date "$(git log -1 --format='%aI' "${DRY_SHA}")" \
    --arg subject "$(git log -1 --format='%s' "${DRY_SHA}")" \
    --arg body "$(git log -1 --format='%b' "${DRY_SHA}")" \
    '{drySha: $drySha}
     + (if $repoURL != "" then {repoURL: $repoURL} else {} end)
     + {author: $author, date: $date, subject: $subject}
     + (if $body != "" then {body: $body} else {} end)' \
    >"${out}/hydrator.metadata"
}

# hydrate_env <env> renders the environment and pushes the proposed branch
# (and, on first run, the active branch). Runs in a subshell so one failing
# environment does not stop the others.
hydrate_env() {
  local env="$1"
  local active="${BRANCH_PREFIX}${env}" proposed="${BRANCH_PREFIX}${env}-next"
  local out="${WORK}/${env}/out" wt="${WORK}/${env}/wt"
  local proposed_exists=false active_exists=false need_note=true tip="" subject

  mkdir -p "${WORK}/${env}"

  if git fetch -q "${REMOTE}" "+refs/heads/${proposed}:refs/remotes/${REMOTE}/${proposed}" >/dev/null 2>&1; then
    proposed_exists=true
    tip="$(git rev-parse "refs/remotes/${REMOTE}/${proposed}")"
  fi
  if git ls-remote --exit-code --heads "${REMOTE}" "${active}" >/dev/null 2>&1; then
    active_exists=true
  fi
  fetch_notes

  if [ "${proposed_exists}" = true ] && [ "$(note_dry_sha "${tip}")" = "${DRY_SHA}" ]; then
    log "[${env}] ${proposed} already hydrated from ${DRY_SHA:0:7}; skipping"
    need_note=false
  else
    render_env "${env}" "${out}"

    # Materialize the hydrated tree in a worktree on the proposed branch (or an
    # orphan when the branch does not exist yet).
    if [ "${proposed_exists}" = true ]; then
      git worktree add -q --detach "${wt}" "refs/remotes/${REMOTE}/${proposed}" >/dev/null 2>&1
    else
      git worktree add -q --detach "${wt}" HEAD >/dev/null 2>&1
      git -C "${wt}" checkout -q --orphan "_hydrate/${env}"
      git -C "${wt}" rm -rfq --cached . >/dev/null 2>&1 || true
    fi
    find "${wt}" -mindepth 1 -maxdepth 1 ! -name .git -exec rm -rf {} +
    cp -a "${out}"/. "${wt}"/
    git -C "${wt}" add -A

    # Only the inputs decide whether a new hydrated commit is needed. plan.txt is
    # derived from live state and hydrator.metadata changes on every DRY commit;
    # neither is a reason to open a pull request on its own.
    if [ "${proposed_exists}" = true ] \
      && git -C "${wt}" diff --cached --quiet -- . ':(exclude)plan.txt' ':(exclude)hydrator.metadata'; then
      log "[${env}] hydrated content unchanged; recording ${DRY_SHA:0:7} in git note only"
    else
      subject="$(git log -1 --format='%s' "${DRY_SHA}")"
      git -C "${wt}" commit -q \
        -m "Hydrate ${env} from ${DRY_SHA:0:7}: ${subject}" \
        --trailer "Hydrated-From: ${DRY_SHA}"
      git -C "${wt}" push -q "${REMOTE}" "HEAD:refs/heads/${proposed}"
      tip="$(git -C "${wt}" rev-parse HEAD)"
      log "[${env}] pushed ${proposed} @ ${tip:0:7}"
    fi
    git worktree remove --force "${wt}" >/dev/null 2>&1 || true
  fi

  if [ "${need_note}" = true ]; then
    push_note_with_retry "${tip}" || die "[${env}] failed to push hydrator note after retries"
  fi

  if [ "${active_exists}" = false ]; then
    if [ "${BOOTSTRAP_ACTIVE}" = "true" ]; then
      git push -q "${REMOTE}" "${tip}:refs/heads/${active}"
      log "[${env}] bootstrapped ${active} from ${proposed} @ ${tip:0:7} (first hydration counts as promoted)"
    else
      warn "[${env}] active branch ${active} does not exist; GitOps Promoter requires it (set BOOTSTRAP_ACTIVE=true to create it)"
    fi
  fi
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
log "hydrating ${DRY_SHA:0:7} for: ${ENVIRONMENTS//,/ }"
failed=()
for env in ${ENVIRONMENTS//,/ }; do
  set +e
  ( set -e; hydrate_env "${env}" )
  rc=$?
  set -e
  if [ "${rc}" -ne 0 ]; then
    warn "[${env}] hydration failed (exit ${rc})"
    failed+=("${env}")
  fi
done

if [ "${#failed[@]}" -gt 0 ]; then
  die "hydration failed for: ${failed[*]}"
fi
log "done"
