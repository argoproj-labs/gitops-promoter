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

// Package templates implements the `promoter templates` CLI subcommand, a read-only offline tool for
// rendering templates and evaluating expressions against user-supplied YAML fixtures. It has no
// Kubernetes client and never performs network requests: templates/expressions are exercised in-memory
// so users can iterate on PullRequest templates or WebRequestCommitStatus templates without running the
// controller.
package templates

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"sigs.k8s.io/yaml"
)

// Output format values accepted by the `--output` flag on each templates subcommand.
const (
	outputHuman = "human"
	outputYAML  = "yaml"
	outputJSON  = "json"
)

// NewTemplatesCommand returns the `promoter templates` root command with `pullrequest` and `webrequest`
// subcommands. It has no Kubernetes client dependency and does not interact with the cluster; all
// inputs are read from YAML files.
func NewTemplatesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "templates",
		Short: "Render templates and evaluate expressions offline",
		Long: `Render templates and evaluate expressions from this project offline, using user-supplied YAML
fixtures. Useful for iterating on PullRequest template or WebRequestCommitStatus template/expression
configuration without running the controller or touching a cluster.`,
		SilenceUsage: true,
	}
	cmd.AddCommand(newPullRequestCommand())
	cmd.AddCommand(newWebRequestCommand())
	return cmd
}

// readFile reads the entire contents of a path and returns them as bytes. It wraps errors so CLI
// callers always see which flag the failing file came from.
func readFile(flagName, path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s file %q: %w", flagName, path, err)
	}
	return data, nil
}

// decodeYAMLFile reads a YAML file and unmarshals it into out using sigs.k8s.io/yaml, which respects
// JSON tags (required for Kubernetes API types). flagName is used only for error context.
func decodeYAMLFile(flagName, path string, out any) error {
	data, err := readFile(flagName, path)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		return fmt.Errorf("failed to parse %s file %q as YAML: %w", flagName, path, err)
	}
	return nil
}

// loadPromotionStrategy decodes a YAML file containing a PromotionStrategy manifest and returns it.
func loadPromotionStrategy(path string) (*promoterv1alpha1.PromotionStrategy, error) {
	return loadPromotionStrategyFromFlag(path, "--promotion-strategy")
}

// loadPromotionStrategyFromFlag is like loadPromotionStrategy but allows the caller to surface the
// specific flag name in error messages (e.g. for --promotion-strategy-updated).
func loadPromotionStrategyFromFlag(path, flagName string) (*promoterv1alpha1.PromotionStrategy, error) {
	var ps promoterv1alpha1.PromotionStrategy
	if err := decodeYAMLFile(flagName, path, &ps); err != nil {
		return nil, err
	}
	return &ps, nil
}

// loadWebRequestCommitStatus decodes a YAML file containing a WebRequestCommitStatus manifest.
func loadWebRequestCommitStatus(path string) (*promoterv1alpha1.WebRequestCommitStatus, error) {
	return loadWebRequestCommitStatusFromFlag(path, "--web-request")
}

// loadWebRequestCommitStatusFromFlag is like loadWebRequestCommitStatus but allows the caller to
// surface a specific flag name in error messages (e.g. for --web-request-updated).
func loadWebRequestCommitStatusFromFlag(path, flagName string) (*promoterv1alpha1.WebRequestCommitStatus, error) {
	var wrcs promoterv1alpha1.WebRequestCommitStatus
	if err := decodeYAMLFile(flagName, path, &wrcs); err != nil {
		return nil, err
	}
	return &wrcs, nil
}

// loadChangeTransferPolicy decodes a YAML file containing a ChangeTransferPolicy manifest.
func loadChangeTransferPolicy(path string) (*promoterv1alpha1.ChangeTransferPolicy, error) {
	var ctp promoterv1alpha1.ChangeTransferPolicy
	if err := decodeYAMLFile("--change-transfer-policy", path, &ctp); err != nil {
		return nil, err
	}
	return &ctp, nil
}

// NamespaceLabelsFile is the on-disk shape of the `--namespace-labels` input: a YAML object with
// `labels` and `annotations` maps. Both fields are optional; missing keys yield nil maps.
type NamespaceLabelsFile struct {
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// loadNamespaceLabels decodes a YAML file containing a NamespaceLabelsFile.
func loadNamespaceLabels(path string) (NamespaceLabelsFile, error) {
	var nsLabels NamespaceLabelsFile
	if err := decodeYAMLFile("--namespace-labels", path, &nsLabels); err != nil {
		return NamespaceLabelsFile{}, err
	}
	return nsLabels, nil
}

// MockResponseFile is the on-disk shape of the `--response` input: a YAML object with statusCode, body
// (any, typically a JSON-compatible scalar/map/slice or a string), and headers (map[string][]string).
type MockResponseFile struct {
	Body       any                 `json:"body,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	StatusCode int                 `json:"statusCode"`
}

// loadMockResponseFlag decodes a YAML file containing a MockResponseFile, using flagName in errors.
func loadMockResponseFlag(flagName, path string) (MockResponseFile, error) {
	var mock MockResponseFile
	if err := decodeYAMLFile(flagName, path, &mock); err != nil {
		return MockResponseFile{}, err
	}
	return mock, nil
}

// loadMockResponse decodes a YAML file containing a MockResponseFile.
func loadMockResponse(path string) (MockResponseFile, error) {
	return loadMockResponseFlag("--response", path)
}

// PullRequestTemplateFile accepts either a bare PullRequestTemplate ({title, description}) or a full
// ControllerConfiguration with `spec.pullRequest.template`. Only the template fields are used by the
// CLI; other ControllerConfiguration fields are ignored.
type PullRequestTemplateFile struct {
	// Spec is populated when the file is a full ControllerConfiguration.
	Spec *pullRequestTemplateFileSpec `json:"spec,omitempty"`

	// Title / Description are populated when the file is a bare PullRequestTemplate.
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

type pullRequestTemplateFileSpec struct {
	PullRequest *pullRequestTemplateFilePullRequest `json:"pullRequest,omitempty"`
}

type pullRequestTemplateFilePullRequest struct {
	Template *promoterv1alpha1.PullRequestTemplate `json:"template,omitempty"`
}

// loadPullRequestTemplate decodes a YAML file containing either a bare PullRequestTemplate or a full
// ControllerConfiguration and returns the extracted template.
func loadPullRequestTemplate(path string) (promoterv1alpha1.PullRequestTemplate, error) {
	var f PullRequestTemplateFile
	if err := decodeYAMLFile("--pull-request-template", path, &f); err != nil {
		return promoterv1alpha1.PullRequestTemplate{}, err
	}
	if f.Spec != nil && f.Spec.PullRequest != nil && f.Spec.PullRequest.Template != nil {
		return *f.Spec.PullRequest.Template, nil
	}
	if f.Title == "" && f.Description == "" {
		return promoterv1alpha1.PullRequestTemplate{}, fmt.Errorf(
			"--pull-request-template file %q must contain {title, description} "+
				"or a full ControllerConfiguration with spec.pullRequest.template",
			path,
		)
	}
	return promoterv1alpha1.PullRequestTemplate{Title: f.Title, Description: f.Description}, nil
}

// validateOutputFormat returns an error when format is not one of human|yaml|json.
func validateOutputFormat(format string) error {
	switch format {
	case outputHuman, outputYAML, outputJSON:
		return nil
	default:
		return fmt.Errorf("invalid --output %q: must be one of %q, %q, %q", format, outputHuman, outputYAML, outputJSON)
	}
}
