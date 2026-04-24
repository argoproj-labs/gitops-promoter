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

// Package webrequest contains shared types and pure helpers used by the WebRequestCommitStatus
// reconciler for evaluating expr expressions and rendering templates. It intentionally has no
// dependency on client.Client, the settings manager, or the HTTP transport so it can be reused
// by a future simulator/CLI package without pulling in controller-runtime machinery.
package webrequest

import (
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// TemplateData is the data passed to Go templates when rendering URL, headers, body, and description,
// and (via triggerExprData / successWhenExprData) to expr expressions.
//
// Branch is the environment branch currently being processed — set per-iteration in both
// environments and promotionstrategy contexts (empty for the shared HTTP request in promotionstrategy).
//
// Phase is the previous reconcile's phase for carry-forward logic in expressions.
// For description/URL templates it is updated to the current reconcile's phase before upsert.
type TemplateData struct {
	NamespaceMetadata      NamespaceMetadata
	PromotionStrategy      *promoterv1alpha1.PromotionStrategy
	WebRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus
	TriggerOutput          map[string]any
	ResponseOutput         map[string]any
	SuccessOutput          map[string]any
	Branch                 string
	Phase                  string
}

// NamespaceMetadata holds the labels and annotations of the WebRequestCommitStatus's namespace.
// It is included in TemplateData so URL, header, body, and description templates can reference them.
type NamespaceMetadata struct {
	Labels      map[string]string
	Annotations map[string]string
}

// HTTPResponse holds the raw HTTP response (status, body, headers) after the controller makes the
// HTTP request. It is passed to the validation and response expressions as the Response variable.
type HTTPResponse struct {
	Body       any
	Headers    map[string][]string
	StatusCode int
}

// triggerResult holds the result of evaluateTriggerExpression. Trigger is true when the controller
// should perform the HTTP request. When when.output.expression is configured its map result is
// stored in WebRequestCommitStatusEnvironmentStatus.TriggerOutput and on the next reconcile is
// passed back into TemplateData.TriggerOutput for both trigger expressions and into the CommitStatus
// description/URL templates.
type triggerResult struct {
	Trigger bool
}
