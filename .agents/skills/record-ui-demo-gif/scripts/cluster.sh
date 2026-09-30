#!/usr/bin/env bash
# Cluster prep for the dashboard demo GIF: up | down [--all].
# See .agents/skills/record-ui-demo-gif/SKILL.md.
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
DEMO_ROOT="${DEMO_ROOT:-/tmp/promoter-ui-demo}"
DEMO_NAMESPACE="${DEMO_NAMESPACE:-promoter-demo}"
CONTROLLER_NAMESPACE="${CONTROLLER_NAMESPACE:-promoter-system}"
APISERVICE="v1alpha1.view.promoter.argoproj.io"

log() { printf '>> %s\n' "$*" >&2; }

apply_default_controller_config() {
  sed 's/^  name: controller-configuration$/  name: promoter-controller-configuration/' \
    "${ROOT}/config/config/controllerconfiguration.yaml" | kubectl -n "${CONTROLLER_NAMESPACE}" apply -f -
}

# The fake provider posts its webhook to port 3334, which nothing serves outside
# tests, so the controller only notices merges on requeue.
patch_requeue() {
  kubectl -n "${CONTROLLER_NAMESPACE}" patch controllerconfiguration promoter-controller-configuration \
    --type merge --patch "$(cat <<'EOF'
spec:
  promotionStrategy:
    workQueue:
      requeueDuration: 3s
  changeTransferPolicy:
    workQueue:
      requeueDuration: 3s
  changeTransferPolicyHistory:
    workQueue:
      requeueDuration: 3s
  pullRequest:
    workQueue:
      requeueDuration: 3s
  commitStatus:
    workQueue:
      requeueDuration: 3s
  timedCommitStatus:
    workQueue:
      requeueDuration: 3s
  dependentsSuccessfulCommitStatus:
    workQueue:
      requeueDuration: 3s
EOF
)"
}

cmd_up() {
  cd "${ROOT}"
  log "context: $(kubectl config current-context)"
  log "installing CRDs"
  make -s install

  log "creating namespace ${CONTROLLER_NAMESPACE} and ControllerConfiguration"
  kubectl create namespace "${CONTROLLER_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f -
  apply_default_controller_config >/dev/null
  patch_requeue

  # An in-cluster controller cannot reach the localhost git server and would
  # fight the host controller. Park it; down restores the replica count.
  mkdir -p "${DEMO_ROOT}"
  if kubectl -n "${CONTROLLER_NAMESPACE}" get deploy promoter-controller-manager >/dev/null 2>&1; then
    local replicas
    replicas="$(kubectl -n "${CONTROLLER_NAMESPACE}" get deploy promoter-controller-manager -o jsonpath='{.spec.replicas}')"
    if [ "${replicas}" != "0" ]; then
      log "scaling in-cluster promoter-controller-manager from ${replicas} to 0 (restored by down)"
      printf '%s' "${replicas}" >"${DEMO_ROOT}/in-cluster-controller-replicas"
      kubectl -n "${CONTROLLER_NAMESPACE}" scale deploy promoter-controller-manager --replicas=0
    fi
  fi

  log "deploying the dashboard aggregation apiserver"
  kubectl apply -k config/apiserver/base
  NAMESPACE="${CONTROLLER_NAMESPACE}" hack/gen-apiserver-certs.sh
  kubectl -n "${CONTROLLER_NAMESPACE}" rollout restart deploy/promoter-apiserver
  kubectl -n "${CONTROLLER_NAMESPACE}" rollout status deploy/promoter-apiserver --timeout=180s

  log "waiting for APIService ${APISERVICE} to become Available"
  local _ ok=false
  for _ in $(seq 1 60); do
    if [ "$(kubectl get apiservice "${APISERVICE}" -o jsonpath='{.status.conditions[?(@.type=="Available")].status}')" = "True" ]; then
      log "APIService is Available"
      ok=true
      break
    fi
    sleep 2
  done
  kubectl get apiservice "${APISERVICE}"
  [ "${ok}" = true ] || { log "APIService did not become Available"; exit 1; }
  log "done. Next: go run ./.agents/skills/record-ui-demo-gif/scripts/gitserver"
}

strip_finalizers() {
  local kind name
  for kind in pullrequests changetransferpolicies promotionstrategies gitrepositories scmproviders secrets; do
    for name in $(kubectl -n "${DEMO_NAMESPACE}" get "${kind}" -o name 2>/dev/null || true); do
      kubectl -n "${DEMO_NAMESPACE}" patch "${name}" --type merge -p '{"metadata":{"finalizers":null}}' >/dev/null 2>&1 || true
    done
  done
}

cmd_down() {
  cd "${ROOT}"
  log "deleting namespace ${DEMO_NAMESPACE}"
  strip_finalizers
  kubectl delete namespace "${DEMO_NAMESPACE}" --ignore-not-found --wait=false
  local _
  for _ in $(seq 1 60); do
    kubectl get namespace "${DEMO_NAMESPACE}" >/dev/null 2>&1 || break
    strip_finalizers
    sleep 2
  done
  if kubectl get namespace "${DEMO_NAMESPACE}" >/dev/null 2>&1; then
    log "namespace ${DEMO_NAMESPACE} is still terminating; check: kubectl get ns ${DEMO_NAMESPACE} -o yaml"
  fi

  log "restoring default requeue durations on the ControllerConfiguration"
  if kubectl -n "${CONTROLLER_NAMESPACE}" get controllerconfiguration promoter-controller-configuration >/dev/null 2>&1; then
    apply_default_controller_config >/dev/null
  fi

  if [ -f "${DEMO_ROOT}/in-cluster-controller-replicas" ]; then
    local replicas
    replicas="$(cat "${DEMO_ROOT}/in-cluster-controller-replicas")"
    log "restoring in-cluster promoter-controller-manager to ${replicas} replica(s)"
    kubectl -n "${CONTROLLER_NAMESPACE}" scale deploy promoter-controller-manager --replicas="${replicas}" || true
  fi

  log "removing local scratch dir ${DEMO_ROOT}"
  rm -rf "${DEMO_ROOT}"

  if [ "${1:-}" = "--all" ]; then
    log "removing the aggregation apiserver, ControllerConfiguration, ${CONTROLLER_NAMESPACE} and CRDs"
    kubectl delete -k config/apiserver/base --ignore-not-found
    kubectl -n "${CONTROLLER_NAMESPACE}" delete controllerconfiguration promoter-controller-configuration --ignore-not-found
    kubectl delete namespace "${CONTROLLER_NAMESPACE}" --ignore-not-found --wait=true --timeout=120s
    make -s uninstall ignore-not-found=true
  fi
  log "done"
}

case "${1:-}" in
  up) shift; cmd_up "$@" ;;
  down) shift; cmd_down "$@" ;;
  *) printf 'usage: %s up | down [--all]\n' "$0" >&2; exit 1 ;;
esac
