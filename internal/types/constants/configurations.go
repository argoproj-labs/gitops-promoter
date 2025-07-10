package constants

import "time"

const (
	// EventuallyTimeout is the default timeout for eventually assertions in tests.
	EventuallyTimeout = 90 * time.Second
	// WebhookReceiverPort is the port on which the webhook receiver listens for incoming HTTP requests.
	WebhookReceiverPort = 3333
	// KubeconfigSecretNamespace is the namespace where the kubeconfig secret is stored.
	KubeconfigSecretNamespace = "default"
	// KubeconfigSecretLabel is the label used to identify the kubeconfig secret.
	KubeconfigSecretLabel = "kubeconfig"
	// KubeconfigSecretKey is the key in the kubeconfig secret that contains the kubeconfig data.
	KubeconfigSecretKey = "kubeconfig"
)
