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

package apicallmetrics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
)

// GitCLIMetrics holds aggregated git CLI subcommand counts for a scenario phase.
type GitCLIMetrics struct {
	Total        float64            `json:"total"`
	TotalNetwork float64            `json:"total_network"`
	Breakdown    map[string]float64 `json:"breakdown"`
}

// SCMMetrics holds aggregated fake-SCM API call counts for a scenario phase.
type SCMMetrics struct {
	Total     float64            `json:"total"`
	Breakdown map[string]float64 `json:"breakdown"`
}

// APICallMetricsPhaseSnapshot records cumulative git CLI and SCM deltas at a promotion boundary.
type APICallMetricsPhaseSnapshot struct {
	Phase  string        `json:"phase"`
	GitCLI GitCLIMetrics `json:"git_cli"`
	SCM    SCMMetrics    `json:"scm"`
}

// APICallMetricsReport is the structured output for one scenario run (or phase).
type APICallMetricsReport struct {
	Scenario                  string                        `json:"scenario"`
	Phase                     string                        `json:"phase"`
	RunAt                     time.Time                     `json:"run_at"`
	GitRepo                   string                        `json:"git_repository"`
	ControllerRequeueDuration string                        `json:"controller_requeue_duration,omitempty"`
	ReconcilePolicy           string                        `json:"reconcile_policy,omitempty"`
	WaitTimeout               string                        `json:"wait_timeout,omitempty"`
	TimedGateByBranch         map[string]string             `json:"timed_gate_by_branch,omitempty"`
	GitCLI                    GitCLIMetrics                 `json:"git_cli"`
	SCM                       SCMMetrics                    `json:"scm"`
	Phases                    []APICallMetricsPhaseSnapshot `json:"phases,omitempty"`
}

type metricsReportCollector struct {
	scenario                  string
	gitRepo                   string
	controllerRequeueDuration string
	waitTimeout               string
	timedGateByBranch         map[string]string
	phases                    []APICallMetricsPhaseSnapshot
}

func newMetricsReportCollector(scenario, gitRepo, requeueDuration string) *metricsReportCollector {
	return &metricsReportCollector{
		scenario:                  scenario,
		gitRepo:                   gitRepo,
		controllerRequeueDuration: requeueDuration,
	}
}

func (c *metricsReportCollector) addPhase(phase string, gitBreakdown, scmBreakdown map[string]float64) {
	c.phases = append(c.phases, APICallMetricsPhaseSnapshot{
		Phase: phase,
		GitCLI: GitCLIMetrics{
			Total:        gitOperationTotal(gitBreakdown),
			TotalNetwork: gitOperationNetworkTotal(gitBreakdown),
			Breakdown:    gitBreakdown,
		},
		SCM: SCMMetrics{
			Total:     scmCallTotal(scmBreakdown),
			Breakdown: scmBreakdown,
		},
	})
}

func (c *metricsReportCollector) finalReport(phase string, gitBreakdown, scmBreakdown map[string]float64) APICallMetricsReport {
	return APICallMetricsReport{
		Scenario:                  c.scenario,
		Phase:                     phase,
		RunAt:                     time.Now().UTC(),
		GitRepo:                   c.gitRepo,
		ControllerRequeueDuration: c.controllerRequeueDuration,
		ReconcilePolicy:           "event_driven",
		WaitTimeout:               c.waitTimeout,
		TimedGateByBranch:         c.timedGateByBranch,
		GitCLI: GitCLIMetrics{
			Total:        gitOperationTotal(gitBreakdown),
			TotalNetwork: gitOperationNetworkTotal(gitBreakdown),
			Breakdown:    gitBreakdown,
		},
		SCM: SCMMetrics{
			Total:     scmCallTotal(scmBreakdown),
			Breakdown: scmBreakdown,
		},
		Phases: c.phases,
	}
}

func emitAPICallMetricsReport(report APICallMetricsReport) {
	GinkgoLogr.Info("APICallMetrics report",
		"scenario", report.Scenario,
		"phase", report.Phase,
		"git_repository", report.GitRepo,
		"controller_requeue_duration", report.ControllerRequeueDuration,
		"reconcile_policy", report.ReconcilePolicy,
		"wait_timeout", report.WaitTimeout,
		"timed_gate_by_branch", report.TimedGateByBranch,
		"git_cli_total", report.GitCLI.Total,
		"git_cli_total_network", report.GitCLI.TotalNetwork,
		"git_cli_breakdown", report.GitCLI.Breakdown,
		"scm_total", report.SCM.Total,
		"scm_breakdown", report.SCM.Breakdown,
	)
	AddReportEntry("APICallMetrics", report)
	if path := resolveReportPath(report); path != "" {
		if err := writeJSON(path, report); err != nil {
			GinkgoLogr.Error(err, "failed to write APICallMetrics report", "path", path)
		}
	}
}

func resolveReportPath(report APICallMetricsReport) string {
	base := os.Getenv("PROMOTER_API_METRICS_REPORT")
	if base == "" {
		return filepath.Join(os.TempDir(), fmt.Sprintf("promoter-api-metrics-%s.json", report.Scenario))
	}
	info, err := os.Stat(base)
	if err == nil && info.IsDir() {
		name := fmt.Sprintf("%s_%s_%d.json", report.Scenario, report.Phase, report.RunAt.Unix())
		return filepath.Join(base, name)
	}
	if strings.HasSuffix(base, ".json") {
		return base
	}
	return filepath.Join(base, fmt.Sprintf("%s.json", report.Scenario))
}

func writeJSON(path string, report APICallMetricsReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	GinkgoLogr.Info("APICallMetrics report written", "path", path)
	return nil
}
