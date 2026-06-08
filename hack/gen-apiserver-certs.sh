#!/usr/bin/env bash
#
# Generates a self-signed CA and serving certificate for the dashboard aggregation
# apiserver, creates the serving-cert Secret, and patches the APIService caBundle
# with the CA. This is the "manual / scripted" serving-cert path (Path A) and has
# no cert-manager dependency.
#
# Usage:
#   hack/gen-apiserver-certs.sh [NAMESPACE]
#
# Environment overrides:
#   NAMESPACE      target namespace (default: promoter-system)
#   SERVICE        apiserver service name (default: promoter-apiserver)
#   SECRET         serving-cert secret name (default: promoter-apiserver-serving-cert)
#   APISERVICE     APIService name (default: v1alpha1.view.promoter.argoproj.io)
#
# Requires: openssl, kubectl.
set -euo pipefail

NAMESPACE="${1:-${NAMESPACE:-promoter-system}}"
SERVICE="${SERVICE:-promoter-apiserver}"
SECRET="${SECRET:-promoter-apiserver-serving-cert}"
APISERVICE="${APISERVICE:-v1alpha1.view.promoter.argoproj.io}"

WORKDIR="$(mktemp -d)"
trap 'rm -rf "${WORKDIR}"' EXIT

DNS1="${SERVICE}.${NAMESPACE}.svc"
DNS2="${SERVICE}.${NAMESPACE}.svc.cluster.local"

echo ">> Generating self-signed CA"
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout "${WORKDIR}/ca.key" -out "${WORKDIR}/ca.crt" \
  -days 3650 -subj "/CN=promoter-apiserver-ca" >/dev/null 2>&1

echo ">> Generating serving key + CSR (SAN: ${DNS1}, ${DNS2})"
openssl req -newkey rsa:2048 -nodes \
  -keyout "${WORKDIR}/tls.key" -out "${WORKDIR}/tls.csr" \
  -subj "/CN=${DNS1}" >/dev/null 2>&1

cat >"${WORKDIR}/san.ext" <<EOF
subjectAltName = DNS:${DNS1}, DNS:${DNS2}
extendedKeyUsage = serverAuth
EOF

echo ">> Signing serving certificate"
openssl x509 -req -in "${WORKDIR}/tls.csr" \
  -CA "${WORKDIR}/ca.crt" -CAkey "${WORKDIR}/ca.key" -CAcreateserial \
  -out "${WORKDIR}/tls.crt" -days 365 -extfile "${WORKDIR}/san.ext" >/dev/null 2>&1

echo ">> Creating/updating Secret ${NAMESPACE}/${SECRET}"
kubectl create secret tls "${SECRET}" \
  --namespace "${NAMESPACE}" \
  --cert "${WORKDIR}/tls.crt" \
  --key "${WORKDIR}/tls.key" \
  --dry-run=client -o yaml | kubectl apply -f -

CA_BUNDLE="$(base64 < "${WORKDIR}/ca.crt" | tr -d '\n')"

echo ">> Patching APIService ${APISERVICE} caBundle"
kubectl patch apiservice "${APISERVICE}" \
  --type merge \
  -p "{\"spec\":{\"caBundle\":\"${CA_BUNDLE}\",\"insecureSkipTLSVerify\":null}}"

echo ">> Done. Restart the apiserver pod to pick up the new serving cert:"
echo "   kubectl -n ${NAMESPACE} rollout restart deploy/${SERVICE}"
