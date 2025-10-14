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

package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestDoraMetrics(t *testing.T) {
	// Create a new registry for testing to avoid conflicts
	testRegistry := prometheus.NewRegistry()

	// Create new metrics for testing
	testDeploymentsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "test_dora_deployments_total",
			Help: "Test deployment counter",
		},
		[]string{"promotion_strategy", "environment", "is_terminal"},
	)

	testLeadTime := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "test_dora_lead_time_seconds",
			Help: "Test lead time gauge",
		},
		[]string{"promotion_strategy", "environment", "is_terminal"},
	)

	testChangeFailures := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "test_dora_change_failure_rate_total",
			Help: "Test change failure counter",
		},
		[]string{"promotion_strategy", "environment", "is_terminal"},
	)

	testMTTR := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "test_dora_mean_time_to_restore_seconds",
			Help: "Test MTTR gauge",
		},
		[]string{"promotion_strategy", "environment", "is_terminal"},
	)

	testRegistry.MustRegister(testDeploymentsTotal, testLeadTime, testChangeFailures, testMTTR)

	t.Run("DeploymentCounter", func(t *testing.T) {
		labels := prometheus.Labels{
			"promotion_strategy": "test-strategy",
			"environment":        "production",
			"is_terminal":        "true",
		}

		testDeploymentsTotal.With(labels).Inc()
		testDeploymentsTotal.With(labels).Inc()

		count := testutil.ToFloat64(testDeploymentsTotal.With(labels))
		if count != 2 {
			t.Errorf("Expected deployment count to be 2, got %f", count)
		}
	})

	t.Run("LeadTimeGauge", func(t *testing.T) {
		labels := prometheus.Labels{
			"promotion_strategy": "test-strategy",
			"environment":        "production",
			"is_terminal":        "true",
		}

		testLeadTime.With(labels).Set(120.5)

		value := testutil.ToFloat64(testLeadTime.With(labels))
		if value != 120.5 {
			t.Errorf("Expected lead time to be 120.5, got %f", value)
		}
	})

	t.Run("ChangeFailureCounter", func(t *testing.T) {
		labels := prometheus.Labels{
			"promotion_strategy": "test-strategy",
			"environment":        "staging",
			"is_terminal":        "false",
		}

		testChangeFailures.With(labels).Inc()

		count := testutil.ToFloat64(testChangeFailures.With(labels))
		if count != 1 {
			t.Errorf("Expected failure count to be 1, got %f", count)
		}
	})

	t.Run("MTTRGauge", func(t *testing.T) {
		labels := prometheus.Labels{
			"promotion_strategy": "test-strategy",
			"environment":        "production",
			"is_terminal":        "true",
		}

		testMTTR.With(labels).Set(300.0)

		value := testutil.ToFloat64(testMTTR.With(labels))
		if value != 300.0 {
			t.Errorf("Expected MTTR to be 300.0, got %f", value)
		}
	})

	t.Run("MultipleEnvironments", func(t *testing.T) {
		// Test that we can track metrics for multiple environments independently
		prodLabels := prometheus.Labels{
			"promotion_strategy": "test-strategy",
			"environment":        "production",
			"is_terminal":        "true",
		}
		stagingLabels := prometheus.Labels{
			"promotion_strategy": "test-strategy",
			"environment":        "staging",
			"is_terminal":        "false",
		}

		testDeploymentsTotal.With(prodLabels).Inc()
		testDeploymentsTotal.With(prodLabels).Inc()
		testDeploymentsTotal.With(stagingLabels).Inc()

		prodCount := testutil.ToFloat64(testDeploymentsTotal.With(prodLabels))
		stagingCount := testutil.ToFloat64(testDeploymentsTotal.With(stagingLabels))

		if prodCount != 4 { // 2 from previous test + 2 new
			t.Errorf("Expected production deployment count to be 4, got %f", prodCount)
		}
		if stagingCount != 1 {
			t.Errorf("Expected staging deployment count to be 1, got %f", stagingCount)
		}
	})
}

func TestRecordFunctions(t *testing.T) {
	// These tests verify that the record functions can be called without panicking
	// The actual metric values are tested via the existing metrics registry

	t.Run("RecordDeployment", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("RecordDeployment panicked: %v", r)
			}
		}()

		RecordDeployment("test-ps", "test-env", true)
	})

	t.Run("RecordLeadTime", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("RecordLeadTime panicked: %v", r)
			}
		}()

		RecordLeadTime("test-ps", "test-env", false, 123.45)
	})

	t.Run("RecordChangeFailure", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("RecordChangeFailure panicked: %v", r)
			}
		}()

		RecordChangeFailure("test-ps", "test-env", true)
	})

	t.Run("RecordMeanTimeToRestore", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("RecordMeanTimeToRestore panicked: %v", r)
			}
		}()

		RecordMeanTimeToRestore("test-ps", "test-env", false, 567.89)
	})
}
