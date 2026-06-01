#!/usr/bin/env bash
#
# Registers (or unregisters) the dashboard aggregation APIService so the cluster's
# kube-aggregator routes PromotionStrategyDetails to a locally-running apiserver
# (`make run-apiserver`) on your host machine.
#
# How it works: APIService can only target an in-cluster Service, so we create a
# selector-less Service + a hand-written EndpointSlice whose address is a host IP the
# cluster can reach. This only works when the cluster can route back to your host
# (kind / Docker Desktop via host.docker.internal). Remote clusters can't reach your
# laptop without a tunnel.
#
# Usage:
#   hack/apiserver-local-register.sh [register|unregister]
#
# Environment overrides:
#   NAMESPACE   target namespace (default: promoter-system)
#   KIND_NODE   docker container of the cluster node used to resolve the host IP
#               (default: kind-control-plane)
#   HOST_IP     host IP reachable from the cluster (default: auto-detected from
#               host.docker.internal inside KIND_NODE)
#   PORT        local apiserver --secure-port (default: 6443)
#
# Requires: kubectl, and (for auto HOST_IP detection) docker.
set -euo pipefail

ACTION="${1:-register}"
NAMESPACE="${NAMESPACE:-promoter-system}"
KIND_NODE="${KIND_NODE:-kind-control-plane}"
PORT="${PORT:-6443}"

SRCROOT="$(CDPATH='' cd -- "$(dirname "$0")/.." && pwd -P)"
DIR="${SRCROOT}/config/apiserver/local-host"

APISERVICE_NAME="v1alpha1.dashboard.promoter.argoproj.io"
SERVICE_NAME="promoter-apiserver-local"
ENDPOINTSLICE_NAME="promoter-apiserver-local-1"

if [ "$ACTION" = "unregister" ]; then
  echo ">> Removing local APIService registration"
  kubectl delete apiservice "$APISERVICE_NAME" --ignore-not-found
  kubectl -n "$NAMESPACE" delete endpointslice "$ENDPOINTSLICE_NAME" --ignore-not-found
  kubectl -n "$NAMESPACE" delete service "$SERVICE_NAME" --ignore-not-found
  echo ">> Done."
  exit 0
fi

if [ "$ACTION" != "register" ]; then
  echo "unknown action: $ACTION (expected 'register' or 'unregister')" >&2
  exit 1
fi

if [ -z "${HOST_IP:-}" ]; then
  echo ">> Detecting host IP reachable from node ${KIND_NODE} (host.docker.internal)"
  HOST_IP="$(docker exec "$KIND_NODE" getent hosts host.docker.internal 2>/dev/null | awk '{print $1}' | head -n1 || true)"
fi
if [ -z "${HOST_IP:-}" ]; then
  echo "ERROR: could not determine HOST_IP. Set HOST_IP=<ip reachable from the cluster> explicitly." >&2
  echo "       (On Docker Desktop: docker exec ${KIND_NODE} getent hosts host.docker.internal)" >&2
  exit 1
fi

echo ">> Registering local apiserver: namespace=${NAMESPACE} host=${HOST_IP} port=${PORT}"

render() {
  sed -e "s/__HOST_IP__/${HOST_IP}/g" -e "s/__PORT__/${PORT}/g" "$1"
}

{
  render "${DIR}/service.yaml"
  echo "---"
  render "${DIR}/endpointslice.yaml"
  echo "---"
  cat "${DIR}/apiservice.yaml"
} | kubectl apply -f -

echo ">> Done. Check readiness with:"
echo "   kubectl get apiservice ${APISERVICE_NAME}"
echo "   kubectl get promotionstrategydetails -A"
echo ">> Make sure 'make run-apiserver' is running and listening on :${PORT}."
