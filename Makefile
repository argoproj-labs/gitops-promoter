# Image URL to use all building/pushing image targets
IMG ?= quay.io/argoprojlabs/gitops-promoter:latest
# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.31.0

CURRENT_DIR=$(shell pwd)

MKDOCS_DOCKER_IMAGE?=python:3.13-alpine
MKDOCS_RUN_ARGS?=

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

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
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases
	# Move the Application CRD to the test dir. We don't need it in the promoter config, but we need it for e2e tests.
	mv config/crd/bases/argoproj.io_applications.yaml test/external_crds/

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: mockery-gen
mockery-gen:
	$(MOCKERY)

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
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" $(GINKGO) -p -procs=4 -r -v -cover -coverprofile=cover.out -coverpkg=./... --junit-report=junit.xml internal/

.PHONY: test-parallel-repeat3
test-parallel-repeat3: test-deps ## Run tests in parallel 3 times to check for flakiness --repeat does not count the first run
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" $(GINKGO) -p -procs=4 -r -v -cover -coverprofile=cover.out -coverpkg=./... --junit-report=junit.xml --repeat=2 internal/

.PHONY: lint nilaway-no-test
lint: golangci-lint ## Run golangci-lint linter & yamllint
	$(GOLANGCI_LINT) run

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

.PHONY: build-dashboard
build-dashboard: ## Build dashboard UI and embed it.
	cd ui/components-lib && npm install
	cd ui/dashboard && npm install && npm run build:embed

.PHONY: build-extension
build-extension: ## Build ArgoCD extension.
	cd ui/components-lib && npm install
	cd ui/extension && npm install && npm run build

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

.PHONY: lint-dashboard
lint-dashboard: ## Run dashboard type-check, lint and audit checks
	cd ui/dashboard && npm run type-check && npm run lint && npm audit

.PHONY: lint-extension
lint-extension: ## Run extension type-check, lint and audit checks
	cd ui/extension && npm run type-check && npm run lint && npm audit

.PHONY: lint-ui
lint-ui: lint-dashboard lint-extension ## Run all UI checks


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
build-installer: manifests generate kustomize ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	# cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default > dist/install.yaml

.PHONY: manifests-release
manifests-release: generate manifests kustomize ## Generate the consolidated install.yaml with the release tag.
	./hack/manifests-release.sh $(KUSTOMIZE) $(IMAGE_TAG)

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
MOCKERY = $(LOCALBIN)/mockery-$(MOCKERY_VERSION)
NILAWAY = $(LOCALBIN)/nilaway-$(NILAWAY_VERSION)
GINKGO = $(LOCALBIN)/ginkgo-$(GINKGO_VERSION)
GORELEASER ?= $(LOCALBIN)/goreleaser-$(GORELEASER_VERSION)

## Tool Versions
KUSTOMIZE_VERSION ?= v5.3.0
CONTROLLER_TOOLS_VERSION ?= v0.19.0
ENVTEST_VERSION ?= release-0.19
GOLANGCI_LINT_VERSION ?= v2.7.2
MOCKERY_VERSION ?= v2.42.2
NILAWAY_VERSION ?= latest
GINKGO_VERSION=$(shell go list -m all | grep github.com/onsi/ginkgo/v2 | awk '{print $$2}')
GORELEASER_VERSION ?= v2.13.2

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

.PHONY: mockery
mockery:
	$(call go-install-tool,$(MOCKERY),github.com/vektra/mockery/v2,${MOCKERY_VERSION})

.PHONY: nilaway
nilaway:
	$(call go-install-tool,$(NILAWAY),go.uber.org/nilaway/cmd/nilaway,${NILAWAY_VERSION})

.PHONY: ginkgo
ginkgo:
	$(call go-install-tool,$(GINKGO),github.com/onsi/ginkgo/v2/ginkgo,${GINKGO_VERSION})

.PHONY: goreleaser
goreleaser: $(GORELEASER)
$(GORELEASER): $(LOCALBIN)
	$(call go-install-tool,$(GORELEASER),github.com/goreleaser/goreleaser/v2,$(GORELEASER_VERSION))

.PHONY: serve-docs
serve-docs:
	$(CONTAINER_TOOL) run ${MKDOCS_RUN_ARGS} --rm -it -p 8000:8000 -v ${CURRENT_DIR}:/docs -w /docs --entrypoint "" ${MKDOCS_DOCKER_IMAGE} sh -c 'pip install -r docs/requirements.txt; mkdocs serve -a $$(ip route get 1 | awk '\''{print $$7}'\''):8000'

.PHONY: lint-docs
lint-docs:  ## Build docs and fail if there are warnings
	@mkdocs build 2>&1 | tee mkdocs-lint.log
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
