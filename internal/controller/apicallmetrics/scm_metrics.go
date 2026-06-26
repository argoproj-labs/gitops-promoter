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
	"fmt"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const scmCallsMetricName = "scm_calls_total"

// scmCallSnapshot reads scm_calls_total from the controller-runtime prometheus registry and
// sums counts per api/operation for one GitRepository name (across response_code labels).
func scmCallSnapshot(gitRepo *promoterv1alpha1.GitRepository) map[string]float64 {
	if gitRepo == nil {
		return nil
	}
	out := make(map[string]float64)
	mfs, err := metrics.Registry.Gather()
	if err != nil {
		return out
	}
	for _, mf := range mfs {
		if mf.GetName() != scmCallsMetricName {
			continue
		}
		for _, m := range mf.GetMetric() {
			if labelValue(m, "git_repository") != gitRepo.Name {
				continue
			}
			api := labelValue(m, "api")
			op := labelValue(m, "operation")
			if api == "" || op == "" {
				continue
			}
			key := fmt.Sprintf("%s/%s", api, op)
			out[key] += m.GetCounter().GetValue()
		}
	}
	return out
}

func scmCallDelta(before, after map[string]float64) map[string]float64 {
	return gitOperationDelta(before, after)
}

func scmCallTotal(breakdown map[string]float64) float64 {
	return gitOperationTotal(breakdown)
}
