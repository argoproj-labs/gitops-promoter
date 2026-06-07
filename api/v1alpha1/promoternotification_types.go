/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// NotificationEventType is the type of event that a PromoterNotification subscribes to.
// This is an extensible enum: new event types may be added over time, so consumers
// must tolerate unknown values rather than assuming the set is closed.
// +kubebuilder:validation:Enum=PromotionComplete;GateFailed;GateStale;CTPProposed;CTPActive
type NotificationEventType string

const (
	// NotificationEventPromotionComplete fires when a promotion to an environment completes.
	NotificationEventPromotionComplete NotificationEventType = "PromotionComplete"
	// NotificationEventGateFailed fires when a commit-status gate transitions to failure.
	NotificationEventGateFailed NotificationEventType = "GateFailed"
	// NotificationEventGateStale fires when a commit-status gate is stale (e.g. not updated within its expected window).
	NotificationEventGateStale NotificationEventType = "GateStale"
	// NotificationEventCTPProposed fires when a ChangeTransferPolicy proposes a new dry sha on the proposed branch.
	NotificationEventCTPProposed NotificationEventType = "CTPProposed"
	// NotificationEventCTPActive fires when a ChangeTransferPolicy's active branch advances to a new sha.
	NotificationEventCTPActive NotificationEventType = "CTPActive"
)

// HTTPMethod is the HTTP method used to deliver a webhook.
// +kubebuilder:validation:Enum=POST;PUT;PATCH
type HTTPMethod string

const (
	// HTTPMethodPost delivers the webhook via HTTP POST. This is the default.
	HTTPMethodPost HTTPMethod = "POST"
	// HTTPMethodPut delivers the webhook via HTTP PUT.
	HTTPMethodPut HTTPMethod = "PUT"
	// HTTPMethodPatch delivers the webhook via HTTP PATCH.
	HTTPMethodPatch HTTPMethod = "PATCH"
)

// BackoffPolicy describes how delivery retries are spaced.
// +kubebuilder:validation:Enum=Fixed;Exponential
type BackoffPolicy string

const (
	// BackoffFixed waits a constant duration between retries.
	BackoffFixed BackoffPolicy = "Fixed"
	// BackoffExponential doubles the wait duration between successive retries.
	BackoffExponential BackoffPolicy = "Exponential"
)

// PromoterNotificationSpec defines the desired state of PromoterNotification.
type PromoterNotificationSpec struct {
	// EventTypes is the set of event types this notification subscribes to.
	// At least one event type must be specified. This is an extensible enum; consumers
	// must tolerate event types they do not recognize.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +listType=set
	EventTypes []NotificationEventType `json:"eventTypes"`

	// Selector is a label selector matched against the originating custom resource
	// (e.g. the PromotionStrategy, ChangeTransferPolicy, or CommitStatus that produced the event).
	// When omitted (nil), all originating resources match.
	// +optional
	Selector *metav1.LabelSelector `json:"selector,omitempty"`

	// Delivery describes how matched events are delivered.
	// +kubebuilder:validation:Required
	Delivery NotificationDelivery `json:"delivery"`

	// Variables is an optional set of named expressions evaluated against the event before the
	// payloadTemplate is rendered. Each value is an expr-lang expression
	// (https://expr-lang.org); the result is exposed to payloadTemplate as {{ .Vars.<name> }}.
	// This lets templates stay simple — compute structured or conditional values here (e.g.
	// `event.type == "GateFailed" ? "critical" : "info"`, or `event.newSha[:7]`) instead of
	// expressing logic in Go text/template. The expression environment exposes the event as
	// `event` with lower-camelCase string fields: event.type, event.object (.namespace/.name/
	// .kind/.apiVersion/.uid), event.labels, event.environment, event.gateName, event.previousSha,
	// event.newSha, event.previousState, event.newState, event.activeURL, event.proposedURL — plus
	// a top-level `eventID`. (event.type is a plain string, so `event.type == "GateFailed"` works.)
	// A variable that fails to evaluate is a configuration error (surfaced as a failing
	// condition), not a transient delivery failure.
	// +optional
	Variables map[string]string `json:"variables,omitempty"`

	// PayloadTemplate is a Go text/template rendered against the event to produce the
	// request body. The event fields (CR ref, labels, environment, sha, URLs, prev/new state,
	// timestamps, eventID) are exposed as template data, and any Variables results are exposed
	// as {{ .Vars.<name> }}. This is NOT a CEL expression.
	// When empty, a default JSON serialization of the event (including evaluated Variables) is delivered.
	// +optional
	PayloadTemplate string `json:"payloadTemplate,omitempty"`
}

