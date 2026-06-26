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
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	promotermetrics "github.com/argoproj-labs/gitops-promoter/internal/metrics"
	dto "github.com/prometheus/client_model/go"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const gitOperationsMetricName = "git_operations_total"

// gitOperationSnapshot reads git_operations_total from the controller-runtime prometheus
// registry and sums success+failure per subcommand for one GitRepository name.
func gitOperationSnapshot(gitRepo *promoterv1alpha1.GitRepository) map[string]float64 {
	if gitRepo == nil {
		return nil
	}
	out := make(map[string]float64)
	mfs, err := metrics.Registry.Gather()
	if err != nil {
		return out
	}
	for _, mf := range mfs {
		if mf.GetName() != gitOperationsMetricName {
			continue
		}
		for _, m := range mf.GetMetric() {
			if labelValue(m, "git_repository") != gitRepo.Name {
				continue
			}
			op := labelValue(m, "operation")
			if op == "" {
				continue
			}
			out[op] += m.GetCounter().GetValue()
		}
	}
	return out
}

func gitOperationDelta(before, after map[string]float64) map[string]float64 {
	delta := make(map[string]float64)
	for op, count := range after {
		delta[op] = count - before[op]
	}
	for op, count := range before {
		if _, ok := after[op]; !ok && count > 0 {
			delta[op] = -count
		}
	}
	return delta
}

func gitOperationTotal(breakdown map[string]float64) float64 {
	var total float64
	for _, count := range breakdown {
		total += count
	}
	return total
}

func gitOperationNetworkTotal(breakdown map[string]float64) float64 {
	var total float64
	for op, count := range breakdown {
		if promotermetrics.GitOperationHitsNetwork(promotermetrics.GitOperation(op)) {
			total += count
		}
	}
	return total
}

func labelValue(m *dto.Metric, name string) string {
	for _, lp := range m.GetLabel() {
		if lp.GetName() == name {
			return lp.GetValue()
		}
	}
	return ""
}
