#!/usr/bin/env bash
#
# Generates a self-signed CA and serving certificate for the KubernetesResourceCommitStatus
# admission webhook, creates the serving-cert Secret the controller mounts, and patches the
# ValidatingWebhookConfiguration caBundle with the CA. This is the "manual / scripted" cert
# path and has no cert-manager dependency.
#
# Usage:
#   hack/gen-webhook-certs.sh [NAMESPACE]
#
# Environment overrides:
#   NAMESPACE      target namespace (default: promoter-system)
#   SERVICE        webhook service name (default: promoter-webhook-service)
#   SECRET         serving-cert secret name (default: promoter-webhook-server-cert)
#   WEBHOOK_CONFIG ValidatingWebhookConfiguration name (default: promoter-validating-webhook-configuration)
#   DEPLOYMENT     controller deployment to restart (default: promoter-controller-manager)
#
# Requires: openssl, kubectl.
set -euo pipefail

NAMESPACE="${1:-${NAMESPACE:-promoter-system}}"
SERVICE="${SERVICE:-promoter-webhook-service}"
SECRET="${SECRET:-promoter-webhook-server-cert}"
WEBHOOK_CONFIG="${WEBHOOK_CONFIG:-promoter-validating-webhook-configuration}"
DEPLOYMENT="${DEPLOYMENT:-promoter-controller-manager}"

WORKDIR="$(mktemp -d)"
trap 'rm -rf "${WORKDIR}"' EXIT

DNS1="${SERVICE}.${NAMESPACE}.svc"
DNS2="${SERVICE}.${NAMESPACE}.svc.cluster.local"

echo ">> Generating self-signed CA"
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout "${WORKDIR}/ca.key" -out "${WORKDIR}/ca.crt" \
  -days 3650 -subj "/CN=promoter-webhook-ca" >/dev/null 2>&1

echo ">> Generating serving key + CSR (SAN: ${DNS1}, ${DNS2})"
openssl req -newkey rsa:2048 -nodes \
  -keyout "${WORKDIR}/tls.key" -out "${WORKDIR}/tls.csr" \
  -subj "/CN=${DNS1}" >/dev/null 2>&1

cat >"${WORKDIR}/san.ext" <<EOT
subjectAltName = DNS:${DNS1}, DNS:${DNS2}
extendedKeyUsage = serverAuth
EOT

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

echo ">> Patching ValidatingWebhookConfiguration ${WEBHOOK_CONFIG} caBundle"
COUNT="$(kubectl get validatingwebhookconfiguration "${WEBHOOK_CONFIG}" -o jsonpath='{.webhooks[*].name}' | wc -w)"
PATCH="["
for ((i = 0; i < COUNT; i++)); do
  [ "$i" -gt 0 ] && PATCH+=","
  PATCH+="{\"op\":\"add\",\"path\":\"/webhooks/${i}/clientConfig/caBundle\",\"value\":\"${CA_BUNDLE}\"}"
done
PATCH+="]"
kubectl patch validatingwebhookconfiguration "${WEBHOOK_CONFIG}" --type json -p "${PATCH}"

echo ">> Done. Restart the controller to pick up the new serving cert:"
echo "   kubectl -n ${NAMESPACE} rollout restart deploy/${DEPLOYMENT}"
