# Build the dashboard UI
FROM node:24.20.0-bullseye-slim@sha256:f7818cba7c85740e6f9dd31553636131d01e9992fb7d112b9b0893afd884d1d6 AS dashboard-builder
WORKDIR /workspace

# Copy package files first for better layer caching
COPY ui/components-lib/package*.json ./ui/components-lib/
COPY ui/dashboard/package*.json ./ui/dashboard/

# Copy all UI source files
COPY ui/ ./ui/

# Install components-lib dependencies first (use ci for reproducible install from lock file)
WORKDIR /workspace/ui/components-lib
RUN npm ci

# Install dashboard dependencies (application - has lock file)
WORKDIR /workspace/ui/dashboard
RUN npm ci

# Build the dashboard
WORKDIR /workspace/ui/dashboard
RUN npx vite build
RUN mkdir -p ../web/static && cp -r dist/* ../web/static/

# Build the gitops-promoter binary
FROM golang:1.27.2@sha256:820153096fda4de6ac1b7194d12e416b7dd8f4e9a33d17e0345b111b945162d8 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY cmd/ cmd/
COPY api/ api/
COPY applyconfiguration/ applyconfiguration/
COPY internal/ internal/
COPY hack/git/promoter_askpass.sh hack/git/promoter_askpass.sh
COPY ui/web/ ./ui/web/

# Copy the built static files from dashboard-builder for embedding
COPY --from=dashboard-builder /workspace/ui/web/static ./ui/web/static

# Build
# the GOARCH has not a default value to allow the binary be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
# COVER=1 instruments the binary for an e2e coverage upload. Building the package (./cmd) is required for
# that: building the file cmd/main.go does not instrument main. The release image leaves COVER at 0.
ARG COVER=0
RUN set -eu; \
    extra=""; target="cmd/main.go"; \
    if [ "$COVER" = "1" ]; then \
      extra="-cover -covermode=atomic -coverpkg=github.com/argoproj-labs/gitops-promoter/..."; \
      target="./cmd"; \
    fi; \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a $extra -o gitops-promoter $target

# Use distroless as minimal base image to package the gitops-promoter binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
#FROM gcr.io/distroless/static:nonroot #TODO: figure out smallest/safest way to get git installed
FROM golang:1.27.2@sha256:820153096fda4de6ac1b7194d12e416b7dd8f4e9a33d17e0345b111b945162d8
WORKDIR /

# Install tini to handle process management and prevent process leaks
RUN apt-get update && apt-get install -y tini && apt-get clean && rm -rf /var/lib/apt/lists/*

RUN mkdir /git
COPY --from=builder /workspace/gitops-promoter .
COPY --from=builder /workspace/hack/git/promoter_askpass.sh /git/promoter_askpass.sh
ENV PATH="${PATH}:/git"
RUN echo "${PATH}" >> /etc/bash.bashrc
USER 65532:65532

ENTRYPOINT ["/usr/bin/tini", "--", "/gitops-promoter"]
