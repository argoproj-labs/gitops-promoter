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
		wrcsPath            string
		wrcsUpdatedPath     string
		psPath              string
		psUpdatedPath       string
		nsLabelsPath        string
		responsePath        string
		responseUpdatedPath string
		branch              string
		outputFormat        string
		colorMode           string
	)

	cmd := &cobra.Command{
		Use:   "webrequest",
		Short: "Simulate WebRequestCommitStatus templates and expressions offline",
		Long: `Simulate a WebRequestCommitStatus through reconcile-shaped steps — "reconcile"
(first reconcile) and "next-reconcile" (second reconcile with state carried forward from the first).
On each step the mock HTTP response is injected iff the trigger fires, or unconditionally in polling
mode — the same gating the controller uses. The trigger expression is always evaluated and its
result is shown in the output.

When --promotion-strategy-updated or --web-request-updated is provided, an optional third
"after-state-change" step is appended. It represents a later reconcile after upstream state changed
(new Proposed.Note.DrySha, edited template, re-seeded status, etc.); it swaps the updated inputs in
and, like the default steps, injects iff the trigger re-fires or polling is configured. Use this to
exercise fingerprint-based invalidation or verify re-authored expressions behave as intended.

Optional --response-updated supplies a different mocked HTTP response used only for that third step
when it injects; omit it to reuse the primary --response mock for every step.

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
			if responseUpdatedPath != "" && psUpdatedPath == "" && wrcsUpdatedPath == "" {
				return fmt.Errorf("--response-updated requires --promotion-strategy-updated and/or --web-request-updated")
			}
			var mockUpdated *controller.SimulationMockResponse
			if responseUpdatedPath != "" {
				mu, err := loadMockResponseFlag("--response-updated", responseUpdatedPath)
				if err != nil {
					return err
				}
				mockUpdated = &controller.SimulationMockResponse{
					StatusCode: mu.StatusCode,
					Body:       mu.Body,
					Headers:    mu.Headers,
				}
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
					MockResponseUpdated:           mockUpdated,
				},
			)
			if err != nil {
				return fmt.Errorf("simulate WebRequest templates: %w", err)
			}

			colorize, err := resolveHumanColor(colorMode, cmd.OutOrStdout())
			if err != nil {
				return err
			}
			pal := newHumanPalette(colorize)
			return writeWebRequestResults(cmd.OutOrStdout(), outputFormat, results, pal)
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
		"Path to a YAML file with the mocked HTTP response {statusCode, body, headers} used when a step injects")
	cmd.Flags().StringVar(&responseUpdatedPath, "response-updated", "",
		"Optional: alternate mock response YAML for the 'after-state-change' step only (requires --promotion-strategy-updated and/or --web-request-updated)")
	cmd.Flags().StringVar(&branch, "branch", "",
		"Optional: restrict simulation to a single environment branch (environments context only)")
	cmd.Flags().StringVarP(&outputFormat, "output", "o", outputHuman, "Output format: human|yaml|json")
	cmd.Flags().StringVar(&colorMode, "color", "auto",
		"Colorize human output: auto (TTY + NO_COLOR/FORCE_COLOR), always, or never")

	for _, name := range []string{"web-request", "promotion-strategy", "namespace-labels", "response"} {
		if err := cmd.MarkFlagRequired(name); err != nil {
			panic(err)
		}
	}

	return cmd
}
