# Image URL to use all building/pushing image targets
IMG ?= quay.io/argoprojlabs/gitops-promoter:latest
# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.36.0

CURRENT_DIR=$(shell pwd)

MKDOCS_DOCKER_IMAGE?=python:3.13-alpine
MKDOCS_RUN_ARGS?=

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif
# containerbase install-tool (Renovate postUpgradeTasks) symlinks `go` into PATH but
# not sibling tools like gofmt; GOROOT/bin is the standard place to find them.
export PATH := $(shell go env GOROOT)/bin:$(PATH)

GIT_TAG:=$(if $(GIT_TAG),$(GIT_TAG),$(shell if [ -z "`git status --porcelain`" ]; then git describe --exact-match --tags HEAD 2>/dev/null; fi))


# docker image publishing options
IMAGE_NAMESPACE?=quay.io/argoprojlabs
IMAGE_NAME=${IMAGE_NAMESPACE}/gitops-promoter

ifneq (${GIT_TAG},)
IMAGE_TAG=${GIT_TAG}
else
IMAGE_TAG?=latest
endif

IMG=${IMAGE_NAME}:${IMAGE_TAG}

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#Select_Graphic_Rendition_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role webhook paths="./..."
	# CRD generation is scoped to the packages that actually define CRDs. The
	# view aggregation API (api/view/...) is served by an extension
	# apiserver, not as CRDs; including it would make controller-gen's CRD generator
	# treat those TypeMeta/ObjectMeta types as Kinds and fail.
	$(CONTROLLER_GEN) crd paths="./api/v1alpha1/..." paths="./internal/types/argocd/..." output:crd:artifacts:config=config/crd/bases
	# Move the Application CRD to the test dir. We don't need it in the promoter config, but we need it for e2e tests.
	mv config/crd/bases/argoproj.io_applications.yaml test/external_crds/

# controller-gen v0.21+ emits applyconfiguration types that import applyconfiguration/internal
# (for Extract* helpers). On the first pass after an output-format change, controller-gen
# can rewrite those files and then fail in the same process because go/packages loaded the
# import graph before the rewrite ("no such package located"). A second pass loads the
# updated files and succeeds. Steady-state runs succeed on both passes; the first || true
# only masks that one-shot migration failure.
.PHONY: generate
generate: controller-gen ## Generate DeepCopy and applyconfiguration code (controller-gen object + applyconfiguration).
	@$(CONTROLLER_GEN) applyconfiguration:headerFile="hack/boilerplate.go.txt" object:headerFile="hack/boilerplate.go.txt" paths="./..." || true
	@$(CONTROLLER_GEN) applyconfiguration:headerFile="hack/boilerplate.go.txt" object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: generate-extension-icon-styles
generate-extension-icon-styles: ## Generate Argo CD extension icon styles from logo SVGs.
	./hack/extension-icon-styles.sh

# Generates hack/celcost/report.md, embedded into docs/contributing/writing-cel-rules.md
# via markdown_include. celcost is its own Go module (hack/celcost/go.mod) so its
# CEL/apiserver dependencies stay out of the root module, hence the cd + go run .
.PHONY: cel-cost-report
cel-cost-report: ## Estimate CRD CEL validation costs vs apiserver limits and write the docs report.
	cd hack/celcost && go run . -o report.md ../../config/crd/bases

.PHONY: generate-ui-types
generate-ui-types: ## Generate TypeScript types from the view APIService OpenAPI schemas (requires committed zz_generated.openapi.go from make generate-apiserver).
	go run ./hack/view2ts -o hack/view2ts/dist/view.openapi.json
	cd ui/codegen && npm ci && npm run generate
	cd ui/shared && npm ci

.PHONY: generate-all
generate-all: generate generate-apiserver generate-extension-icon-styles ## Run all code generation used in CI (controller-gen, view apiserver codegen, extension icon styles). For mocks, run make mockery-gen separately.