// NotificationDelivery describes the delivery mechanism for a PromoterNotification.
// It is a discriminated container; currently only webhook delivery is supported.
type NotificationDelivery struct {
	// Webhook delivers the notification to an HTTP endpoint.
	// +kubebuilder:validation:Required
	Webhook *WebhookDelivery `json:"webhook"`
}

// WebhookDelivery configures HTTP webhook delivery.
type WebhookDelivery struct {
	// URL is the HTTP(S) endpoint to deliver the webhook to.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^https?://.+`
	URL string `json:"url"`

	// Method is the HTTP method used for delivery.
	// +optional
	// +kubebuilder:default=POST
	Method HTTPMethod `json:"method,omitempty"`

	// Headers is a set of static HTTP headers to send with every request.
	// Values are sent verbatim; do not place secrets here (use signing.secretRef instead).
	// +optional
	Headers map[string]string `json:"headers,omitempty"`

	// Signing, when set, signs the rendered payload using an HMAC secret and adds a
	// signature header so the receiver can verify authenticity.
	// +optional
	Signing *WebhookSigning `json:"signing,omitempty"`

	// TimeoutSeconds is the per-attempt request timeout in seconds.
	// +optional
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=300
	TimeoutSeconds int32 `json:"timeoutSeconds,omitempty"`

	// Retry configures retry behavior for failed deliveries.
	// +optional
	Retry *WebhookRetry `json:"retry,omitempty"`
}

// WebhookSigning configures HMAC signing of the webhook payload.
type WebhookSigning struct {
	// SecretRef references a Secret (in the same namespace as the PromoterNotification)
	// whose Key holds the HMAC signing secret.
	// +kubebuilder:validation:Required
	SecretRef SecretKeyReference `json:"secretRef"`
}

// SecretKeyReference references a specific key within a Secret in the same namespace.
type SecretKeyReference struct {
	// Name is the name of the Secret.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`
	Name string `json:"name"`

	// Key is the key within the Secret's data that holds the value.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// WebhookRetry configures retry behavior for failed webhook deliveries.
type WebhookRetry struct {
	// MaxAttempts is the total number of delivery attempts (including the first).
	// +optional
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10
	MaxAttempts int32 `json:"maxAttempts,omitempty"`

	// Backoff is the backoff policy applied between attempts.
	// +optional
	// +kubebuilder:default=Exponential
	Backoff BackoffPolicy `json:"backoff,omitempty"`
}

// PromoterNotificationStatus defines the observed state of PromoterNotification.
type PromoterNotificationStatus struct {
	// ObservedGeneration is the .metadata.generation that this status was reconciled from.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastDelivery describes the most recent delivery attempt.
	// +optional
	LastDelivery *NotificationDeliveryStatus `json:"lastDelivery,omitempty"`

	// TotalDelivered is the cumulative number of successfully delivered events.
	// +optional
	TotalDelivered int64 `json:"totalDelivered,omitempty"`

	// TotalFailed is the cumulative number of events that failed delivery after exhausting retries.
	// +optional
	TotalFailed int64 `json:"totalFailed,omitempty"`

	// Conditions represents the observations of the current state.
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// NotificationDeliveryStatus records the outcome of a single delivery attempt.
type NotificationDeliveryStatus struct {
	// Time is when the delivery attempt occurred.
	// +optional
	Time *metav1.Time `json:"time,omitempty"`

	// EventID is the stable identifier of the event that was delivered.
	// +optional
	EventID string `json:"eventID,omitempty"`

	// ResponseStatus is the HTTP status code returned by the receiver, or 0 if no response
	// was received (for example on connection or timeout errors).
	// +optional
	ResponseStatus int32 `json:"responseStatus,omitempty"`
}

// GetConditions returns the conditions of the PromoterNotification.
func (n *PromoterNotification) GetConditions() *[]metav1.Condition {
	return &n.Status.Conditions
}

// SetObservedGeneration records the object generation that produced the current status.
func (n *PromoterNotification) SetObservedGeneration(generation int64) {
	n.Status.ObservedGeneration = generation
}

// +kubebuilder:ac:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// PromoterNotification is the Schema for the promoternotifications API.
// +kubebuilder:printcolumn:name="Delivered",type=integer,JSONPath=`.status.totalDelivered`,priority=1
// +kubebuilder:printcolumn:name="Failed",type=integer,JSONPath=`.status.totalFailed`,priority=1
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
type PromoterNotification struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PromoterNotificationSpec   `json:"spec,omitempty"`
	Status PromoterNotificationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PromoterNotificationList contains a list of PromoterNotification.
type PromoterNotificationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PromoterNotification `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &PromoterNotification{}, &PromoterNotificationList{})
		return nil
	})
}
