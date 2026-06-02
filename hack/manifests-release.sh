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

# Dashboard aggregation apiserver bundles. These are published as separate release
# artifacts (not folded into install.yaml) because the APIService name must stay exactly
# v1alpha1.dashboard.promoter.argoproj.io and the extension-apiserver-authentication-reader
# RoleBinding must stay in kube-system - see docs/dashboard-apiserver.md. We ship two
# variants so users can pick their serving-cert strategy:
#   * cert-manager: cert-manager issues and rotates the serving cert (single apply).
#   * byo-cert:     no cert-manager; the operator provides the serving cert + caBundle.
cd "${SRCROOT}/config/apiserver/release-cert-manager" && $KUSTOMIZE edit set image "quay.io/argoprojlabs/gitops-promoter=${IMAGE_FQN}"
echo "${AUTOGENMSG}" > "${SRCROOT}/install-dashboard-apiserver-cert-manager.yaml"
$KUSTOMIZE build "${SRCROOT}/config/apiserver/release-cert-manager" >> "${SRCROOT}/install-dashboard-apiserver-cert-manager.yaml"

cd "${SRCROOT}/config/apiserver/release-byo-cert" && $KUSTOMIZE edit set image "quay.io/argoprojlabs/gitops-promoter=${IMAGE_FQN}"
echo "${AUTOGENMSG}" > "${SRCROOT}/install-dashboard-apiserver-byo-cert.yaml"
$KUSTOMIZE build "${SRCROOT}/config/apiserver/release-byo-cert" >> "${SRCROOT}/install-dashboard-apiserver-byo-cert.yaml"