# Code generation for the view aggregation API (api/view/...).
#
# These are aggregated-apiserver types (NOT CRDs), so they are owned entirely by the
# k8s code-generators here, never by controller-gen:
#   * deepcopy-gen   -> zz_generated.deepcopy.go   (DeepCopy + DeepCopyObject)
#   * openapi-gen    -> zz_generated.openapi.go    (OpenAPI definitions)
# The resource has a single served version (v1alpha1) and no etcd backing, so there
# is no internal/hub version and no conversion-gen step. The package carries no
# kubebuilder markers, so `make generate` (controller-gen) and `make manifests` skip
# it and never emit a CRD for this group. Re-run this target after changing any type
# in api/view/...; the output is committed.
.PHONY: generate-apiserver
generate-apiserver: ## Generate deepcopy/openapi for the view aggregation API (api/view/...).
	go tool deepcopy-gen \
		--output-file zz_generated.deepcopy.go \
		--go-header-file hack/boilerplate.go.txt \
		./api/view/v1alpha1
	# The bundle embeds the promoter v1alpha1 types, so openapi-gen runs over both the
	# view and promoter packages plus the apimachinery/core/apiextensions deps the
	# promoter types reference. --output-model-name-file generates OpenAPIModelName()
	# accessors so the served model names are dot-separated (e.g.
	# io.argoproj.promoter.v1alpha1.* and io.argoproj.promoter.view.v1alpha1.*)
	# rather than the Go import path; slash-containing names break strict OpenAPI v2
	# consumers (e.g. Argo CD). The k8s.io dependency packages already ship their own
	# model names, so they are marked --readonly-pkg (don't regenerate into the cache).
	go tool openapi-gen \
		--output-dir ./api/view/v1alpha1 \
		--output-pkg github.com/argoproj-labs/gitops-promoter/api/view/v1alpha1 \
		--output-file zz_generated.openapi.go \
		--output-model-name-file zz_generated.model_name.go \
		--go-header-file hack/boilerplate.go.txt \
		--readonly-pkg k8s.io/api/core/v1 \
		--readonly-pkg k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1 \
		--readonly-pkg k8s.io/apimachinery/pkg/apis/meta/v1 \
		--readonly-pkg k8s.io/apimachinery/pkg/runtime \
		--readonly-pkg k8s.io/apimachinery/pkg/version \
		--readonly-pkg k8s.io/apimachinery/pkg/api/resource \
		--readonly-pkg k8s.io/apimachinery/pkg/util/intstr \
		./api/view/v1alpha1 ./api/v1alpha1 \
		k8s.io/api/core/v1 \
		k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1 \
		k8s.io/apimachinery/pkg/apis/meta/v1 k8s.io/apimachinery/pkg/runtime k8s.io/apimachinery/pkg/version \
		k8s.io/apimachinery/pkg/api/resource k8s.io/apimachinery/pkg/util/intstr

.PHONY: mockery-gen
mockery-gen: mockery
	$(MOCKERY)

.PHONY: apiserver-certs
apiserver-certs: ## Generate self-signed serving certs for the dashboard apiserver and patch the APIService caBundle (manual cert path).
	./hack/gen-apiserver-certs.sh

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out

# Utilize Kind or modify the e2e tests to load the image locally, enabling compatibility with other vendors.
.PHONY: test-e2e  # Run the e2e tests against a Kind k8s instance that is spun up.
test-e2e:
	go test ./test/e2e/ -v

.PHONY: test-deps
test-deps: ginkgo manifests generate fmt vet envtest

.PHONY: test-parallel
test-parallel: test-deps ## Run tests in parallel
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go tool ginkgo -p -procs=4 -r -v -cover -coverprofile=cover.out -coverpkg=./... --junit-report=junit.xml internal/

.PHONY: test-parallel-repeat3
test-parallel-repeat3: test-deps ## Run tests in parallel 3 times to check for flakiness --repeat does not count the first run
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go tool ginkgo -p -procs=4 -r -v -cover -coverprofile=cover.out -coverpkg=./... --junit-report=junit.xml --repeat=2 internal/

##@ Fuzzing

# Packages that define native Go fuzz targets (Fuzz* in fuzz_test.go).
FUZZ_PACKAGES ?= ./internal/utils
# Duration per target for `fuzz-explore` (scheduled workflow and local; each Fuzz* runs sequentially within a package).
FUZZ_TIME ?= 30s

