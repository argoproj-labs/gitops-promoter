package argocd

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// This lets us use argocd as the package name.
// +versionName=v1alpha1

// +kubebuilder:object:root=true

type Application struct {
	Status            ApplicationStatus `json:"status,omitempty"`
	Spec              ApplicationSpec   `json:"spec"`
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
}

type ApplicationSpec struct {
	Source         *Source         `json:"source,omitempty"`
	SourceHydrator *SourceHydrator `json:"sourceHydrator,omitempty"`
}

type Source struct {
	RepoURL        string `json:"repoURL"`
	TargetRevision string `json:"targetRevision,omitempty"`
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

// GetRepoURL returns the repository URL from either SourceHydrator or Source.
// Returns empty string if neither is configured.
func (a *Application) GetRepoURL() string {
	if a.Spec.SourceHydrator != nil {
		return a.Spec.SourceHydrator.DrySource.RepoURL
	}
	if a.Spec.Source != nil {
		return a.Spec.Source.RepoURL
	}
	return ""
}

// GetEnvironment returns the environment identifier (branch name) from either
// SourceHydrator.SyncSource.TargetBranch or Source.TargetRevision.
// Returns empty string if neither is configured.
func (a *Application) GetEnvironment() string {
	if a.Spec.SourceHydrator != nil {
		return a.Spec.SourceHydrator.SyncSource.TargetBranch
	}
	if a.Spec.Source != nil {
		return a.Spec.Source.TargetRevision
	}
	return ""
}

func init() {
	SchemeBuilder.Register(&Application{}, &ApplicationList{})
}
