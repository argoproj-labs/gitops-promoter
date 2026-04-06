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
	KubeconfigSecretLabel = "sigs.k8s.io/multicluster-runtime-kubeconfig"
	// KubeconfigSecretKey is the key in the kubeconfig secret that contains the kubeconfig data.
	KubeconfigSecretKey = "kubeconfig"

	// PromotionStrategyControllerFieldOwner is the field owner for Server-Side Apply operations
	// performed by the PromotionStrategy controller.
	PromotionStrategyControllerFieldOwner = "promoter.argoproj.io/promotionstrategy-controller"

	// ChangeTransferPolicyControllerFieldOwner is the field owner for Server-Side Apply operations
	// performed by the ChangeTransferPolicy controller.
	ChangeTransferPolicyControllerFieldOwner = "promoter.argoproj.io/changetransferpolicy-controller"

	// ChangeTransferPolicyProposedHydratedSHAIndexField is the field index path for the
	// proposed branch hydrated commit SHA on a ChangeTransferPolicy resource.
	ChangeTransferPolicyProposedHydratedSHAIndexField = ".status.proposed.hydrated.sha"

	// ChangeTransferPolicyActiveHydratedSHAIndexField is the field index path for the
	// active branch hydrated commit SHA on a ChangeTransferPolicy resource.
	ChangeTransferPolicyActiveHydratedSHAIndexField = ".status.active.hydrated.sha"

	// ArgoCDCommitStatusControllerFieldOwner is the field owner for Server-Side Apply operations
	// performed by the ArgoCDCommitStatus controller.
	ArgoCDCommitStatusControllerFieldOwner = "promoter.argoproj.io/argocdcommitstatus-controller"

	// TimedCommitStatusControllerFieldOwner is the field owner for Server-Side Apply operations
	// performed by the TimedCommitStatus controller.
	TimedCommitStatusControllerFieldOwner = "promoter.argoproj.io/timedcommitstatus-controller"

	// WebRequestCommitStatusControllerFieldOwner is the field owner for Server-Side Apply operations
	// performed by the WebRequestCommitStatus controller.
	WebRequestCommitStatusControllerFieldOwner = "promoter.argoproj.io/webrequestcommitstatus-controller"
)
