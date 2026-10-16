# Test SCM server image for e2e tests and local development. Build from the repository root:
#   docker build -f test/scmserver/Dockerfile -t scm-server:e2e .
FROM golang:1.27.1@sha256:512690a5660563b57d37ecc31129e7f136e831db2aed24a1dbeb8ad7380dc0fa AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
COPY go.mod go.mod
COPY go.sum go.sum
COPY test/scmserver/ test/scmserver/
# Only the few modules the server imports are downloaded, rather than the whole module graph.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -o scm-server ./test/scmserver/cmd

FROM alpine:3.22
# git-daemon provides git-http-backend, which serves the hosted repositories over smart HTTP.
RUN apk add --no-cache git git-daemon tini && mkdir /data && chown 65532:65532 /data
COPY --from=builder /workspace/scm-server /scm-server
USER 65532:65532
VOLUME /data
EXPOSE 9000 9001 9002 9003 9004 9005 9006
ENTRYPOINT ["/sbin/tini", "--", "/scm-server", "--data-dir=/data"]