.PHONY: fuzz-replay fuzz-explore
fuzz-replay: ## Replay seeds (`f.Add`) + corpus (`testdata/fuzz`); regression only (no `-fuzz`).
	@set -euo pipefail; \
	for pkg in $(FUZZ_PACKAGES); do \
	  list=$$(go test $$pkg -list Fuzz); \
	  fuzzes=$$(echo "$$list" | { grep '^Fuzz' || true; }); \
	  if [ -z "$$fuzzes" ]; then \
	    continue; \
	  fi; \
	  for fuzz in $$fuzzes; do \
	    echo "fuzz-replay $$pkg $$fuzz"; \
	    go test $$pkg -count=1 -run=^$$fuzz$$; \
	  done; \
	done

# Bounded exploratory fuzzing: mutates beyond seeds (`f.Add`) + corpus (`testdata/fuzz`) for FUZZ_TIME per target (scheduled workflow uses this variable too).
fuzz-explore: ## Exploratory fuzzing for FUZZ_TIME per target (default $(FUZZ_TIME); see Makefile).
	@set -euo pipefail; \
	for pkg in $(FUZZ_PACKAGES); do \
	  list=$$(go test $$pkg -list Fuzz); \
	  fuzzes=$$(echo "$$list" | { grep '^Fuzz' || true; }); \
	  if [ -z "$$fuzzes" ]; then \
	    continue; \
	  fi; \
	  for fuzz in $$fuzzes; do \
	    echo "fuzz-explore $$pkg $$fuzz ($(FUZZ_TIME))"; \
	    go test $$pkg -count=1 -run=^$$fuzz$$ -fuzz=$$fuzz -fuzztime=$(FUZZ_TIME); \
	  done; \
	done

.PHONY: lint nilaway-no-test
lint: golangci-lint ## Run golangci-lint linter & yamllint
	$(GOLANGCI_LINT) run

.PHONY: deadcode
deadcode: ## Report unreachable functions in module internals (from cmd entrypoints).
	$(MAKE) $(DEADCODE)
	@out=$$($(DEADCODE) -test -filter='$(DEADCODE_FILTER)' ./... 2>&1); \
	if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	$(GOLANGCI_LINT) run --fix

.PHONY: nilaway-no-test
nilaway-no-test: nilaway ## Run nilaway to remove nil checks from the code
	GOMEMLIMIT=8GiB $(NILAWAY) -test=false -exclude-pkgs="sigs.k8s.io,k8s.io" ./...

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/manager cmd/main.go

.PHONY: install-ui-deps
install-ui-deps: ## Install shared UI workspace dependencies (ui/shared, ui/components-lib).
	cd ui/shared && npm install
	cd ui/components-lib && npm install

.PHONY: build-dashboard-ui
build-dashboard-ui: ## Build dashboard UI and embed it (requires install-ui-deps).
	cd ui/dashboard && npm install && npm run build:embed

.PHONY: build-dashboard
build-dashboard: install-ui-deps build-dashboard-ui ## Install UI deps and build dashboard.

.PHONY: build-extension-ui
build-extension-ui: ## Build ArgoCD extension (requires install-ui-deps).
	cd ui/extension && npm install && npm run build

.PHONY: build-extension
build-extension: install-ui-deps build-extension-ui ## Install UI deps and build extension.

.PHONY: install-extension-local
install-extension-local: ## Install ArgoCD extension to /tmp/extensions/promoter directory.
	mkdir -p /tmp/extensions/promoter
	cp ui/extension/dist/extension-promoter.js /tmp/extensions/promoter/

.PHONY: build-extension-local
build-extension-local: build-extension install-extension-local ## Build ArgoCD extension and install it locally to /tmp/extensions/promoter directory.

.PHONY: build-all
build-all: build-dashboard build-extension build ## Build dashboard UI, extension, and then the manager binary.

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/main.go controller

.PHONY: run-dashboard-dev
run-dashboard-dev: 
	cd ui/dashboard && npm install && npm run dev &
	sleep 2
	go run ./cmd/main.go dashboard

.PHONY: run-dashboard
run-dashboard: build-dashboard 
	go run ./cmd/main.go dashboard

