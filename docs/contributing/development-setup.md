# Development Setup

This page will cover the different ways to run the project locally on a
local Kubernetes cluster.

## Prerequisites

The following tools are needed for local development.

- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Go](https://go.dev/doc/install)
- [Node.js](https://nodejs.org/en/download)
- A Local Kubernetes Cluster
    - [Kind](https://kind.sigs.k8s.io/)
    - [Minikube](https://minikube.sigs.k8s.io/docs/)
    - [K3s](https://k3s.io/)
- [Tilt](https://docs.tilt.dev/install.html) (Optional)

## Tilt

Tilt is the easiest way to get the development environment started. It will automatically build
your changes and deploy the needed resources to the cluster. The only thing it will not deploy is the
aggregation API server. Refer to the section below to learn how to deploy that. Tilt is started by
running the following command in the project's directory.

```bash
tilt up
```

After running Tilt's Web UI will be available for you to see the logs and status of everything that was deployed.
When you are finished run the command below to clean up your environment.

```bash
tilt down
```

## In cluster

This section will cover deploying the components into a cluster for testing.

### Building The Image

Before running any of the components in cluster you need to build and push an image containing your
changes. The following two make targets will allow for this as long as your container tool is signed into the
registry you intend to push to. Docker is the default and it can be changed with the `CONTAINER_TOOL` environment
variable.

```bash
IMG=someregistry.io/some-usr/gitops-promoter:latest make docker-build
IMG=someregistry.io/some-usr/gitops-promoter:latest make docker-push
```

### Controller

Deploying the controller can simply be done with the below make target. The CRDs will be deployed alongside the
controller.

```bash
IMG=someregistry.io/some-usr/gitops-promoter:latest make deploy
```

### API Server

Deploying the API Server depends on what you are looking for. You can either deploy it insecurely or with generated certs. 

#### Insecure

```bash
kubectl apply -k config/apiserver/dev-insecure
kubectl -n promoter-system set image deploy/promoter-apiserver apiserver=someregistry.io/some-usr/gitops-promoter:latest
```

#### With Generated Certs

```bash
kubectl apply -k config/apiserver/certs-manual
make apiserver-certs
kubectl -n promoter-system set image deploy/promoter-apiserver apiserver=someregistry.io/some-usr/gitops-promoter:latest
```

## Out of cluster

This section will cover running the components locally on your system against a cluster. To run anything for
the project you will need to have the CRDs installed in the cluster. This can be done with the following make
target.

```bash
make install
```

### Controller

The below make target will build and run the controller. When you make a change you will need to restart the
process.

```bash
make run
```

### API Server

To run the API Server locally you first must register it with the following make target. The port can be set with the
`PORT` environment variable. This routes the in-cluster APIService to your locally running instance of the api server.

```bash
make apiserver-register-local
```

Once registered you can run the api server with the following command.
```bash
make run-apiserver
```

When finished you can unregister with the local APIService registration by running the below make target.

```bash
make apiserver-unregister-local
```

## Dashboard

Below are the two ways you can run the dashboard for development. The dev version enables hot reloading to make
testing out changes easier.

```bash
make run-dashboard
make run-dashboard-dev
```