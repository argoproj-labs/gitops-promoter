// Package tekon defines API Schemas for tekton.dev/v1
package tekton

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Data represents a Tekton input or output
type Data struct {
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
	Type  string `json:"type,omitempty"`
}

// ObjectReference represents a reference to a Pipeline or Task by name that is in the same
// namespace as the PipelineRun or TaskRun.
type ObjectReference struct {
	Name string `json:"name,omitempty"`
}

// +versionName=v1

// +kubebuilder:object:root=true

// TaskRun is an object that runs a specific Task
type TaskRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   TaskRunSpec     `json:"spec"`
	Status TektonRunStatus `json:"status,omitempty"`
}

type TaskRunSpec struct {
	ServiceAccountName string          `json:"serviceAccountName,omitempty"`
	TaskRef            ObjectReference `json:"taskRef,omitempty"`
	Params             []Data          `json:"params,omitempty"`
}

// +kubebuilder:object:root=true

// PipelineRun is an object that runs a specific pipeline
type PipelineRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   PipelineRunSpec `json:"spec"`
	Status TektonRunStatus `json:"status,omitempty"`
}

type PipelineRunSpec struct {
	TaskRunTemplate PipelineTaskRunTemplate `json:"taskRunTemplate,omitempty"`
	PipelineRef     ObjectReference         `json:"pipelineRef,omitempty"`
	Params          []Data                  `json:"params,omitempty"`
}

type PipelineTaskRunTemplate struct {
	ServiceAccountName string `json:"serviceAccountName,omitempty"`
}

// TektonRunStatus is an object that represents the status of a TaskRun or PipelineRun
type TektonRunStatus struct {
	Conditions []metav1.Condition `json:"conditions"`
	Results    []Data             `json:"results,omitempty"`
}

// +kubebuilder:object:root=true

type CustomRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`
}

// The below constants represent the different run phases for a Tekton resource
const (
	TektonConditionType     = "Succeeded"
	TektonRunPhaseSucceeded = "Succeeded"
	TektonRunPhaseFailed    = "Failed"
	TektonRunPhaseRunning   = "Running"
)

// The following constants represent the different Kind of runs that exist
const (
	TektonKindTask     = "Task"
	TektonKindPipeline = "Pipeline"
)

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &TaskRun{}, &PipelineRun{})
		return nil
	})
}
