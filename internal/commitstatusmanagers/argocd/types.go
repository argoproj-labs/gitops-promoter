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
	Status    SyncStatusCode `json:"status"`
	Revision  string         `json:"revision,omitempty"`
	Revisions []string       `json:"revisions,omitempty"`
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
	SourceHydrator SourceHydratorStatus `json:"sourceHydrator,omitempty"`
}

// HealthStatus contains information about the currently observed health state of an application or resource
type HealthStatus struct {
	Status             HealthStatusCode `json:"status,omitempty"`
	Message            string           `json:"message,omitempty"`
	LastTransitionTime *metav1.Time     `json:"lastTransitionTime,omitempty"`
}

type SourceHydratorStatus struct {
	LastSuccessfulOperation *SuccessfulHydrateOperation `json:"lastSuccessfulOperation,omitempty"`
	CurrentOperation        *HydrateOperation           `json:"currentOperation,omitempty"`
}

// HydrateOperation contains information about the most recent hydrate operation
type HydrateOperation struct {
	StartedAt      metav1.Time           `json:"startedAt,omitempty"`
	FinishedAt     *metav1.Time          `json:"finishedAt,omitempty"`
	Phase          HydrateOperationPhase `json:"phase"`
	Message        string                `json:"message"`
	DrySHA         string                `json:"drySHA,omitempty"`
	HydratedSHA    string                `json:"hydratedSHA,omitempty"`
	SourceHydrator SourceHydrator        `json:"sourceHydrator,omitempty"`
}

type HydrateOperationPhase string

const (
	HydrateOperationPhaseHydrating HydrateOperationPhase = "Hydrating"
	HydrateOperationPhaseFailed    HydrateOperationPhase = "Failed"
	HydrateOperationPhaseHydrated  HydrateOperationPhase = "Hydrated"
)

// SuccessfulHydrateOperation contains information about the most recent successful hydrate operation
type SuccessfulHydrateOperation struct {
	DrySHA         string         `json:"drySHA,omitempty"`
	HydratedSHA    string         `json:"hydratedSHA,omitempty"`
	SourceHydrator SourceHydrator `json:"sourceHydrator,omitempty"`
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
