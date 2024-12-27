package argocd

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// TODO: trim down the struct to only the fields that are needed

type ArgoCDApplication struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
	Spec              ApplicationSpec   `json:"spec"`
	Status            ApplicationStatus `json:"status,omitempty"`
}

type ApplicationSpec struct {
	SourceHydrator *SourceHydrator `json:"sourceHydrator,omitempty"`
}

type SyncStatus struct {
	Status   SyncStatusCode `json:"status"`
	Revision string         `json:"revision,omitempty" protobuf:"bytes,3,opt,name=revision"`
	// Revisions contains information about the revisions of multiple sources the comparison has been performed to
	Revisions []string `json:"revisions,omitempty" protobuf:"bytes,4,opt,name=revisions"`
}

type SourceHydrator struct {
	DrySource  DrySource  `json:"drySource"`
	SyncSource SyncSource `json:"syncSource"`
	HydrateTo  *HydrateTo `json:"hydrateTo,omitempty"`
}

type DrySource struct {
	RepoURL        string `json:"repoURL"`
	TargetRevision string `json:"targetRevision"`
	Path           string `json:"path"`
}

type SyncSource struct {
	TargetBranch string `json:"targetBranch"`
	Path         string `json:"path"`
}

type HydrateTo struct {
	TargetBranch string `json:"targetBranch"`
}

type ApplicationStatus struct {
	Sync           SyncStatus           `json:"sync,omitempty"`
	Health         HealthStatus         `json:"health,omitempty"`
	SourceHydrator SourceHydratorStatus `json:"sourceHydrator,omitempty" protobuf:"bytes,14,opt,name=sourceHydrator"`
}

// HealthStatus contains information about the currently observed health state of an application or resource
type HealthStatus struct {
	Status             HealthStatusCode `json:"status,omitempty"`
	Message            string           `json:"message,omitempty"`
	LastTransitionTime *metav1.Time     `json:"lastTransitionTime,omitempty"`
}

type SourceHydratorStatus struct {
	// LastSuccessfulOperation holds info about the most recent successful hydration
	LastSuccessfulOperation *SuccessfulHydrateOperation `json:"lastSuccessfulOperation,omitempty" protobuf:"bytes,1,opt,name=lastSuccessfulOperation"`
	// CurrentOperation holds the status of the hydrate operation
	CurrentOperation *HydrateOperation `json:"currentOperation,omitempty" protobuf:"bytes,2,opt,name=currentOperation"`
}

// HydrateOperation contains information about the most recent hydrate operation
type HydrateOperation struct {
	// StartedAt indicates when the hydrate operation started
	StartedAt metav1.Time `json:"startedAt,omitempty" protobuf:"bytes,1,opt,name=startedAt"`
	// FinishedAt indicates when the hydrate operation finished
	FinishedAt *metav1.Time `json:"finishedAt,omitempty" protobuf:"bytes,2,opt,name=finishedAt"`
	// Phase indicates the status of the hydrate operation
	Phase HydrateOperationPhase `json:"phase" protobuf:"bytes,3,opt,name=phase"`
	// Message contains a message describing the current status of the hydrate operation
	Message string `json:"message" protobuf:"bytes,4,opt,name=message"`
	// DrySHA holds the resolved revision (sha) of the dry source as of the most recent reconciliation
	DrySHA string `json:"drySHA,omitempty" protobuf:"bytes,5,opt,name=drySHA"`
	// HydratedSHA holds the resolved revision (sha) of the hydrated source as of the most recent reconciliation
	HydratedSHA string `json:"hydratedSHA,omitempty" protobuf:"bytes,6,opt,name=hydratedSHA"`
	// SourceHydrator holds the hydrator config used for the hydrate operation
	SourceHydrator SourceHydrator `json:"sourceHydrator,omitempty" protobuf:"bytes,7,opt,name=sourceHydrator"`
}

type HydrateOperationPhase string

const (
	HydrateOperationPhaseHydrating HydrateOperationPhase = "Hydrating"
	HydrateOperationPhaseFailed    HydrateOperationPhase = "Failed"
	HydrateOperationPhaseHydrated  HydrateOperationPhase = "Hydrated"
)

// SuccessfulHydrateOperation contains information about the most recent successful hydrate operation
type SuccessfulHydrateOperation struct {
	// DrySHA holds the resolved revision (sha) of the dry source as of the most recent reconciliation
	DrySHA string `json:"drySHA,omitempty" protobuf:"bytes,5,opt,name=drySHA"`
	// HydratedSHA holds the resolved revision (sha) of the hydrated source as of the most recent reconciliation
	HydratedSHA string `json:"hydratedSHA,omitempty" protobuf:"bytes,6,opt,name=hydratedSHA"`
	// SourceHydrator holds the hydrator config used for the hydrate operation
	SourceHydrator SourceHydrator `json:"sourceHydrator,omitempty" protobuf:"bytes,7,opt,name=sourceHydrator"`
}

type HealthStatusCode string

const (
	HealthStatusUnknown     HealthStatusCode = "Unknown"
	HealthStatusProgressing HealthStatusCode = "Progressing"
	HealthStatusHealthy     HealthStatusCode = "Healthy"
	HealthStatusSuspended   HealthStatusCode = "Suspended"
	HealthStatusDegraded    HealthStatusCode = "Degraded"
	HealthStatusMissing     HealthStatusCode = "Missing"
)

type SyncStatusCode string

const (
	SyncStatusCodeUnknown   SyncStatusCode = "Unknown"
	SyncStatusCodeSynced    SyncStatusCode = "Synced"
	SyncStatusCodeOutOfSync SyncStatusCode = "OutOfSync"
)
