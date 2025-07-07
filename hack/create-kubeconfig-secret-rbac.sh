#!/usr/bin/env bash

# This script generates a kubeconfig with minimal permissions to get, list, and watch Argo CD applications.
# It creates the necessary RBAC (ClusterRole, ClusterRoleBinding, ServiceAccount) and stores
# the resulting kubeconfig as a secret that can be used for cross-cluster access.
#
# Steps:
# 1. Creates a ClusterRole and ClusterRoleBinding for Argo CD application access in the target context cluster
# 2. Creates a ServiceAccount in the target context's namespace
# 3. Generates a kubeconfig using the ServiceAccount token 
# 4. Verifies the kubeconfig has proper permissions
# 5. Creates a secret containing the kubeconfig in the specified context and namespace



set -e

# Default values
SERVICE_ACCOUNT="argocd-app-viewer"
TARGET_CONTEXT=""
SECRET_NAME=""
TARGET_NAMESPACE="default"
SECRET_CONTEXT=""
SECRET_NAMESPACE=""

# Function to display usage information
function show_help {
    echo "Usage: $0 [options]"
    echo " --secret-name NAME      Name for the secret (defaults to target context name)"
    echo " --secret-context CONTEXT Context where the secret should be created (defaults to target context)"
    echo " --secret-namespace NS    Namespace where the secret should be created (defaults to target namespace)"
    echo " --target-context CONTEXT  Kubeconfig context to use for service account (required)"
    echo " --target-namespace NS    Namespace to create the service account in (default namespace: ${TARGET_NAMESPACE})"
    echo " --service-account NAME  Name of the service account to create (default: ${SERVICE_ACCOUNT})"
    echo " -h, --help              Show this help message"
    echo ""
    echo "Example: $0 --target-context remote-cluster --target-namespace argocd --secret-context promoter-cluster --secret-namespace promoter"
}

# Parse command line options
while [[ $# -gt 0 ]]; do
    key="$1"
    case $key in
        --secret-name)
            SECRET_NAME="$2"
            shift 2
            ;;
        --target-namespace)
            TARGET_NAMESPACE="$2"
            shift 2
            ;;
        --target-context)
            TARGET_CONTEXT="$2"
            shift 2
            ;;
        --secret-context)
            SECRET_CONTEXT="$2"
            shift 2
            ;;
        --secret-namespace)
            SECRET_NAMESPACE="$2"
            shift 2
            ;;
        --service-account)
            SERVICE_ACCOUNT="$2"
            shift 2
            ;;
        -h|--help)
            show_help
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            show_help
            exit 1
            ;;
    esac
done

# Validate required arguments
if [ -z "$TARGET_CONTEXT" ]; then
    echo "ERROR: Target context is required (--target-context)"
    show_help
    exit 1
fi

# Set default values for secret context and namespace if not specified
if [ -z "$SECRET_CONTEXT" ]; then
    SECRET_CONTEXT="$TARGET_CONTEXT"
fi

if [ -z "$SECRET_NAMESPACE" ]; then
    SECRET_NAMESPACE="$TARGET_NAMESPACE"
fi

# Set secret name to context if not specified
if [ -z "$SECRET_NAME" ]; then
    SECRET_NAME="${TARGET_CONTEXT}-kubeconfig"
fi

# Create temporary files
TEMP_KUBECONFIG=$(mktemp)
TEMP_ROLE_YAML=$(mktemp)
TEMP_ROLEBINDING_YAML=$(mktemp)

# Create ClusterRole for Argo CD application access
cat > "$TEMP_ROLE_YAML" <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: ${SERVICE_ACCOUNT}
rules:
- apiGroups:
  - argoproj.io
  resources:
  - applications
  verbs:
  - get
  - list
  - watch
EOF

# Create ClusterRoleBinding
cat > "$TEMP_ROLEBINDING_YAML" <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: ${SERVICE_ACCOUNT}
subjects:
- kind: ServiceAccount
  name: ${SERVICE_ACCOUNT}
  namespace: ${TARGET_NAMESPACE}
