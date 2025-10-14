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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

var _ = Describe("DORA Metrics", func() {
	var (
		testRegistry         *prometheus.Registry
		testDeploymentsTotal *prometheus.CounterVec
		testLeadTime         *prometheus.GaugeVec
		testChangeFailures   *prometheus.CounterVec
		testMTTR             *prometheus.GaugeVec
	)

	BeforeEach(func() {
		// Create a new registry for testing to avoid conflicts
		testRegistry = prometheus.NewRegistry()

		// Create new metrics for testing
		testDeploymentsTotal = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "test_dora_deployments_total",
				Help: "Test deployment counter",
			},
			[]string{"promotion_strategy", "namespace", "environment", "is_terminal"},
		)

		testLeadTime = prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "test_dora_lead_time_seconds",
				Help: "Test lead time gauge",
			},
			[]string{"promotion_strategy", "namespace", "environment", "is_terminal"},
		)

		testChangeFailures = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "test_dora_change_failure_rate_total",
				Help: "Test change failure counter",
			},
			[]string{"promotion_strategy", "namespace", "environment", "is_terminal"},
		)

		testMTTR = prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "test_dora_mean_time_to_restore_seconds",
				Help: "Test MTTR gauge",
			},
			[]string{"promotion_strategy", "namespace", "environment", "is_terminal"},
		)

		testRegistry.MustRegister(testDeploymentsTotal, testLeadTime, testChangeFailures, testMTTR)
	})

	Context("Deployment Counter", func() {
		It("should increment deployment counter", func() {
			labels := prometheus.Labels{
				"promotion_strategy": "test-strategy",
				"namespace":          "test-namespace",
				"environment":        "production",
				"is_terminal":        "true",
			}

			testDeploymentsTotal.With(labels).Inc()
			testDeploymentsTotal.With(labels).Inc()

			count := testutil.ToFloat64(testDeploymentsTotal.With(labels))
			Expect(count).To(Equal(float64(2)))
		})
	})

	Context("Lead Time Gauge", func() {
		It("should set lead time gauge value", func() {
			labels := prometheus.Labels{
				"promotion_strategy": "test-strategy",
				"namespace":          "test-namespace",
				"environment":        "production",
				"is_terminal":        "true",
			}

			testLeadTime.With(labels).Set(120.5)

			value := testutil.ToFloat64(testLeadTime.With(labels))
			Expect(value).To(Equal(120.5))
		})
	})

	Context("Change Failure Counter", func() {
		It("should increment change failure counter", func() {
			labels := prometheus.Labels{
				"promotion_strategy": "test-strategy",
				"namespace":          "test-namespace",
				"environment":        "staging",
				"is_terminal":        "false",
			}

			testChangeFailures.With(labels).Inc()

			count := testutil.ToFloat64(testChangeFailures.With(labels))
			Expect(count).To(Equal(float64(1)))
		})
	})

	Context("MTTR Gauge", func() {
		It("should set MTTR gauge value", func() {
			labels := prometheus.Labels{
				"promotion_strategy": "test-strategy",
				"namespace":          "test-namespace",
				"environment":        "production",
				"is_terminal":        "true",
			}

			testMTTR.With(labels).Set(300.0)

			value := testutil.ToFloat64(testMTTR.With(labels))
			Expect(value).To(Equal(300.0))
		})
	})

	Context("Multiple Environments", func() {
		It("should track metrics for multiple environments independently", func() {
			prodLabels := prometheus.Labels{
				"promotion_strategy": "test-strategy",
				"namespace":          "test-namespace",
				"environment":        "production",
				"is_terminal":        "true",
			}
			stagingLabels := prometheus.Labels{
				"promotion_strategy": "test-strategy",
				"namespace":          "test-namespace",
				"environment":        "staging",
				"is_terminal":        "false",
			}

			testDeploymentsTotal.With(prodLabels).Inc()
			testDeploymentsTotal.With(prodLabels).Inc()
			testDeploymentsTotal.With(stagingLabels).Inc()

			prodCount := testutil.ToFloat64(testDeploymentsTotal.With(prodLabels))
			stagingCount := testutil.ToFloat64(testDeploymentsTotal.With(stagingLabels))

			Expect(prodCount).To(Equal(float64(2)))
			Expect(stagingCount).To(Equal(float64(1)))
		})
	})
})

var _ = Describe("Record Functions", func() {
	Context("RecordDeployment", func() {
		It("should not panic when called", func() {
			Expect(func() {
				RecordDeployment("test-ps", "test-ns", "test-env", true)
			}).ToNot(Panic())
		})
	})

	Context("RecordLeadTime", func() {
		It("should not panic when called", func() {
			Expect(func() {
				RecordLeadTime("test-ps", "test-ns", "test-env", false, 123.45)
			}).ToNot(Panic())
		})
	})

	Context("RecordChangeFailure", func() {
		It("should not panic when called", func() {
			Expect(func() {
				RecordChangeFailure("test-ps", "test-ns", "test-env", true)
			}).ToNot(Panic())
		})
	})

	Context("RecordMeanTimeToRestore", func() {
		It("should not panic when called", func() {
			Expect(func() {
				RecordMeanTimeToRestore("test-ps", "test-ns", "test-env", false, 567.89)
			}).ToNot(Panic())
		})
	})
})
