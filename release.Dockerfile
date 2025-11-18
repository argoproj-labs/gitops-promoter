FROM golang:1.25

WORKDIR /

# Install tini to handle process management and prevent process leaks
RUN apt-get update && apt-get install -y tini && rm -rf /var/lib/apt/lists/*

# goreleaser runs docker build in a context that contains just the Dockerfile and the binary.

COPY gitops-promoter .
RUN mkdir /git
COPY hack/git/promoter_askpass.sh /git/promoter_askpass.sh
ENV PATH="${PATH}:/git"
RUN echo "${PATH}" >> /etc/bash.bashrc
USER 65532:65532
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["/gitops-promoter"]