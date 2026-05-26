FROM golang:1.26.3@sha256:e3665e241a474aba30bbfaf177cfa88e1913e970c83bd86889cacfb67d6e7e51

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