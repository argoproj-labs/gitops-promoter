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
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/argoproj-labs/gitops-promoter/internal/webrequest"
)

// renderHTTPRequest renders the HTTP request templates (URL, headers, body) for
// a WebRequestCommitStatus against the given TemplateData. Branch is copied onto
// the result so callers can tell per-env requests apart in environments context.
// Headers are rendered into a flat map (single value per header) which matches
// how the controller sets them on http.Request.Header.
func renderHTTPRequest(wrcs *promoterv1alpha1.WebRequestCommitStatus, td webrequest.TemplateData) (RenderedRequest, error) {
	req := RenderedRequest{
		Branch: td.Branch,
		Method: wrcs.Spec.HTTPRequest.Method,
	}

	url, err := utils.RenderStringTemplate(wrcs.Spec.HTTPRequest.URLTemplate, td)
	if err != nil {
		return RenderedRequest{}, fmt.Errorf("failed to render URL template: %w", err)
	}
	req.URL = url

	if wrcs.Spec.HTTPRequest.BodyTemplate != "" {
		body, err := utils.RenderStringTemplate(wrcs.Spec.HTTPRequest.BodyTemplate, td)
		if err != nil {
			return RenderedRequest{}, fmt.Errorf("failed to render body template: %w", err)
		}
		req.Body = body
	}

	if len(wrcs.Spec.HTTPRequest.HeaderTemplates) > 0 {
		req.Headers = make(map[string]string, len(wrcs.Spec.HTTPRequest.HeaderTemplates))
		for name, headerTemplate := range wrcs.Spec.HTTPRequest.HeaderTemplates {
			value, err := utils.RenderStringTemplate(headerTemplate, td)
			if err != nil {
				return RenderedRequest{}, fmt.Errorf("failed to render header template %q: %w", name, err)
			}
			req.Headers[name] = value
		}
	}

	return req, nil
}

// renderCommitStatus builds a *promoterv1alpha1.CommitStatus matching what the
// controller's upsertCommitStatus would produce: ObjectMeta.Name follows the
// controller's KubeSafeUniqueName formula, labels match (WebRequestCommitStatus /
// Environment / CommitStatus), and the spec is populated with the rendered
// description and URL, the reported SHA, and the computed phase.
//
// OwnerReferences and Status are intentionally omitted — the simulator does not
// run against a live cluster, so it has no UID to reference and observes no state.
func renderCommitStatus(
	ctx context.Context,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	repositoryRefName, branch, sha string,
	phase promoterv1alpha1.CommitStatusPhase,
	td webrequest.TemplateData,
) (*promoterv1alpha1.CommitStatus, error) {
	cs := &promoterv1alpha1.CommitStatus{
		TypeMeta: metav1.TypeMeta{
			APIVersion: promoterv1alpha1.GroupVersion.String(),
			Kind:       "CommitStatus",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      utils.KubeSafeUniqueName(ctx, fmt.Sprintf("%s-%s-webrequest", wrcs.Name, branch)),
			Namespace: wrcs.Namespace,
			Labels: map[string]string{
				promoterv1alpha1.WebRequestCommitStatusLabel: utils.KubeSafeLabel(wrcs.Name),
				promoterv1alpha1.EnvironmentLabel:            utils.KubeSafeLabel(branch),
				promoterv1alpha1.CommitStatusLabel:           wrcs.Spec.Key,
			},
		},
		Spec: promoterv1alpha1.CommitStatusSpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{Name: repositoryRefName},
			Name:                wrcs.Spec.Key + "/" + branch,
			Sha:                 sha,
			Phase:               phase,
		},
	}

	if wrcs.Spec.DescriptionTemplate != "" {
		desc, err := utils.RenderStringTemplate(wrcs.Spec.DescriptionTemplate, td)
		if err != nil {
			return nil, fmt.Errorf("failed to render description template: %w", err)
		}
		cs.Spec.Description = desc
	}

	if wrcs.Spec.UrlTemplate != "" {
		url, err := utils.RenderStringTemplate(wrcs.Spec.UrlTemplate, td)
		if err != nil {
			return nil, fmt.Errorf("failed to render URL template: %w", err)
		}
		cs.Spec.Url = url
	}

	return cs, nil
}