# Run the dashboard aggregation apiserver from your host (out-of-cluster), against
# the current kubeconfig context. It self-signs a serving cert into APISERVER_CERT_DIR
# (no --tls-cert-file), and delegates authn/authz to the cluster via the kubeconfig.
# Note: `kubectl get promotionstrategydetails` won't route here (the cluster's
# aggregator can't reach your host) — curl it directly, e.g.
#   curl -k https://127.0.0.1:$(APISERVER_SECURE_PORT)/healthz
APISERVER_KUBECONFIG ?= $(HOME)/.kube/config
APISERVER_SECURE_PORT ?= 6443
APISERVER_CERT_DIR ?= /tmp/promoter-apiserver-certs
.PHONY: run-apiserver
run-apiserver: ## Run the dashboard aggregation apiserver locally (out-of-cluster) against the current kubeconfig.
	go run ./cmd/main.go apiserver \
		--authentication-kubeconfig "$(APISERVER_KUBECONFIG)" \
		--authorization-kubeconfig "$(APISERVER_KUBECONFIG)" \
		--secure-port $(APISERVER_SECURE_PORT) \
		--cert-dir "$(APISERVER_CERT_DIR)"

# Register/unregister the APIService so a kind/Docker-Desktop cluster routes the
# aggregated PromotionStrategyDetails resource to your locally-running `make
# run-apiserver`. Auto-detects the host IP via host.docker.internal; override with
# HOST_IP=... (and KIND_NODE=..., PORT=...). Local-dev only (insecureSkipTLSVerify).
.PHONY: apiserver-register-local
apiserver-register-local: ## Point the cluster aggregator at your local run-apiserver (kind/Docker Desktop).
	PORT=$(APISERVER_SECURE_PORT) ./hack/apiserver-local-register.sh register

.PHONY: apiserver-unregister-local
apiserver-unregister-local: ## Remove the local APIService registration created by apiserver-register-local.
	./hack/apiserver-local-register.sh unregister

.PHONY: lint-dashboard
lint-dashboard: install-ui-deps ## Run dashboard type-check, lint and audit checks
	cd ui/dashboard && npm run type-check && npm run lint && npm run format:check && npm audit --omit=dev

.PHONY: lint-extension
lint-extension: install-ui-deps ## Run extension type-check, lint and audit checks
	cd ui/extension && npm run type-check && npm run lint && npm run format:check && npm audit --omit=dev

# LCOV paths: vitest coverage uses istanbul lcov reporter with projectRoot = repo root (see vitest.config).
.PHONY: test-unit-test-extension
test-unit-test-extension: ## Run extension unit tests (with coverage)
	cd ui/extension && npm test

.PHONY: lint-components-lib
lint-components-lib: install-ui-deps ## Run components-lib type-check and format checks (includes shared/)
	cd ui/components-lib && npm run type-check && npm run format:check
	cd ui/components-lib && npx prettier --check '../shared/**/*.{ts,tsx}' --ignore-path ../.prettierignore

.PHONY: lint-ui
lint-ui: lint-dashboard lint-extension lint-components-lib ## Run all UI checks

.PHONY: test-ui-test-dashboard
test-ui-test-dashboard: ## Run dashboard unit tests (with coverage)
	cd ui/dashboard && npm test

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/build/buildkit/
.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

.PHONY: goreleaser-build-local
goreleaser-build-local: goreleaser ## Run goreleaser build locally. Use to validate the goreleaser configuration.
	$(GORELEASER) build --snapshot --clean --single-target --verbose

.PHONY: goreleaser-release-local
goreleaser-release-local: goreleaser ## Run goreleaser release locally. Use to validate the goreleaser configuration.
	$(GORELEASER) release --snapshot --clean

# PLATFORMS defines the target platforms for the manager image be built to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1). To use this option you need to:
# - be able to use docker buildx. More info: https://docs.docker.com/reference/cli/docker/buildx/
# - have enabled BuildKit. More info: https://docs.docker.com/build/buildkit/
# - be able to push the image to your registry (i.e. if you do not set a valid value via IMG=<myregistry/image:<tag>> then the export will fail)
# To adequately provide solutions that are compatible with multiple platforms, you should consider using this option.
PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for the manager for cross-platform support
	# copy existing Dockerfile and insert --platform=${BUILDPLATFORM} into Dockerfile.cross, and preserve the original Dockerfile
	sed -e '1 s/\(^FROM\)/FROM --platform=\$$\{BUILDPLATFORM\}/; t' -e ' 1,// s//FROM --platform=\$$\{BUILDPLATFORM\}/' Dockerfile > Dockerfile.cross
	- $(CONTAINER_TOOL) buildx create --name project-v3-builder
	$(CONTAINER_TOOL) buildx use project-v3-builder
	- $(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) --tag ${IMG} -f Dockerfile.cross .
	- $(CONTAINER_TOOL) buildx rm project-v3-builder
	rm Dockerfile.cross

