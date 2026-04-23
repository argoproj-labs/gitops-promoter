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
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/argoproj-labs/gitops-promoter/internal/controller"
)

// writePullRequestResult writes a PullRequestRenderResult in the chosen output format.
func writePullRequestResult(w io.Writer, format string, result PullRequestRenderResult) error {
	switch format {
	case outputJSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return fmt.Errorf("encode PR render result as JSON: %w", err)
		}
		return nil
	case outputYAML:
		data, err := yaml.Marshal(result)
		if err != nil {
			return fmt.Errorf("marshal PR render result as YAML: %w", err)
		}
		if _, err := w.Write(data); err != nil {
			return fmt.Errorf("write PR render result: %w", err)
		}
		return nil
	default:
		var b strings.Builder
		b.WriteString("Title:\n")
		b.WriteString(indent(result.Title, "  "))
		b.WriteString("\n\nDescription:\n")
		b.WriteString(indent(result.Description, "  "))
		b.WriteString("\n")
		if _, err := io.WriteString(w, b.String()); err != nil {
			return fmt.Errorf("write PR render result: %w", err)
		}
		return nil
	}
}

// writeWebRequestResults writes a slice of WebRequestStepResult in the chosen output format.
func writeWebRequestResults(w io.Writer, format string, results []controller.WebRequestStepResult) error {
	switch format {
	case outputJSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(results); err != nil {
			return fmt.Errorf("encode WebRequest results as JSON: %w", err)
		}
		return nil
	case outputYAML:
		data, err := yaml.Marshal(results)
		if err != nil {
			return fmt.Errorf("marshal WebRequest results as YAML: %w", err)
		}
		if _, err := w.Write(data); err != nil {
			return fmt.Errorf("write WebRequest results: %w", err)
		}
		return nil
	default:
		return writeWebRequestResultsHuman(w, results)
	}
}

