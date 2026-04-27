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

package simulate

import (
	"context"
	"fmt"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/webrequest"
	"github.com/argoproj-labs/gitops-promoter/webrequestsimulator/simulatortypes"
)

// mockHTTPEXecutor implements webrequest.HTTPEXecutor using an injected resolve(branch).
type mockHTTPEXecutor struct {
	resolve  func(branch string) (*webrequest.HTTPResponse, error)
	rendered *[]simulatortypes.RenderedRequest
}

func newMockHTTPEXecutor(resolve func(branch string) (*webrequest.HTTPResponse, error), rendered *[]simulatortypes.RenderedRequest) *mockHTTPEXecutor {
	if resolve == nil {
		panic("newMockHTTPEXecutor: resolve is nil")
	}
	return &mockHTTPEXecutor{resolve: resolve, rendered: rendered}
}

func (e *mockHTTPEXecutor) Execute(_ context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, td webrequest.TemplateData) (webrequest.HTTPResponse, error) {
	return makeHTTPRequest(wrcs, td, e.resolve, e.rendered)
}

// makeHTTPRequest runs the simulator HTTP round-trip: optionally records a rendered-request snapshot,
// then returns the injected mock response for td.Branch.
//
// Unlike internal/controller.WebRequestCommitStatusReconciler.makeHTTPRequest, this path does not
// validate URLs against SCM providers, apply authentication, attach timeouts, perform metrics, or
// issue real network requests.
func makeHTTPRequest(
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	td webrequest.TemplateData,
	resolve func(branch string) (*webrequest.HTTPResponse, error),
	rendered *[]simulatortypes.RenderedRequest,
) (webrequest.HTTPResponse, error) {
	if rendered != nil {
		req, err := webrequest.BuildRenderedHTTPRequestFromTemplates(wrcs, td)
		if err != nil {
			return webrequest.HTTPResponse{}, fmt.Errorf("failed to render HTTP request templates: %w", err)
		}
		*rendered = append(*rendered, simulatortypes.RenderedRequest{
			Branch: req.Branch, Method: req.Method, URL: req.URL, Headers: req.Headers, Body: req.Body,
		})
	}
	resp, err := resolve(td.Branch)
	if err != nil {
		return webrequest.HTTPResponse{}, err
	}
	if resp == nil {
		return webrequest.HTTPResponse{}, fmt.Errorf("branch %q: %w", td.Branch, webrequest.ErrHTTPResponseRequiredWhenTriggerFires)
	}
	return *resp, nil
}