.PHONY: build-installer
build-installer: manifests generate-all cel-cost-report kustomize ## Generate CRDs, applyconfiguration, CEL cost report, and the committed dist/ install bundles.
	./hack/manifests-release.sh $(KUSTOMIZE) $(CURDIR)/dist

.PHONY: manifests-release
manifests-release: generate manifests kustomize ## Generate the consolidated install bundles at the repo root, pinned to the release tag.
	./hack/manifests-release.sh $(KUSTOMIZE) $(CURDIR) $(IMAGE_TAG)

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) apply -f -

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default | $(KUBECTL) apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/default | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
KUBECTL ?= kubectl
KUSTOMIZE ?= $(LOCALBIN)/kustomize-$(KUSTOMIZE_VERSION)
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen-$(CONTROLLER_TOOLS_VERSION)
ENVTEST ?= $(LOCALBIN)/setup-envtest-$(ENVTEST_VERSION)
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint-$(GOLANGCI_LINT_VERSION)
DEADCODE = $(LOCALBIN)/deadcode-$(DEADCODE_VERSION)
MOCKERY = $(LOCALBIN)/mockery-$(MOCKERY_VERSION)
NILAWAY = $(LOCALBIN)/nilaway-$(NILAWAY_VERSION)
GORELEASER ?= $(LOCALBIN)/goreleaser-$(GORELEASER_VERSION)

## Tool Versions
KUSTOMIZE_VERSION ?= v5.8.1
CONTROLLER_TOOLS_VERSION ?= v0.21.0
ENVTEST_VERSION ?= release-0.24
GOLANGCI_LINT_VERSION ?= v2.12.2
DEADCODE_VERSION ?= v0.46.0
DEADCODE_FILTER ?= github.com/argoproj-labs/gitops-promoter/internal
MOCKERY_VERSION ?= v3.7.1
NILAWAY_VERSION ?= latest
GORELEASER_VERSION ?= v2.16.0

.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## configure envtest k8s directory.
	$(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN)

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,${GOLANGCI_LINT_VERSION})

$(DEADCODE): $(LOCALBIN)
	$(call go-install-tool,$(DEADCODE),golang.org/x/tools/cmd/deadcode,${DEADCODE_VERSION})

.PHONY: mockery
mockery:
	$(call go-install-tool,$(MOCKERY),github.com/vektra/mockery/v3,${MOCKERY_VERSION})

.PHONY: nilaway
nilaway:
	$(call go-install-tool,$(NILAWAY),go.uber.org/nilaway/cmd/nilaway,${NILAWAY_VERSION})

.PHONY: ginkgo
ginkgo: ## Ginkgo CLI is pinned via the go.mod `tool` directive (matches the ginkgo/v2 library version); this just verifies it builds.
	go tool ginkgo version

.PHONY: goreleaser
goreleaser: $(GORELEASER)
$(GORELEASER): $(LOCALBIN)
	$(call go-install-tool,$(GORELEASER),github.com/goreleaser/goreleaser/v2,$(GORELEASER_VERSION))

.PHONY: serve-docs
serve-docs:
	$(CONTAINER_TOOL) run ${MKDOCS_RUN_ARGS} --rm -it -p 8000:8000 -v ${CURRENT_DIR}:/docs -w /docs --entrypoint "" ${MKDOCS_DOCKER_IMAGE} sh -c 'pip install -r docs/requirements.txt; mkdocs serve -a $$(ip route get 1 | awk '\''{print $$7}'\''):8000'

.PHONY: lint-docs
lint-docs:  ## Build docs and fail if there are warnings
	@DISABLE_MKDOCS_2_WARNING=true mkdocs build 2>&1 | tee mkdocs-lint.log
	@if grep -q 'WARNING' mkdocs-lint.log; then \
	  echo "MkDocs build produced warnings!"; \
	  cat mkdocs-lint.log; \
	  exit 1; \
	fi

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary (ideally with version)
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f $(1) ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
mv "$$(echo "$(1)" | sed "s/-$(3)$$//")" $(1) ;\
}
endef
