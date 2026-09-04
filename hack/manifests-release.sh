#! /usr/bin/env bash
set -euox pipefail

SRCROOT="$( CDPATH='' cd -- "$(dirname "$0")/.." && pwd -P )"
AUTOGENMSG="# This is an auto-generated file. DO NOT EDIT"

KUSTOMIZE="${1:-}"
if [ -z "$KUSTOMIZE" ]; then
    echo "Path to kustomize not provided"
    exit 1
fi

# Directory the generated bundles are written to. The release writes them to the repo root
# (uploaded as GitHub release assets, see .goreleaser.yaml); `make build-installer` writes
# them to dist/ where they're committed and verified by CI.
OUT_DIR="${2:-}"
if [ -z "$OUT_DIR" ]; then
    echo "Output directory not provided"
    exit 1
fi

# Optional image tag. When set, the controller + apiserver images are pinned to this tag
# (release path). When empty, the bundles keep the image as defined in config/manager
# (i.e. :latest) so the committed dist/ artifacts don't churn on every release.
IMAGE_TAG="${3:-}"
IMAGE_NAMESPACE="${IMAGE_NAMESPACE:-quay.io/argoprojlabs/gitops-promoter}"

mkdir -p "$OUT_DIR"
$KUSTOMIZE version

# build_bundle <config-dir> <output-file>
build_bundle() {
    local config_dir="$1"
    local out_file="$2"
    if [ -n "$IMAGE_TAG" ]; then
        ( cd "${SRCROOT}/${config_dir}" && $KUSTOMIZE edit set image "quay.io/argoprojlabs/gitops-promoter=${IMAGE_NAMESPACE}:${IMAGE_TAG}" )
    fi
    echo "${AUTOGENMSG}" > "$out_file"
    $KUSTOMIZE build "${SRCROOT}/${config_dir}" >> "$out_file"
}

# Controller-only bundle (no dashboard aggregation apiserver / UI). Named "without-ui" to make
# the missing UI explicit; the preferred install is one of the install-with-dashboard-* bundles
# below. See docs/getting-started.md and docs/dashboard-apiserver.md.
build_bundle "config/release" "${OUT_DIR}/install-without-ui.yaml"

# Combined bundles: the controller PLUS the dashboard aggregation apiserver in a single
# file, so installing the dashboard is one `kubectl apply`. We ship two variants so users
# can pick their serving-cert strategy:
#   * cert-manager: cert-manager issues and rotates the serving cert.
#   * byo-cert:     no cert-manager; the operator provides the serving cert + caBundle.
# The apiserver is unioned (not folded into config/default) so its base stays intact - in
# particular the extension-apiserver-authentication-reader RoleBinding stays in kube-system,
# which config/default's namespace transformer would otherwise relocate. See
# docs/dashboard-apiserver.md.
build_bundle "config/apiserver/release-combined-cert-manager" "${OUT_DIR}/install-with-dashboard-cert-manager.yaml"
build_bundle "config/apiserver/release-combined-byo-cert" "${OUT_DIR}/install-with-dashboard-byo-cert.yaml"
