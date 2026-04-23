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

package templates

import (
	"fmt"

	"github.com/spf13/cobra"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/controller"
)

// newWebRequestCommand constructs the `promoter templates webrequest` subcommand. It runs the
// WebRequestCommitStatus templates simulation (before-response → with-response → after-response
// → optional after-state-change) against user-supplied YAML fixtures, without touching Kubernetes
// or the network.
func newWebRequestCommand() *cobra.Command {
	var (
		wrcsPath        string
		wrcsUpdatedPath string
		psPath          string
		psUpdatedPath   string
		nsLabelsPath    string
		responsePath    string
		branch          string
		outputFormat    string
	)

	cmd := &cobra.Command{
		Use:   "webrequest",
		Short: "Simulate WebRequestCommitStatus templates and expressions offline",
		Long: `Simulate a WebRequestCommitStatus through three reconcile steps — "before-response"
(Response=nil, empty prior outputs), "with-response" (Response=mock, HTTP URL/body/headers templates
rendered, response.output expression run, outputs carried forward), and "after-response" (Response=nil
again, carry-forward state from step 2). The trigger expression is always evaluated and reported as
information, but does not gate whether the mock Response is injected — that is driven by the step.

When --promotion-strategy-updated or --web-request-updated is provided, an optional fourth
"after-state-change" step is appended: it runs exactly like after-response (Response=nil, all prior
outputs carried from step 3) but swaps in the updated PromotionStrategy and/or WebRequestCommitStatus
for that step only. Use this to exercise scenarios where state changes between reconciles — e.g.
to test fingerprint-based carry-forward when a new Proposed.Note.DrySha invalidates a successful
gate, or to verify a re-authored success expression still behaves correctly against carried state.

Useful for iterating on URL, body, header, description, and url templates plus trigger/response/success
expressions without running the controller or hitting real HTTP endpoints.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutputFormat(outputFormat); err != nil {
				return err
			}

			wrcs, err := loadWebRequestCommitStatus(wrcsPath)
			if err != nil {
				return err
			}
			var wrcsUpdated *promoterv1alpha1.WebRequestCommitStatus
			if wrcsUpdatedPath != "" {
				wrcsUpdated, err = loadWebRequestCommitStatusFromFlag(wrcsUpdatedPath, "--web-request-updated")
				if err != nil {
					return err
				}
			}
			ps, err := loadPromotionStrategy(psPath)
			if err != nil {
				return err
			}
			var psUpdated *promoterv1alpha1.PromotionStrategy
			if psUpdatedPath != "" {
				psUpdated, err = loadPromotionStrategyFromFlag(psUpdatedPath, "--promotion-strategy-updated")
				if err != nil {
					return err
				}
			}
			nsLabels, err := loadNamespaceLabels(nsLabelsPath)
			if err != nil {
				return err
			}
			mock, err := loadMockResponse(responsePath)
			if err != nil {
				return err
			}

			results, err := controller.SimulateWebRequestTemplates(
				cmd.Context(),
				wrcs,
				ps,
				controller.SimulationNamespaceMetadata{
					Labels:      nsLabels.Labels,
					Annotations: nsLabels.Annotations,
				},
				controller.SimulationMockResponse{
					StatusCode: mock.StatusCode,
					Body:       mock.Body,
					Headers:    mock.Headers,
				},
				branch,
				controller.SimulateWebRequestOptions{
					PromotionStrategyUpdated:      psUpdated,
					WebRequestCommitStatusUpdated: wrcsUpdated,
				},
			)
			if err != nil {
				return fmt.Errorf("simulate WebRequest templates: %w", err)
			}

			return writeWebRequestResults(cmd.OutOrStdout(), outputFormat, results)
		},
	}

	cmd.Flags().StringVar(&wrcsPath, "web-request", "",
		"Path to a YAML file containing a WebRequestCommitStatus manifest")
	cmd.Flags().StringVar(&wrcsUpdatedPath, "web-request-updated", "",
		"Optional: WebRequestCommitStatus YAML for an additional 'after-state-change' step "+
			"that simulates the WRCS changing between reconciles — typically from the controller "+
			"writing back updated status, or less commonly a spec edit")
	cmd.Flags().StringVar(&psPath, "promotion-strategy", "",
		"Path to a YAML file containing a PromotionStrategy manifest")
	cmd.Flags().StringVar(&psUpdatedPath, "promotion-strategy-updated", "",
		"Optional: PromotionStrategy YAML for an additional 'after-state-change' step "+
			"that simulates upstream state changing between reconciles")
	cmd.Flags().StringVar(&nsLabelsPath, "namespace-labels", "",
		"Path to a YAML file with {labels, annotations} maps exposed to templates as .NamespaceMetadata")
	cmd.Flags().StringVar(&responsePath, "response", "",
		"Path to a YAML file with the mocked HTTP response {statusCode, body, headers} injected in 'with-response' step")
	cmd.Flags().StringVar(&branch, "branch", "",
		"Optional: restrict simulation to a single environment branch (environments context only)")
	cmd.Flags().StringVarP(&outputFormat, "output", "o", outputHuman, "Output format: human|yaml|json")

	for _, name := range []string{"web-request", "promotion-strategy", "namespace-labels", "response"} {
		if err := cmd.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}

	return cmd
}
