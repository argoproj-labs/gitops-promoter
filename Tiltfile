# -*- mode: Python -*-

# Tiltfile for gitops-promoter

# =============================================================================
# Local Development (Dashboard runs locally with hot reload)
# =============================================================================

# Build the dashboard UI (runs on UI source changes)
local_resource(
    'build-dashboard',
    cmd='make build-dashboard',
    deps=['ui/dashboard/src', 'ui/components-lib/src', 'ui/shared/src'],
    ignore=['**/node_modules', '**/dist'],
    allow_parallel=True,
    labels=['build'],
)

# Run the dashboard locally (auto-restarts on ui changes)
local_resource(
    'run-dashboard',
    serve_cmd='go run ./cmd dashboard --port=8080',
    resource_deps=['build-dashboard'],
    deps=['ui/web/static'],
    labels=['local'],
    links=['http://localhost:8080'],
)

# Build extension (optional, for ArgoCD extension development)
local_resource(
    'build-extension',
    cmd='make build-extension',
    deps=['ui/extension/src', 'ui/components-lib/src'],
    ignore=['**/node_modules', '**/dist'],
    labels=['build'],
    allow_parallel=True,
)

# =============================================================================
# Kubernetes Deployment (Controller runs in K8s)
# =============================================================================

# Build Docker image for K8s deployment
docker_build(
    'quay.io/argoprojlabs/gitops-promoter',
    context='.',
    dockerfile='Dockerfile',
    only=[
        'cmd', 'api', 'internal', 'ui', 'hack/git',
        'go.mod', 'go.sum', 'Dockerfile',
    ],
    ignore=[
        '**/node_modules',
    ],
)

# Generate manifests and CRDs
local_resource(
    'generate-manifests',
    cmd='make manifests generate',
    deps=['api/v1alpha1'],
    ignore=['api/v1alpha1/zz_generated*'],
    labels=['setup'],
)

# Deploy the controller using kustomize
k8s_yaml(kustomize('config/default'))

# Configure the controller resource
k8s_resource(
    'promoter-controller-manager',
    new_name='controller',
    objects=[
        'promoter-controller-manager:serviceaccount',
        'promoter-manager-role:clusterrole',
        'promoter-manager-rolebinding:clusterrolebinding',
        'promoter-leader-election-role:role',
        'promoter-leader-election-rolebinding:rolebinding',
    ],
    resource_deps=['generate-manifests'],
    labels=['promoter'],
    port_forwards=[
        port_forward(8081, 8081, name='health'),
        port_forward(8443, 8443, name='metrics'),
        port_forward(3333, 3333, name='webhook'),
    ],
)
