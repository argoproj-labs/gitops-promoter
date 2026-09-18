#!/usr/bin/env bash
# Apply go fix, taking modernizations from a newer Go language version without
# raising the go.mod language floor unless the result actually needs it:
#
#   1. go fix at the current go.mod language version.
#   2. If the running toolchain has a newer language version (1.N.0, not a
#      patch), raise the go directive to it and run go fix again.
#   3. If that pass rewrote code, put the original go directive back and
#      type-check. If everything still builds at the old floor, keep the fixes
#      and the old floor; otherwise keep the bump.
#
# Run from repo root: hack/go-fix-maybe-bump.sh (or make go-fix-maybe-bump)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

# Fixers gated on a language version's *semantics* rather than on an API that did
# not exist before. Their output still compiles at the old floor, so pass 3 cannot
# tell it depends on the newer version and would drop the bump while silently
# changing behavior. forvar removes `x := x` in range loops, which is only
# equivalent under go1.22 per-iteration loop variables. Other fixers are safe
# here because go fix applies only the non-behavior-changing fix. Pass 1 applies
# these once the floor legitimately reaches their version.
SEMANTIC_GATED_FIXERS=(-forvar=false)

# Language floor X.Y.0 from a go.mod / GOVERSION string (1.28.1 -> 1.28.0).
lang_floor() {
  local v="${1#go}"
  v="${v%% *}"
  if [[ ! "$v" =~ ^[0-9]+\.[0-9]+ ]]; then
    return 1
  fi
  awk -F. '{printf "%s.%s.0\n", $1, $2}' <<<"$v"
}

# Content hash of tracked .go files, to detect whether a fix pass rewrote anything.
go_files_hash() {
  git ls-files '*.go' | LC_ALL=C sort | git hash-object --stdin-paths | git hash-object --stdin
}

version_ge() {
  local left="$1" right="$2"
  [[ "$(printf '%s\n%s\n' "$left" "$right" | sort -V | tail -n1)" == "$left" ]]
}

echo "go-fix-maybe-bump: pass 1 (go.mod language version)"
go fix ./... || true

current="$(go list -m -f '{{.GoVersion}}')"
current_lang="$(lang_floor "$current")"
# GOVERSION is the toolchain actually running, after any GOTOOLCHAIN switch. The
# trial bump below never exceeds it, so it cannot trigger a toolchain download.
toolchain_lang="$(lang_floor "$(go env GOVERSION)")" || toolchain_lang=""

if [[ -z "$toolchain_lang" ]]; then
  echo "go-fix-maybe-bump: could not parse go env GOVERSION=$(go env GOVERSION); skipping language bump"
  exit 0
fi

if version_ge "$current_lang" "$toolchain_lang"; then
  echo "go-fix-maybe-bump: skipping language bump (go.mod language ${current_lang} >= toolchain language ${toolchain_lang})"
  exit 0
fi

hash_before="$(go_files_hash)"
echo "go-fix-maybe-bump: pass 2 (trying go ${toolchain_lang}; was go ${current})"
go mod edit -go="$toolchain_lang"
go fix "${SEMANTIC_GATED_FIXERS[@]}" ./... || true

if [[ "$hash_before" == "$(go_files_hash)" ]]; then
  echo "go-fix-maybe-bump: reverting go ${toolchain_lang} -> ${current} (second pass made no Go file changes)"
  go mod edit -go="$current"
  exit 0
fi

# -stdversion type-checks every package, tests included, and reports stdlib
# symbols that are too new for the go directive; language features that need the
# newer version fail as type errors. A failure for any other reason keeps the
# bump, which is the conservative direction.
echo "go-fix-maybe-bump: pass 3 (checking whether the fixes build at go ${current})"
go mod edit -go="$current"
if vet_output="$(go vet -stdversion ./... 2>&1)"; then
  echo "go-fix-maybe-bump: keeping go ${current} (fixes do not require go ${toolchain_lang})"
else
  echo "$vet_output"
  echo "go-fix-maybe-bump: keeping go ${toolchain_lang} (fixes require the newer language version)"
  go mod edit -go="$toolchain_lang"
fi

# The rewrites can drop imports, so go.mod/go.sum may no longer be tidy.
go mod tidy
