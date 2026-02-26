# -*- mode: Python -*-

# Tiltfile for gitops-promoter

load('ext://restart_process', 'docker_build_with_restart')

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

# detect cluster architecture for build
cluster_version = decode_yaml(local('kubectl version -o yaml'))
platform = cluster_version['serverVersion']['platform']
arch = platform.split('/')[1]

# Build binary locally
local_resource(
    'build-binary',
    cmd='CGO_ENABLED=0 GOOS=linux GOARCH=' + arch + ' go build -o bin/gitops-promoter ./cmd',
    deps=['cmd', 'api', 'internal'],
    resource_deps=['build-dashboard'],
    labels=['build'],
)

# =============================================================================
# Kubernetes Deployment (Controller runs in K8s)
# =============================================================================

# Build Docker image for K8s deployment
docker_build_with_restart(
    'quay.io/argoprojlabs/gitops-promoter',
    context='.',
    dockerfile='Dockerfile.tilt',
    entrypoint=[
        "/usr/bin/tini", 
        "-s", 
        "--", 
        "/app/gitops-promoter", 
        "controller", 
        "--leader-elect",
    ],
    live_update=[
        sync('./bin/gitops-promoter', '/app/gitops-promoter'),
        sync('./ui/web/static', '/app/ui/web/static'),
    ],
    only=[
        './bin/gitops-promoter',
        './ui/web/static',
        'hack/git/promoter_askpass.sh',
    ]
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
    resource_deps=['cluster-objects'],
    labels=['promoter'],
    port_forwards=[
        port_forward(8081, 8081, name='health'),
        port_forward(8443, 8443, name='metrics'),
        port_forward(3333, 3333, name='webhook'),
    ],
)

k8s_resource(
    new_name='cluster-objects',
    resource_deps=['generate-manifests'],
    labels=['promoter'],
    objects=[
        'promoter-controller-manager:serviceaccount',
        'promoter-manager-role:clusterrole',
        'promoter-manager-rolebinding:clusterrolebinding',
        'promoter-leader-election-role:role',
        'promoter-leader-election-rolebinding:rolebinding',
        'promoter-system:namespace',
        'argocdcommitstatuses.promoter.argoproj.io:customresourcedefinition',
        'changetransferpolicies.promoter.argoproj.io:customresourcedefinition',
        'clusterscmproviders.promoter.argoproj.io:customresourcedefinition',
        'commitstatuses.promoter.argoproj.io:customresourcedefinition',
        'controllerconfigurations.promoter.argoproj.io:customresourcedefinition',
        'gitcommitstatuses.promoter.argoproj.io:customresourcedefinition',
        'gitrepositories.promoter.argoproj.io:customresourcedefinition',
        'promotionstrategies.promoter.argoproj.io:customresourcedefinition',
        'pullrequests.promoter.argoproj.io:customresourcedefinition',
        'revertcommits.promoter.argoproj.io:customresourcedefinition',
        'scmproviders.promoter.argoproj.io:customresourcedefinition',
        'timedcommitstatuses.promoter.argoproj.io:customresourcedefinition',
        'promoter-argocdcommitstatus-editor-role:clusterrole',
        'promoter-argocdcommitstatus-viewer-role:clusterrole',
        'promoter-clusterscmprovider-admin-role:clusterrole',
        'promoter-clusterscmprovider-editor-role:clusterrole',
        'promoter-clusterscmprovider-viewer-role:clusterrole',
        'promoter-controllerconfiguration-admin-role:clusterrole',
        'promoter-controllerconfiguration-editor-role:clusterrole',
        'promoter-controllerconfiguration-viewer-role:clusterrole',
        'promoter-gitcommitstatus-admin-role:clusterrole',
        'promoter-gitcommitstatus-editor-role:clusterrole',
        'promoter-gitcommitstatus-viewer-role:clusterrole',
        'promoter-timedcommitstatus-admin-role:clusterrole',
        'promoter-timedcommitstatus-editor-role:clusterrole',
        'promoter-timedcommitstatus-viewer-role:clusterrole',
        'promoter-metrics-auth-role:clusterrole',
        'promoter-metrics-auth-rolebinding:clusterrolebinding',
        'promoter-metrics-reader:clusterrole',
        'promoter-controller-configuration:controllerconfiguration',
    ]
)