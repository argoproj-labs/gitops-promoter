# Getting Started

This guide will help you get started installing and setting up the GitOps Promoter. We currently only support
Github and Github Enterprise as the SCM providers. We would welcome any contributions to add support for other
providers.

## Requirements

* kubectl CLI
* kustomize CLI
* kubernetes cluster
* Github or Github Enterprise Application

## Installation

To install the GitOps Promoter, you can use the following command:

```bash
kubectl apply -f https://raw.githubusercontent.com/argoproj-labs/gitops-promoter/main/manifests/install.yaml
```