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

package engine

import (
	"context"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/webrequest"
)

// mockHTTPEXecutor implements webrequest.HTTPEXecutor using an injected resolve(branch).
type mockHTTPEXecutor struct {
	resolve func(branch string) (*webrequest.HTTPResponse, error)
}

func newMockHTTPEXecutor(resolve func(branch string) (*webrequest.HTTPResponse, error)) *mockHTTPEXecutor {
	if resolve == nil {
		panic("newMockHTTPEXecutor: resolve is nil")
	}
	return &mockHTTPEXecutor{resolve: resolve}
}

func (e *mockHTTPEXecutor) Execute(_ context.Context, _ *promoterv1alpha1.WebRequestCommitStatus, td webrequest.TemplateData) (webrequest.HTTPResponse, error) {
	resp, err := e.resolve(td.Branch)
	if err != nil {
		return webrequest.HTTPResponse{}, err
	}
	if resp == nil {
		return webrequest.HTTPResponse{}, webrequest.ErrHTTPResponseRequiredWhenTriggerFires
	}
	return *resp, nil
}
