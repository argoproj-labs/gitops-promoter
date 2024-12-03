FROM gcr.io/distroless/static:nonroot

WORKDIR /

# goreleaser runs docker build in a context that contains just the Dockerfile and the binary.

COPY gitops-promoter .
USER 65532:65532
ENTRYPOINT ["/gitops-promoter"]
