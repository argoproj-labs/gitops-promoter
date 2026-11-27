/*
Copyright 2026.

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

package controller

import (
	"fmt"
	"net/url"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// renderGateURL renders a gate's spec.url template with data and checks that the result is an
// absolute http or https URL. It returns "" when no template is configured.
func renderGateURL(cfg promoterv1alpha1.URLConfig, data any) (string, error) {
	if cfg.Template == "" {
		return "", nil
	}
	rendered, err := utils.RenderStringTemplate(cfg.Template, data, cfg.Options...)
	if err != nil {
		return "", fmt.Errorf("failed to render URL template: %w", err)
	}
	parsed, err := url.Parse(rendered)
	if err != nil {
		return "", fmt.Errorf("failed to parse URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("URL scheme is not http or https: %s", parsed.Scheme)
	}
	return rendered, nil
}
