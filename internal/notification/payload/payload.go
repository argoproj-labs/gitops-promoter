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

// Package payload turns a notification Event into the bytes that get POSTed to a
// webhook receiver, and optionally signs those bytes so the receiver can verify
// authenticity.
//
// It is the seam the delivery layer calls just before performing the HTTP request:
//
//	body, headers, err := payload.BuildRequest(ctx, reader, notification, event)
//	// build req with body; for k, v := range headers { req.Header.Set(k, v) }
//
// Two responsibilities live here:
//
//  1. Rendering. spec.payloadTemplate is rendered as a Go text/template (NOT CEL)
//     against the event. When the template is empty, a stable JSON serialization of
//     the event is used instead. Parse/exec errors are returned to the caller so the
//     controller can surface them as a condition rather than crashing.
//
//  2. Signing. When spec.delivery.webhook.signing is set, the rendered body is signed
//     with HMAC-SHA256 using a secret read from the referenced Secret, and the
//     signature is returned as the X-Promoter-Signature header.
//
// # Signature format
//
// The signature header is:
//
//	X-Promoter-Signature: sha256=<lowercase-hex>
//
// where <lowercase-hex> is the hex encoding of HMAC-SHA256(secret, body). This is the
// same shape GitHub uses for its X-Hub-Signature-256 header, so existing receiver code
// and tooling transfer directly. Receivers MUST compare using a constant-time
// comparison (e.g. hmac.Equal) over the full "sha256=<hex>" value or over the decoded
// bytes. See VerifySignature for a reference implementation and the package tests for
// known-answer test vectors.
package payload

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/expr-lang/expr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/notification/events"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// SignatureHeader is the HTTP header carrying the HMAC signature of the request body.
const SignatureHeader = "X-Promoter-Signature"

// signaturePrefix is prepended to the hex digest in the signature header value, e.g.
// "sha256=abcd...". It names the algorithm so receivers and future algorithms can be
// distinguished, matching GitHub's X-Hub-Signature-256 convention.
const signaturePrefix = "sha256="

// TemplateData is the value exposed to spec.payloadTemplate. It embeds the event by
// value (so all event fields are reachable as {{ .Type }}, {{ .Environment }},
// {{ .NewSha }}, {{ .Labels.foo }}, etc.) and adds template-friendly accessors for
// values that are otherwise methods or need formatting.
//
// Exposed to templates:
//   - All events.Event fields: {{ .Type }} {{ .Object.Name }} {{ .Labels }}
//     {{ .Environment }} {{ .GateName }} {{ .PreviousSha }} {{ .NewSha }}
//     {{ .PreviousState }} {{ .NewState }} {{ .ActiveURL }} {{ .ProposedURL }}
//     {{ .OccurredAt }}
//   - {{ .EventID }}        stable event identifier (string)
//   - {{ .OccurredAtRFC3339 }} OccurredAt formatted as RFC3339
//   - {{ .Vars.<name> }}    results of spec.variables expressions (nil when none configured)
//
// Note: events.Event.EventID is a method, so {{ .EventID }} would also resolve on the
// bare event; we expose it as a field here so it is computed once and is unambiguous.
type TemplateData struct {
	events.Event

	// Vars holds the evaluated spec.variables (name -> result), reachable as {{ .Vars.<name> }}.
	// It is nil when spec.variables is empty.
	Vars map[string]any `json:"vars,omitempty"`

	// EventID is the stable, deterministic identifier of the event.
	EventID string `json:"eventID"`
	// OccurredAtRFC3339 is Event.OccurredAt formatted as RFC3339 for convenient
	// embedding in templates without needing a date function.
	OccurredAtRFC3339 string `json:"occurredAtRFC3339"`
}

