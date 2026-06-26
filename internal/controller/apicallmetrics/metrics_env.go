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
	"os"
	"time"

	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
)

const (
	// Default timed-gate duration for full_gate_stack_three_env (all environments unless overridden).
	defaultTCSGateDuration = 5 * time.Minute

	// ACS lastTransitionTimeThreshold in argocdcommitstatus_controller.go.
	argoCDHealthThreshold = 5 * time.Second

	// TCS requeues every 1m while a timed gate is pending with unmet duration.
	tcsPendingRequeueInterval = time.Minute
)

// Environment variables for full_gate_stack_three_env timing (all optional):
//
//   - PROMOTER_API_METRICS_TCS_DURATION — default timed gate for every environment (default 5m)
//   - PROMOTER_API_METRICS_TCS_DURATION_DEV|_STAGING|_PROD — per-environment override
//   - PROMOTER_API_METRICS_ARGO_HEALTHY_DELAY — delay before patching Argo apps healthy (default 0)
//   - PROMOTER_API_METRICS_ARGO_HEALTHY_DELAY_DEV|_STAGING|_PROD — per-environment override
//   - PROMOTER_API_METRICS_WAIT_TIMEOUT — Eventually timeout for full-gate waits (auto-computed if unset)
//   - PROMOTER_API_METRICS_GINKGO_TIMEOUT — ginkgo suite --timeout (default 30m)

func loadTimedGateByBranchFromEnv() map[string]time.Duration {
	defaultDur := durationFromEnv("PROMOTER_API_METRICS_TCS_DURATION", defaultTCSGateDuration)
	return map[string]time.Duration{
		testBranchDevelopment: durationFromEnv("PROMOTER_API_METRICS_TCS_DURATION_DEV", defaultDur),
		testBranchStaging:     durationFromEnv("PROMOTER_API_METRICS_TCS_DURATION_STAGING", defaultDur),
		testBranchProduction:  durationFromEnv("PROMOTER_API_METRICS_TCS_DURATION_PROD", defaultDur),
	}
}

func loadArgoAppHealthyAfterByEnvFromEnv() map[string]time.Duration {
	defaultDelay := durationFromEnv("PROMOTER_API_METRICS_ARGO_HEALTHY_DELAY", 0)
	return map[string]time.Duration{
		testBranchDevelopment: durationFromEnv("PROMOTER_API_METRICS_ARGO_HEALTHY_DELAY_DEV", defaultDelay),
		testBranchStaging:     durationFromEnv("PROMOTER_API_METRICS_ARGO_HEALTHY_DELAY_STAGING", defaultDelay),
		testBranchProduction:  durationFromEnv("PROMOTER_API_METRICS_ARGO_HEALTHY_DELAY_PROD", defaultDelay),
	}
}

func metricsGinkgoTimeout() time.Duration {
	return durationFromEnv("PROMOTER_API_METRICS_GINKGO_TIMEOUT", 30*time.Minute)
}

func durationFromEnv(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

// perEnvGateWaitTimeout is the Eventually budget for one timed-gate or Argo-health step.
func perEnvGateWaitTimeout(scenario APICallMetricsScenario) time.Duration {
	if v := os.Getenv("PROMOTER_API_METRICS_WAIT_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	maxTCS := maxDuration(scenario.TimedGateByBranch)
	maxArgo := maxDuration(scenario.ArgoAppHealthyAfterByEnv)
	return maxTCS + tcsPendingRequeueInterval + argoCDHealthThreshold + maxArgo + 30*time.Second
}

// scenarioWaitTimeout returns how long a single gate-wait Eventually may run.
func scenarioWaitTimeout(scenario APICallMetricsScenario) time.Duration {
	if !scenario.FullGateStack {
		return constants.EventuallyTimeout
	}
	return perEnvGateWaitTimeout(scenario)
}

// scenarioSpecTimeout bounds the full serial promotion cascade (all Eventually phases).
func scenarioSpecTimeout(scenario APICallMetricsScenario) time.Duration {
	if !scenario.FullGateStack {
		return constants.EventuallyTimeout + 2*time.Minute
	}
	envCount := scenario.EnvironmentCount
	if envCount < 1 {
		envCount = 3
	}
	perGate := perEnvGateWaitTimeout(scenario)
	// CTP ready + dev proposed gates + dev promotion + per-env (timer + argo) + downstream promotions.
	return constants.EventuallyTimeout*3 +
		time.Duration(envCount)*perGate*2 +
		time.Duration(envCount-1)*constants.EventuallyTimeout +
		3*time.Minute
}

func maxDuration(m map[string]time.Duration) time.Duration {
	var max time.Duration
	for _, d := range m {
		if d > max {
			max = d
		}
	}
	return max
}
