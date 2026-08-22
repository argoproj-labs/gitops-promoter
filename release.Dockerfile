FROM golang:1.27.0@sha256:65b6f280bf050ec5af12716857e8ea8439d694dbba8f31ceeb7630670071f2bb

WORKDIR /

# Install tini to handle process management and prevent process leaks
RUN apt-get update && apt-get install -y tini && apt-get clean && rm -rf /var/lib/apt/lists/*

# goreleaser runs docker build in a context that contains just the Dockerfile and the binary.

COPY gitops-promoter .
RUN mkdir /git
COPY hack/git/promoter_askpass.sh /git/promoter_askpass.sh
ENV PATH="${PATH}:/git"
RUN echo "${PATH}" >> /etc/bash.bashrc
USER 65532:65532
ENTRYPOINT ["/usr/bin/tini", "--", "/gitops-promoter"]