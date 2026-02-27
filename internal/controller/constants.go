package controller

const (
	// KubeConfigProviderName is the name of the kubeconfig cluster provider used by multicluster-runtime to connect to
	// clusters using kubeconfig files.
	KubeConfigProviderName = "kubeconfig"
	// LocalProviderName is the name of the local cluster provider used by multicluster-runtime to connect to the local
	// cluster.
	LocalProviderName = "local"
)