// newTemplateData builds the template data view for an event. EventID is computed via
// the event's EventID() so it matches the identifier used everywhere else for dedup.
func newTemplateData(event events.Event) TemplateData {
	return TemplateData{
		Event:             event,
		EventID:           event.EventID(),
		OccurredAtRFC3339: event.OccurredAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

// Render produces the request body for an event. When notification.spec.payloadTemplate
// is set, it is rendered as a Go text/template against TemplateData. When it is empty,
// a deterministic JSON serialization of the TemplateData is returned instead.
//
// Template parse and execution errors are returned (wrapped) so the caller can surface
// them as a status condition; Render never panics on bad templates. The "missingkey=zero"
// option is used so referencing an absent map key (e.g. {{ .Labels.nope }}) renders an
// empty value rather than erroring, keeping templates tolerant of optional fields.
func Render(notification *v1alpha1.PromoterNotification, event events.Event) ([]byte, error) {
	if notification == nil {
		return nil, errors.New("notification is nil")
	}

	data := newTemplateData(event)

	// Evaluate spec.variables (if any) against the event before rendering, exposing the results
	// as {{ .Vars.<name> }}. A bad expression is a configuration error, surfaced to the caller.
	vars, err := evaluateVariables(notification.Spec.Variables, data)
	if err != nil {
		return nil, err
	}
	data.Vars = vars

	tmplStr := notification.Spec.PayloadTemplate
	if tmplStr == "" {
		// Default payload: stable JSON of the event view (includes the evaluated Vars).
		body, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal default event payload: %w", err)
		}
		return body, nil
	}

	rendered, err := utils.RenderStringTemplate(tmplStr, data, "missingkey=zero")
	if err != nil {
		return nil, fmt.Errorf("failed to render payloadTemplate: %w", err)
	}
	return []byte(rendered), nil
}

// eventVars is the event view exposed to expressions as `event`. It deliberately uses plain
// string fields (not the named NotificationEventType) so natural comparisons like
// `event.type == "GateFailed"` type-check in expr. Field names are lower-cased via the expr tag
// to read naturally in expressions.
type eventVars struct {
	Object        events.ObjectRef  `expr:"object"`
	Labels        map[string]string `expr:"labels"`
	Type          string            `expr:"type"`
	Environment   string            `expr:"environment"`
	GateName      string            `expr:"gateName"`
	PreviousSha   string            `expr:"previousSha"`
	NewSha        string            `expr:"newSha"`
	PreviousState string            `expr:"previousState"`
	NewState      string            `expr:"newState"`
	ActiveURL     string            `expr:"activeURL"`
	ProposedURL   string            `expr:"proposedURL"`
	EventID       string            `expr:"eventID"`
}

// variableEnv is the expression environment exposed to spec.variables expressions. The event is
// reachable as `event` and its stable identifier as `eventID`.
type variableEnv struct {
	Event   eventVars `expr:"event"`
	EventID string    `expr:"eventID"`
}

// newVariableEnv builds the expression environment from the template data, presenting the event in
// expr-friendly (plain-string) types.
func newVariableEnv(data TemplateData) variableEnv {
	e := data.Event
	return variableEnv{
		Event: eventVars{
			Type:          string(e.Type),
			Object:        e.Object,
			Labels:        e.Labels,
			Environment:   e.Environment,
			GateName:      e.GateName,
			PreviousSha:   e.PreviousSha,
			NewSha:        e.NewSha,
			PreviousState: e.PreviousState,
			NewState:      e.NewState,
			ActiveURL:     e.ActiveURL,
			ProposedURL:   e.ProposedURL,
			EventID:       data.EventID,
		},
		EventID: data.EventID,
	}
}

// evaluateVariables compiles and runs each spec.variables expression against the event, returning
// a name->result map. It returns nil (no error) when variables is empty. Expressions are evaluated
// in sorted key order for deterministic behavior; a compile or runtime error is returned wrapped so
// the controller surfaces it as a configuration error (it will not fix itself on retry). Each
// expression sees only the event — it cannot reference the results of other variables.
func evaluateVariables(variables map[string]string, data TemplateData) (map[string]any, error) {
	if len(variables) == 0 {
		return nil, nil
	}

	env := newVariableEnv(data)

	// Deterministic evaluation order (matters only if an expression has side effects, which it
	// should not; sorting keeps behavior stable and errors reproducible).
	names := make([]string, 0, len(variables))
	for name := range variables {
		names = append(names, name)
	}
	slices.Sort(names)

	out := make(map[string]any, len(variables))
	for _, name := range names {
		program, err := expr.Compile(variables[name], expr.Env(env))
		if err != nil {
			return nil, fmt.Errorf("failed to compile variable %q: %w", name, err)
		}
		result, err := expr.Run(program, env)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate variable %q: %w", name, err)
		}
		out[name] = result
	}
	return out, nil
}

