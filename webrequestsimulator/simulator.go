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

package webrequestsimulator

import (
	"context"

	"github.com/argoproj-labs/gitops-promoter/internal/webrequest"
	"github.com/argoproj-labs/gitops-promoter/internal/webrequest/simulator"
)

// Simulate runs one WebRequestCommitStatus reconcile against the supplied
// Input, using Input.HTTPResponse in place of any real HTTP call.
//
// Result.Status matches exactly what the controller would write to
// WebRequestCommitStatus.Status, so the Status can be fed back in as
// Input.WebRequestCommitStatus.Status for the next Simulate() call to model a
// follow-up reconcile with accumulated trigger/response/success outputs.
//
// Safe for concurrent use.
func Simulate(ctx context.Context, in Input) (*Result, error) {
	res, err := simulator.Simulate(ctx, simulator.Args{
		WebRequestCommitStatus: in.WebRequestCommitStatus,
		PromotionStrategy:      in.PromotionStrategy,
		NamespaceMetadata: webrequest.NamespaceMetadata{
			Labels:      in.NamespaceMetadata.Labels,
			Annotations: in.NamespaceMetadata.Annotations,
		},
		HTTPResponse: toInternalHTTPResponse(in.HTTPResponse),
	})
	if err != nil {
		return nil, err
	}
	return fromInternalResult(res), nil
}

// toInternalHTTPResponse converts the public HTTPResponse to the internal one.
// Fields are identical — this is just a type-identity translation.
func toInternalHTTPResponse(r *HTTPResponse) *webrequest.HTTPResponse {
	if r == nil {
		return nil
	}
	return &webrequest.HTTPResponse{
		StatusCode: r.StatusCode,
		Body:       r.Body,
		Headers:    r.Headers,
	}
}

// fromInternalResult builds a public Result from the internal simulator.Result.
// CommitStatuses are pass-through (*v1alpha1.CommitStatus in both); RenderedRequest
// is field-for-field copied because the shape is intentionally duplicated at the
// public boundary.
func fromInternalResult(res *simulator.Result) *Result {
	if res == nil {
		return nil
	}
	out := &Result{
		Status:         res.Status,
		CommitStatuses: res.CommitStatuses,
	}
	if len(res.RenderedRequests) > 0 {
		out.RenderedRequests = make([]RenderedRequest, len(res.RenderedRequests))
		for i, req := range res.RenderedRequests {
			out.RenderedRequests[i] = RenderedRequest{
				Branch:  req.Branch,
				Method:  req.Method,
				URL:     req.URL,
				Headers: req.Headers,
				Body:    req.Body,
			}
		}
	}
	return out
}