roleRef:
  kind: ClusterRole
  name: ${SERVICE_ACCOUNT}
  apiGroup: rbac.authorization.k8s.io
EOF

# Apply Role and RoleBinding
echo "Creating ClusterRole and ClusterRoleBinding..."
kubectl --context=${TARGET_CONTEXT} apply -f "$TEMP_ROLE_YAML"
kubectl --context=${TARGET_CONTEXT} apply -f "$TEMP_ROLEBINDING_YAML"

# Create ServiceAccount if it doesn't exist
kubectl --context=${TARGET_CONTEXT} -n ${TARGET_NAMESPACE} create serviceaccount ${SERVICE_ACCOUNT} --dry-run=client -o yaml | kubectl --context=${TARGET_CONTEXT} -n ${TARGET_NAMESPACE} apply -f -

# Get the cluster CA certificate
CLUSTER_CA=$(kubectl --context=${TARGET_CONTEXT} config view --raw --minify --flatten -o jsonpath='{.clusters[].cluster.certificate-authority-data}')
if [ -z "$CLUSTER_CA" ]; then
    echo "ERROR: Could not get cluster CA certificate"
    exit 1
fi

# Get the cluster server URL
CLUSTER_SERVER=$(kubectl --context=${TARGET_CONTEXT} config view --raw --minify --flatten -o jsonpath='{.clusters[].cluster.server}')
if [ -z "$CLUSTER_SERVER" ]; then
    echo "ERROR: Could not get cluster server URL"
    exit 1
fi

# Get the service account token
SA_TOKEN=$(kubectl --context=${TARGET_CONTEXT} -n ${TARGET_NAMESPACE} create token ${SERVICE_ACCOUNT} --duration=8760h)
if [ -z "$SA_TOKEN" ]; then
    echo "ERROR: Could not create service account token"
    exit 1
fi

# Create a new kubeconfig using the service account token
cat > "$TEMP_KUBECONFIG" <<EOF
apiVersion: v1
kind: Config
clusters:
- cluster:
    certificate-authority-data: ${CLUSTER_CA}
    server: ${CLUSTER_SERVER}
  name: ${TARGET_CONTEXT}
contexts:
- context:
    cluster: ${TARGET_CONTEXT}
    namespace: ${TARGET_NAMESPACE}
    user: ${SERVICE_ACCOUNT}
  name: ${TARGET_CONTEXT}
current-context: ${TARGET_CONTEXT}
users:
- name: ${SERVICE_ACCOUNT}
  user:
    token: ${SA_TOKEN}
EOF

# Verify the kubeconfig works
echo "Verifying kubeconfig..."
if ! kubectl --kubeconfig="$TEMP_KUBECONFIG" get applications.argoproj.io -n ${TARGET_NAMESPACE} &>/dev/null; then
    rm "$TEMP_KUBECONFIG" "$TEMP_ROLE_YAML" "$TEMP_ROLEBINDING_YAML"
    echo "ERROR: Failed to verify kubeconfig - unable to list Argo CD applications."
    echo "- Ensure that the service account '${TARGET_NAMESPACE}/${SERVICE_ACCOUNT}' has the necessary permissions."
    exit 1
fi

echo "Kubeconfig verified successfully!"

# Encode the verified kubeconfig
KUBECONFIG_B64=$(cat "$TEMP_KUBECONFIG" | base64 -w0)

# Create the secret in the specified context and namespace
cat <<EOF | kubectl --context=${SECRET_CONTEXT} apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: ${SECRET_NAME}
  namespace: ${SECRET_NAMESPACE}
  labels:
    sigs.k8s.io/multicluster-runtime-kubeconfig: "true"
type: Opaque
data:
  kubeconfig: ${KUBECONFIG_B64}
EOF

# Cleanup
rm "$TEMP_KUBECONFIG" "$TEMP_ROLE_YAML" "$TEMP_ROLEBINDING_YAML"

echo "Successfully created kubeconfig secret '${SECRET_NAME}' in namespace '${SECRET_NAMESPACE}' of context '${SECRET_CONTEXT}'"