// ComputeSignature returns the HMAC-SHA256 of body keyed by secret, encoded as a
// signature header value: "sha256=<lowercase-hex>". It is pure (no I/O) so it can be
// used directly to produce the test vectors and by receivers to verify.
func ComputeSignature(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature reports whether signatureHeader is a valid X-Promoter-Signature for
// body under secret, using a constant-time comparison. It is provided as a reference
// for receivers and is exercised by the package's known-answer tests (including a
// tampered-body negative case).
func VerifySignature(secret, body []byte, signatureHeader string) bool {
	expected := ComputeSignature(secret, body)
	return hmac.Equal([]byte(expected), []byte(signatureHeader))
}

// Sign reads the HMAC secret referenced by signing.SecretRef from the Secret in the
// given namespace (via reader, the controller client) and returns the signature header
// name and value for body. The Secret's data must contain signing.SecretRef.Key with a
// non-empty value.
//
// reader is any client.Reader (e.g. the delivery reconciler's client.Client). The Secret
// is expected to live in the same namespace as the PromoterNotification.
func Sign(ctx context.Context, reader client.Reader, namespace string, signing *v1alpha1.WebhookSigning, body []byte) (headerName, headerValue string, err error) {
	if signing == nil {
		return "", "", errors.New("signing config is nil")
	}
	secretValue, err := readSigningSecret(ctx, reader, namespace, signing.SecretRef)
	if err != nil {
		return "", "", err
	}
	return SignatureHeader, ComputeSignature(secretValue, body), nil
}

// readSigningSecret fetches the referenced Secret and returns the raw bytes at ref.Key.
func readSigningSecret(ctx context.Context, reader client.Reader, namespace string, ref v1alpha1.SecretKeyReference) ([]byte, error) {
	var secret corev1.Secret
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, &secret); err != nil {
		return nil, fmt.Errorf("failed to get signing secret %q: %w", ref.Name, err)
	}
	value, ok := secret.Data[ref.Key]
	if !ok {
		return nil, fmt.Errorf("key %q not found in signing secret %q", ref.Key, ref.Name)
	}
	if len(value) == 0 {
		return nil, fmt.Errorf("key %q in signing secret %q is empty", ref.Key, ref.Name)
	}
	return value, nil
}

// BuildRequest is the one-call seam the delivery layer invokes just before the POST.
// It renders the body and, when spec.delivery.webhook.signing is configured, reads the
// signing secret and computes the signature header.
//
// The returned headers map contains the signature header when signing is enabled, and is
// empty (non-nil) otherwise. The caller is responsible for merging in any static
// spec.delivery.webhook.headers and for setting Content-Type. The Secret is read from the
// PromoterNotification's namespace using reader.
//
// Errors from rendering or signing are returned so the controller can record a failure
// condition; nothing here performs the HTTP request.
func BuildRequest(ctx context.Context, reader client.Reader, notification *v1alpha1.PromoterNotification, event events.Event) (body []byte, headers map[string]string, err error) {
	if notification == nil {
		return nil, nil, errors.New("notification is nil")
	}

	body, err = Render(notification, event)
	if err != nil {
		return nil, nil, err
	}

	headers = make(map[string]string, 1)

	webhook := notification.Spec.Delivery.Webhook
	if webhook != nil && webhook.Signing != nil {
		name, value, signErr := Sign(ctx, reader, notification.Namespace, webhook.Signing, body)
		if signErr != nil {
			return nil, nil, signErr
		}
		headers[name] = value
	}

	return body, headers, nil
}
