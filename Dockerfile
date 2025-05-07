# Build the gitops-promoter binary
FROM golang:1.24 AS builder
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
COPY cmd/main.go cmd/main.go
COPY api/ api/
COPY internal/ internal/
COPY hack/git/promoter_askpass.sh hack/git/promoter_askpass.sh

# Build
# the GOARCH has not a default value to allow the binary be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o gitops-promoter cmd/main.go

# Use distroless as minimal base image to package the gitops-promoter binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
#FROM gcr.io/distroless/static:nonroot #TODO: figure out smallest/safest way to get git installed
FROM golang:1.24
WORKDIR /
RUN mkdir /git
COPY --from=builder /workspace/gitops-promoter .
COPY --from=builder /workspace/hack/git/promoter_askpass.sh /git/promoter_askpass.sh
ENV PATH="${PATH}:/git"
RUN echo "${PATH}" >> /etc/bash.bashrc
USER 65532:65532

ENTRYPOINT ["/gitops-promoter"]
