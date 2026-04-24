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
	"fmt"

	"github.com/argoproj-labs/gitops-promoter/internal/webrequest"
	"github.com/argoproj-labs/gitops-promoter/webrequestsimulator/internal/engine"
	"github.com/argoproj-labs/gitops-promoter/webrequestsimulator/simulatortypes"
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
func Simulate(ctx context.Context, in simulatortypes.Input) (*simulatortypes.Result, error) {
	res, err := engine.Simulate(ctx, engine.Args{
		WebRequestCommitStatus: in.WebRequestCommitStatus,
		PromotionStrategy:      in.PromotionStrategy,
		NamespaceMetadata: webrequest.NamespaceMetadata{
			Labels:      in.NamespaceMetadata.Labels,
			Annotations: in.NamespaceMetadata.Annotations,
		},
		HTTPResponse: toInternalHTTPResponse(in.HTTPResponse),
	})
	if err != nil {
		return nil, fmt.Errorf("simulate: %w", err)
	}
	return res, nil
}

func toInternalHTTPResponse(r *simulatortypes.HTTPResponse) *webrequest.HTTPResponse {
	if r == nil {
		return nil
	}
	return &webrequest.HTTPResponse{
		StatusCode: r.StatusCode,
		Body:       r.Body,
		Headers:    r.Headers,
	}
}
