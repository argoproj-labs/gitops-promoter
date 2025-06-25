package argocd

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

type Application struct {
	Status            ApplicationStatus `json:"status,omitempty"`
	Spec              ApplicationSpec   `json:"spec"`
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
}

type ApplicationSpec struct {
	SourceHydrator *SourceHydrator `json:"sourceHydrator,omitempty"`
}

type SyncStatus struct {
	Status   SyncStatusCode `json:"status"`
	Revision string         `json:"revision,omitempty"`
}

type SourceHydrator struct {
	DrySource  DrySource  `json:"drySource"`
	SyncSource SyncSource `json:"syncSource"`
}

type DrySource struct {
	RepoURL string `json:"repoURL"`
}

type SyncSource struct {
	TargetBranch string `json:"targetBranch"`
}

type ApplicationStatus struct {
	Health HealthStatus `json:"health,omitempty"`
	Sync   SyncStatus   `json:"sync,omitempty"`
}

// HealthStatus contains information about the currently observed health state of an application or resource
type HealthStatus struct {
	LastTransitionTime *metav1.Time     `json:"lastTransitionTime,omitempty"`
	Status             HealthStatusCode `json:"status,omitempty"`
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

// +kubebuilder:object:root=true

// ApplicationList contains a list of ArgoCDApplications.
type ApplicationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Application `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Application{}, &ApplicationList{})
}
