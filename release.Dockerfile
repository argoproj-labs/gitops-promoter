FROM golang:1.26.6@sha256:640a234f4bea3e399c056b7b8f9c667c4939befae8db2f14e9785e16eccd4205

ARG TARGETPLATFORM

WORKDIR /

# Install tini to handle process management and prevent process leaks
RUN apt-get update && apt-get install -y tini && apt-get clean && rm -rf /var/lib/apt/lists/*

COPY ${TARGETPLATFORM}/gitops-promoter .
RUN mkdir /git
COPY hack/git/promoter_askpass.sh /git/promoter_askpass.sh
ENV PATH="${PATH}:/git"
RUN echo "${PATH}" >> /etc/bash.bashrc
USER 65532:65532
ENTRYPOINT ["/usr/bin/tini", "--", "/gitops-promoter"]