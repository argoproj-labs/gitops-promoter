#! /usr/bin/env bash
set -euox pipefail

SRCROOT="$( CDPATH='' cd -- "$(dirname "$0")/.." && pwd -P )"
AUTOGENMSG="# This is an auto-generated file. DO NOT EDIT"

KUSTOMIZE="${1:-}"
if [ -z "$KUSTOMIZE" ]; then
    echo "Path to kustomize not provided"
    exit 1
fi

IMAGE_TAG="${2:-}"
if [ -z "$IMAGE_TAG" ]; then
    echo "Image tag not provided"
    exit 1
fi

IMAGE_NAMESPACE="${IMAGE_NAMESPACE:-quay.io/argoprojlabs/gitops-promoter}"
IMAGE_FQN="$IMAGE_NAMESPACE:$IMAGE_TAG"

$KUSTOMIZE version
cd "${SRCROOT}/config/release" && $KUSTOMIZE edit set image "quay.io/argoprojlabs/gitops-promoter=${IMAGE_FQN}"
echo "${AUTOGENMSG}" > "${SRCROOT}/install.yaml"
$KUSTOMIZE build "${SRCROOT}/config/release" >> "${SRCROOT}/install.yaml"

# Combined bundles: the controller PLUS the dashboard aggregation apiserver in a single
# file, so installing the dashboard is one `kubectl apply`. We ship two variants so users
# can pick their serving-cert strategy:
#   * cert-manager: cert-manager issues and rotates the serving cert.
#   * byo-cert:     no cert-manager; the operator provides the serving cert + caBundle.
# The apiserver is unioned (not folded into config/default) so its base stays intact - in
# particular the extension-apiserver-authentication-reader RoleBinding stays in kube-system,
# which config/default's namespace transformer would otherwise relocate. See
# docs/dashboard-apiserver.md.
cd "${SRCROOT}/config/apiserver/release-combined-cert-manager" && $KUSTOMIZE edit set image "quay.io/argoprojlabs/gitops-promoter=${IMAGE_FQN}"
echo "${AUTOGENMSG}" > "${SRCROOT}/install-with-dashboard-cert-manager.yaml"
$KUSTOMIZE build "${SRCROOT}/config/apiserver/release-combined-cert-manager" >> "${SRCROOT}/install-with-dashboard-cert-manager.yaml"

cd "${SRCROOT}/config/apiserver/release-combined-byo-cert" && $KUSTOMIZE edit set image "quay.io/argoprojlabs/gitops-promoter=${IMAGE_FQN}"
echo "${AUTOGENMSG}" > "${SRCROOT}/install-with-dashboard-byo-cert.yaml"
$KUSTOMIZE build "${SRCROOT}/config/apiserver/release-combined-byo-cert" >> "${SRCROOT}/install-with-dashboard-byo-cert.yaml"
