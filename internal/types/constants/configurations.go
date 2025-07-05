package constants

import "time"

const (
	EventuallyTimeout         = 90 * time.Second
	WebhookReceiverPort       = 3333
	KubeconfigSecretNamespace = "default"
	KubeconfigSecretLabel     = "kubeconfig"
	KubeconfigSecretKey       = "kubeconfig"
)
