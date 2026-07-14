FROM golang:1.26.5@sha256:63f132d58c1f589f0dcda584933a9bb44bfda1150f1506377f5a902f34d86033

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