// writeWebRequestResultsHuman prints the step-by-step simulation output in a human-friendly format.
func writeWebRequestResultsHuman(w io.Writer, results []controller.WebRequestStepResult) error {
	var b strings.Builder
	for _, step := range results {
		b.WriteString(stepHeader(step))
		b.WriteString("\n")
		for _, eval := range step.Evaluations {
			b.WriteString(formatEvaluation(eval, step.Context))
		}
		if len(step.CommitStatuses) > 0 {
			b.WriteString("CommitStatuses:\n")
			for _, cs := range step.CommitStatuses {
				b.WriteString(formatCommitStatus(cs))
			}
		}
		if len(step.Errors) > 0 {
			b.WriteString("Step errors:\n")
			for _, msg := range step.Errors {
				b.WriteString("  - ")
				b.WriteString(msg)
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write WebRequest results: %w", err)
	}
	return nil
}

// stepHeader returns a one-line header announcing the step, padded with `=` to 68 columns when there
// is room. The label text mirrors the simulator labels (before-response / with-response / after-response).
func stepHeader(step controller.WebRequestStepResult) string {
	title := fmt.Sprintf("=== Step: %s (context=%s) ", step.Label, step.Context)
	if len(title) < 68 {
		title += strings.Repeat("=", 68-len(title))
	}
	return title
}

// formatEvaluation formats a single WebRequestStepEvaluation for human output.
func formatEvaluation(eval controller.WebRequestStepEvaluation, context string) string {
	var b strings.Builder
	if eval.Branch != "" {
		fmt.Fprintf(&b, "Environment: %s\n", eval.Branch)
	} else if context == "promotionstrategy" {
		b.WriteString("Shared evaluation (context=promotionstrategy)\n")
	}
	if eval.TriggerEval.Evaluated {
		if eval.TriggerEval.Error != "" {
			fmt.Fprintf(&b, "  Trigger expression (info): ERROR: %s\n", eval.TriggerEval.Error)
		} else {
			fmt.Fprintf(&b, "  Trigger expression (info): %t\n", eval.TriggerEval.ShouldFire)
		}
	} else {
		b.WriteString("  Trigger expression (info): (no trigger mode configured)\n")
	}

	if eval.ResponseInjected {
		writeRenderedRequest(&b, eval.RenderedRequest)
		writeMockResponse(&b, eval.MockResponse)
	} else {
		b.WriteString("  Response: nil\n")
	}

	writeOutputMap(&b, "TriggerOutput", eval.TriggerOutput)
	writeOutputMap(&b, "ResponseOutput", eval.ResponseOutput)
	writeOutputMap(&b, "SuccessOutput", eval.SuccessOutput)
	fmt.Fprintf(&b, "  Phase: %s\n", nonEmpty(eval.Phase, "(empty)"))
	if len(eval.PhasePerBranch) > 0 {
		b.WriteString("  PhasePerBranch:\n")
		for _, key := range sortedKeys(eval.PhasePerBranch) {
			fmt.Fprintf(&b, "    %s: %s\n", key, eval.PhasePerBranch[key])
		}
	}
	if len(eval.Errors) > 0 {
		b.WriteString("  Evaluation errors:\n")
		for _, msg := range eval.Errors {
			fmt.Fprintf(&b, "    - %s\n", msg)
		}
	}
	return b.String()
}

// writeRenderedRequest prints the rendered HTTP request (URL / body / headers) for the with-response
// step. When rendered is nil the section is skipped (e.g. if template rendering failed).
func writeRenderedRequest(b *strings.Builder, rendered *controller.RenderedHTTPRequest) {
	if rendered == nil {
		return
	}
	b.WriteString("  Rendered HTTP request:\n")
	fmt.Fprintf(b, "    Method:  %s\n", nonEmpty(rendered.Method, "GET"))
	fmt.Fprintf(b, "    URL:     %s\n", rendered.URL)
	if len(rendered.Headers) > 0 {
		b.WriteString("    Headers:\n")
		for _, key := range sortedKeys(rendered.Headers) {
			fmt.Fprintf(b, "      %s: %s\n", key, rendered.Headers[key])
		}
	}
	if rendered.Body != nil {
		b.WriteString("    Body:\n")
		b.WriteString(indent(*rendered.Body, "      "))
		b.WriteString("\n")
	}
}

// writeMockResponse prints the mock response applied in the with-response step. When mock is nil
// the section is skipped.
func writeMockResponse(b *strings.Builder, mock *controller.SimulationMockResponse) {
	if mock == nil {
		return
	}
	b.WriteString("  Mock response (applied):\n")
	fmt.Fprintf(b, "    StatusCode: %d\n", mock.StatusCode)
	if len(mock.Headers) > 0 {
		b.WriteString("    Headers:\n")
		for _, key := range sortedKeysMultiValue(mock.Headers) {
			fmt.Fprintf(b, "      %s: %s\n", key, strings.Join(mock.Headers[key], ", "))
		}
	}
	if mock.Body != nil {
		b.WriteString("    Body:\n")
		b.WriteString(indent(fmt.Sprintf("%v", mock.Body), "      "))
		b.WriteString("\n")
	}
}

// formatCommitStatus formats a single RenderedCommitStatus for human output.
func formatCommitStatus(cs controller.RenderedCommitStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  [%s]\n", cs.Branch)
	fmt.Fprintf(&b, "    Sha:         %s\n", nonEmpty(cs.Sha, "(n/a)"))
	fmt.Fprintf(&b, "    Phase:       %s\n", nonEmpty(cs.Phase, "(empty)"))
	fmt.Fprintf(&b, "    Description: %s\n", cs.Description)
	fmt.Fprintf(&b, "    URL:         %s\n", cs.URL)
	return b.String()
}

// writeOutputMap writes a labelled map (TriggerOutput / ResponseOutput / SuccessOutput) to b.
// Empty maps render as a single line (`Label: {}`); populated maps render as multi-line
// indented JSON with the closing brace aligned under the label. Keys are sorted (courtesy of
// json.MarshalIndent) so successive runs produce diff-friendly output.
//
// Layout:
//
//	Label: {
//	  "key1": value,
//	  "key2": { ... }
//	}
//
// All lines are indented by 2 spaces from the column where step sections start, so the block
// lines up with the per-evaluation fields above and below it.
func writeOutputMap(b *strings.Builder, label string, data map[string]any) {
	if len(data) == 0 {
		fmt.Fprintf(b, "  %s: {}\n", label)
		return
	}
	// Prefix "  " is prepended to every line AFTER the first by MarshalIndent. We write the
	// label + first "{" manually, then append the JSON body; MarshalIndent's first line starts
	// with "{", which we'd duplicate — so we strip MarshalIndent's first byte.
	raw, err := json.MarshalIndent(data, "  ", "  ")
	if err != nil {
		fmt.Fprintf(b, "  %s: (unmarshalable: %v)\n", label, err)
		return
	}
	fmt.Fprintf(b, "  %s: %s\n", label, string(raw))
}

// indent prefixes each line of s with prefix. An empty s yields an empty string.
func indent(s, prefix string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

// nonEmpty returns value when non-empty, otherwise fallback.
func nonEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// sortedKeys returns the keys of m in sorted order.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// sortedKeysMultiValue returns the keys of m in sorted order.
func sortedKeysMultiValue(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
