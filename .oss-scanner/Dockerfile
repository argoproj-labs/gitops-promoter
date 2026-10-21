# syntax=docker/dockerfile:1
# Scanner image for https://github.com/argoproj-labs/gitops-promoter.
#
# The audit runs offline. Setup is the toolchain; the repository Makefile does
# the build and the tests. threat_model.md says how to exercise the result.
#
# Dependency stages are separate from the source copy, so a change that does not
# touch go.mod, go.sum, the Makefile tool versions, or the UI package files
# reuses the module cache, envtest binaries, and node_modules.

FROM node:24.20.0-bullseye-slim@sha256:f7818cba7c85740e6f9dd31553636131d01e9992fb7d112b9b0893afd884d1d6 AS node

# Same image the repository's Dockerfile pins.
FROM golang:1.27.2@sha256:820153096fda4de6ac1b7194d12e416b7dd8f4e9a33d17e0345b111b945162d8 AS base

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates git make \
    && rm -rf /var/lib/apt/lists/* \
    && git config --global user.email "scanner@example.com" \
    && git config --global user.name "OSS Scanner"

# Optional. A TLS-intercepting proxy can supply its CA with
# --secret id=extra-ca,src=ca.pem. One file per certificate so the trust
# store indexes each one. The scanner does not set this secret.
RUN --mount=type=secret,id=extra-ca,required=false,target=/tmp/extra-ca.pem \
    if [ -s /tmp/extra-ca.pem ]; then \
      awk 'BEGIN { n = 0 } \
        /BEGIN CERTIFICATE/ { n++; close(out); out = sprintf("/usr/local/share/ca-certificates/extra-%02d.crt", n) } \
        out { print > out }' /tmp/extra-ca.pem \
      && update-ca-certificates; \
    fi

COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -sf ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
    && ln -sf ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx

# Node ships its own CA bundle. --use-system-ca makes npm use the image trust
# store, including an extra CA when one was installed above.
ENV NODE_OPTIONS=--use-system-ca

FROM base AS modules
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

FROM base AS ui
WORKDIR /src
COPY Makefile ./
COPY ui/shared/package.json ui/shared/package-lock.json ui/shared/
COPY ui/components-lib/package.json ui/components-lib/
COPY ui/dashboard/package.json ui/dashboard/package-lock.json ui/dashboard/
COPY ui/extension/package.json ui/extension/package-lock.json ui/extension/
# install-ui-deps covers ui/shared and ui/components-lib. The dashboard and
# extension targets install their own dependencies before building.
RUN --mount=type=cache,target=/root/.npm \
    make install-ui-deps \
    && cd ui/dashboard && npm install \
    && cd /src/ui/extension && npm install

FROM base AS tools
WORKDIR /src
COPY --from=modules /go/pkg/mod /go/pkg/mod
COPY Makefile go.mod go.sum ./
RUN --mount=type=cache,target=/root/.cache/go-build \
    make setup-envtest controller-gen \
    && make ginkgo

FROM base
WORKDIR /src
COPY --from=modules /go/pkg/mod /go/pkg/mod
COPY --from=tools /src/bin /src/bin
COPY --from=ui /src/ui /src/ui

# scanner contract: the checkout lives inside the image, at /src
COPY . .

# Dashboard, Argo CD extension, then the manager binary (bin/manager).
RUN --mount=type=cache,target=/root/.cache/go-build \
    make build-all \
    && ln -sfn /src/bin/manager /usr/local/bin/gitops-promoter

# Same suite CI runs. A failure does not abort the image build.
RUN --mount=type=cache,target=/root/.cache/go-build \
    make test-parallel || echo "WARNING: make test-parallel reported failures"
RUN make test-ui-test-dashboard test-unit-test-extension \
    || echo "WARNING: UI tests reported failures"

# Everything is fetched. From here on the go command must not reach a proxy or download a toolchain.
ENV GOPROXY=off \
    GOTOOLCHAIN=local
