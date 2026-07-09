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
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/argoproj-labs/gitops-promoter/common"
)

// buildInfo is a gauge that always has value 1 and carries version information as const labels.
var buildInfo = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: "promoter_build_info",
		Help: "Current build information for the GitOps Promoter controller. The gauge value is always 1; " +
			"version information is in the labels.",
		ConstLabels: prometheus.Labels{
			"version":    common.Version(),
			"build_date": common.BuildDate(),
		},
	},
)

func init() {
	metrics.Registry.MustRegister(buildInfo)
	buildInfo.Set(1)
}
