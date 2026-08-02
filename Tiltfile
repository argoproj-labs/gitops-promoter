# -*- mode: Python -*-

# Tiltfile for gitops-promoter

load('ext://restart_process', 'docker_build_with_restart')

# =============================================================================
# Local Development (Dashboard runs locally with hot reload)
# =============================================================================

# Shared npm installs must not run in parallel (dashboard + extension both use components-lib).
local_resource(
    'install-ui-deps',
    cmd='make install-ui-deps',
    deps=[
        'ui/shared/package.json',
        'ui/shared/package-lock.json',
        'ui/components-lib/package.json',
        'ui/components-lib/package-lock.json',
    ],
    ignore=['**/node_modules', '**/dist'],
    labels=['build'],
)

# Build the dashboard UI (runs on UI source changes)
local_resource(
    'build-dashboard',
    cmd='make build-dashboard-ui',
    deps=[
        'ui/dashboard/src',
        'ui/dashboard/package.json',
        'ui/dashboard/package-lock.json',
        'ui/components-lib/src',
        'ui/shared/src',
    ],
    ignore=['**/node_modules', '**/dist'],
    resource_deps=['install-ui-deps'],
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
    cmd='make build-extension-ui',
    deps=[
        'ui/extension/src',
        'ui/extension/package.json',
        'ui/extension/package-lock.json',
        'ui/components-lib/src',
        'ui/shared/src',
    ],
    ignore=['**/node_modules', '**/dist'],
    resource_deps=['install-ui-deps'],
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

# Promoter CRD names (must stay in sync with config/crd/kustomization.yaml).
promoter_crds = [
    'argocdcommitstatuses.promoter.argoproj.io',
    'changetransferpolicies.promoter.argoproj.io',
    'clusterscmproviders.promoter.argoproj.io',
    'commitstatuses.promoter.argoproj.io',
    'controllerconfigurations.promoter.argoproj.io',
    'dagcommitstatuses.promoter.argoproj.io',
    'gitcommitstatuses.promoter.argoproj.io',
    'gitrepositories.promoter.argoproj.io',
    'previousenvironmentcommitstatuses.promoter.argoproj.io',
    'promotionstrategies.promoter.argoproj.io',
    'pullrequests.promoter.argoproj.io',
    'revertcommits.promoter.argoproj.io',
    'scheduledcommitstatuses.promoter.argoproj.io',
    'scmproviders.promoter.argoproj.io',
    'timedcommitstatuses.promoter.argoproj.io',
    'webrequestcommitstatuses.promoter.argoproj.io',
]

promoter_crd_objects = [
    crd + ':customresourcedefinition' for crd in promoter_crds
]

# Tilt marks CRD resources ready as soon as apply succeeds, not when the
# Established condition is true. Apply ControllerConfiguration in a separate
# step after kubectl wait, or the mapper can fail with "no matches for kind".
wait_for_crds_cmd = 'kubectl wait --for=condition=Established --timeout=120s ' + ' '.join([
    'crd/' + crd for crd in promoter_crds
])

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
    new_name='cluster-crds',
    resource_deps=['generate-manifests'],
    labels=['promoter'],
    pod_readiness='ignore',
    objects=['promoter-system:namespace'] + promoter_crd_objects,
)

local_resource(
    'wait-for-crds',
    cmd=wait_for_crds_cmd,
    resource_deps=['cluster-crds'],
    labels=['promoter'],
)

k8s_resource(
    new_name='cluster-objects',
    resource_deps=['wait-for-crds'],
    labels=['promoter'],
    pod_readiness='ignore',
    objects=[
        'promoter-controller-manager:serviceaccount',
        'promoter-manager-role:clusterrole',
        'promoter-manager-rolebinding:clusterrolebinding',
        'promoter-leader-election-role:role',
        'promoter-leader-election-rolebinding:rolebinding',
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
        'promoter-previousenvironmentcommitstatus-admin-role:clusterrole',
        'promoter-previousenvironmentcommitstatus-editor-role:clusterrole',
        'promoter-previousenvironmentcommitstatus-viewer-role:clusterrole',
        'promoter-dagcommitstatus-admin-role:clusterrole',
        'promoter-dagcommitstatus-editor-role:clusterrole',
        'promoter-dagcommitstatus-viewer-role:clusterrole',
        'promoter-metrics-reader:clusterrole',
        'promoter-proxy-role:clusterrole',
        'promoter-timedcommitstatus-admin-role:clusterrole',
        'promoter-timedcommitstatus-editor-role:clusterrole',
        'promoter-timedcommitstatus-viewer-role:clusterrole',
        'promoter-proxy-rolebinding:clusterrolebinding',
        'promoter-controller-configuration:controllerconfiguration',
    ],
)