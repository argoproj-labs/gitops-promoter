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

import "github.com/prometheus/client_golang/prometheus"

// GetDORAMetricForTesting returns the underlying Prometheus metric collector for testing purposes.
// This function should only be used in tests.
func GetDORAMetricForTesting(metricName string) prometheus.Collector {
	switch metricName {
	case "dora_deployments_total":
		return deploymentsToProductionTotal
	case "dora_lead_time_seconds":
		return leadTimeForChangesSeconds
	case "dora_change_failure_rate_total":
		return changeFailureRateTotal
	case "dora_mean_time_to_restore_seconds":
		return meanTimeToRestoreSeconds
	default:
		return nil
	}
}
