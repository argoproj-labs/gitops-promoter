# Build the dashboard UI
FROM node:18-bullseye-slim AS dashboard-builder
WORKDIR /workspace
COPY ui/ ./ui/
WORKDIR /workspace/ui/dashboard
RUN npm ci
RUN npm run build:embed

# Final image
FROM golang:1.24

WORKDIR /

# goreleaser runs docker build in a context that contains just the Dockerfile and the binary.
COPY gitops-promoter .
RUN mkdir /git
COPY hack/git/promoter_askpass.sh /git/promoter_askpass.sh
COPY --from=dashboard-builder /workspace/web/static ./web/static
ENV PATH="${PATH}:/git"
RUN echo "${PATH}" >> /etc/bash.bashrc
USER 65532:65532
ENTRYPOINT ["/gitops-promoter"]
