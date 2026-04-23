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

// PullRequestRenderResult is the in-memory representation of what `promoter templates pullrequest`
// produces. It is used for structured YAML/JSON output and consumed by the human renderer.
type PullRequestRenderResult struct {
	Title       string `json:"title" yaml:"title"`
	Description string `json:"description" yaml:"description"`
}

// newPullRequestCommand constructs the `promoter templates pullrequest` subcommand. It reads a
// PullRequestTemplate (or a ControllerConfiguration that contains one), a PromotionStrategy, and a
// ChangeTransferPolicy from YAML files and renders the PR title and description.
func newPullRequestCommand() *cobra.Command {
	var (
		templatePath string
		psPath       string
		ctpPath      string
		outputFormat string
	)

	cmd := &cobra.Command{
		Use:   "pullrequest",
		Short: "Render a PullRequest template (title + description) offline",
		Long: `Render the PullRequestTemplate.Title and PullRequestTemplate.Description using the
provided PromotionStrategy and ChangeTransferPolicy as template data. Templates support Go template
syntax and Sprig functions (minus env/expandenv/getHostByName), matching what the controller uses
in production.`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutputFormat(outputFormat); err != nil {
				return err
			}
			tmpl, err := loadPullRequestTemplate(templatePath)
			if err != nil {
				return err
			}
			ctp, err := loadChangeTransferPolicy(ctpPath)
			if err != nil {
				return err
			}
			var ps *promoterv1alpha1.PromotionStrategy
			if psPath != "" {
				ps, err = loadPromotionStrategy(psPath)
				if err != nil {
					return err
				}
			}

			data := map[string]any{"ChangeTransferPolicy": ctp}
			if ps != nil {
				data["PromotionStrategy"] = ps
			}
			title, description, err := controller.TemplatePullRequest(tmpl, data)
			if err != nil {
				return fmt.Errorf("failed to render PullRequest templates: %w", err)
			}

			result := PullRequestRenderResult{Title: title, Description: description}
			return writePullRequestResult(cmd.OutOrStdout(), outputFormat, result)
		},
	}

	cmd.Flags().StringVar(&templatePath, "pull-request-template", "",
		"Path to a YAML file containing a PullRequestTemplate ({title, description}) or a full ControllerConfiguration")
	cmd.Flags().StringVar(&psPath, "promotion-strategy", "",
		"Path to a YAML file with a PromotionStrategy (optional; some templates only use the ChangeTransferPolicy)")
	cmd.Flags().StringVar(&ctpPath, "change-transfer-policy", "",
		"Path to a YAML file containing a ChangeTransferPolicy manifest")
	cmd.Flags().StringVarP(&outputFormat, "output", "o", outputHuman, "Output format: human|yaml|json")

	if err := cmd.MarkFlagRequired("pull-request-template"); err != nil {
		panic(err)
	}
	if err := cmd.MarkFlagRequired("change-transfer-policy"); err != nil {
		panic(err)
	}

	return cmd
}
