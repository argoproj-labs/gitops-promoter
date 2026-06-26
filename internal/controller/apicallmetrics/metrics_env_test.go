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
	"testing"
	"time"

	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
)

func TestScenarioWaitTimeout_fullGateDefaultTCS(t *testing.T) {
	scenario := APICallMetricsScenario{
		FullGateStack:     true,
		TimedGateByBranch: map[string]time.Duration{testBranchDevelopment: defaultTCSGateDuration},
	}
	got := scenarioWaitTimeout(scenario)
	want := defaultTCSGateDuration + tcsPendingRequeueInterval + argoCDHealthThreshold + 30*time.Second
	if got != want {
		t.Fatalf("scenarioWaitTimeout() = %v, want %v", got, want)
	}
}

func TestScenarioSpecTimeout_fullGateThreeEnv(t *testing.T) {
	scenario := APICallMetricsScenario{
		FullGateStack:     true,
		EnvironmentCount:  3,
		TimedGateByBranch: map[string]time.Duration{testBranchDevelopment: defaultTCSGateDuration},
	}
	got := scenarioSpecTimeout(scenario)
	perGate := defaultTCSGateDuration + tcsPendingRequeueInterval + argoCDHealthThreshold + 30*time.Second
	want := 3*constants.EventuallyTimeout + 3*perGate*2 + 2*constants.EventuallyTimeout + 3*time.Minute
	if got != want {
		t.Fatalf("scenarioSpecTimeout() = %v, want %v", got, want)
	}
}

func TestLoadTimedGateByBranchFromEnv_default(t *testing.T) {
	t.Setenv("PROMOTER_API_METRICS_TCS_DURATION", "")
	t.Setenv("PROMOTER_API_METRICS_TCS_DURATION_DEV", "")
	got := loadTimedGateByBranchFromEnv()
	if got[testBranchDevelopment] != defaultTCSGateDuration {
		t.Fatalf("dev duration = %v, want %v", got[testBranchDevelopment], defaultTCSGateDuration)
	}
}

func TestLoadTimedGateByBranchFromEnv_override(t *testing.T) {
	t.Setenv("PROMOTER_API_METRICS_TCS_DURATION", "2m")
	t.Setenv("PROMOTER_API_METRICS_TCS_DURATION_DEV", "1s")
	got := loadTimedGateByBranchFromEnv()
	if got[testBranchDevelopment] != time.Second {
		t.Fatalf("dev duration = %v, want 1s", got[testBranchDevelopment])
	}
	if got[testBranchStaging] != 2*time.Minute {
		t.Fatalf("staging duration = %v, want 2m", got[testBranchStaging])
	}
}

func TestLoadSoakTimedGateByBranchFromEnv_default(t *testing.T) {
	t.Setenv("PROMOTER_API_METRICS_SOAK_TCS_DURATION", "")
	got := loadSoakTimedGateByBranchFromEnv()
	if got[testBranchDevelopment] != defaultSoakTCSGateDuration {
		t.Fatalf("dev soak TCS = %v, want %v", got[testBranchDevelopment], defaultSoakTCSGateDuration)
	}
}

func TestSoakDurationFromEnv_default(t *testing.T) {
	t.Setenv("PROMOTER_API_METRICS_SOAK_DURATION", "")
	if got := soakDurationFromEnv(); got != defaultSoakDuration {
		t.Fatalf("soakDurationFromEnv() = %v, want %v", got, defaultSoakDuration)
	}
}

func TestSoakRequeueFromEnv_default(t *testing.T) {
	t.Setenv("PROMOTER_API_METRICS_SOAK_REQUEUE", "")
	if got := soakRequeueDurationFromEnv(); got != defaultSoakRequeueDuration {
		t.Fatalf("soakRequeueDurationFromEnv() = %v, want %v", got, defaultSoakRequeueDuration)
	}
}

func TestSoakSpecTimeout(t *testing.T) {
	soak := 5 * time.Minute
	got := soakSpecTimeout(soak)
	want := soak + 2*constants.EventuallyTimeout + 5*time.Minute
	if got != want {
		t.Fatalf("soakSpecTimeout() = %v, want %v", got, want)
	}
}

func TestDefaultFullGateClosedPRsSoakScenario_noPRs(t *testing.T) {
	s := defaultFullGateClosedPRsSoakScenario()
	if s.TriggerChange {
		t.Fatal("closed soak must not trigger a promotion push (no PRs on SCM)")
	}
	if s.PRPosture != prPostureClosed {
		t.Fatalf("PRPosture = %q, want %q", s.PRPosture, prPostureClosed)
	}
}

func TestDefaultFullGateSoak60mRequeueScenarios(t *testing.T) {
	open := defaultFullGateOpenPRsSoak60mRequeueScenario()
	if open.RequeueDuration != defaultSoakLongRequeueDuration {
		t.Fatalf("open 60m soak requeue = %v, want %v", open.RequeueDuration, defaultSoakLongRequeueDuration)
	}
	if open.PRPosture != prPostureOpen || !open.TriggerChange {
		t.Fatal("open 60m soak must match open PR soak posture and trigger change")
	}

	closed := defaultFullGateClosedPRsSoak60mRequeueScenario()
	if closed.RequeueDuration != defaultSoakLongRequeueDuration {
		t.Fatalf("closed 60m soak requeue = %v, want %v", closed.RequeueDuration, defaultSoakLongRequeueDuration)
	}
	if closed.PRPosture != prPostureClosed || closed.TriggerChange {
		t.Fatal("closed 60m soak must match closed PR soak posture and not trigger change")
	}
}
