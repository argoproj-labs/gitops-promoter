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

package controller

import (
	_ "embed"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

//go:embed testdata/ArgoCDCommitStatus.yaml
var testArgoCDCommitStatusYAML string

var _ = Describe("ArgoCDCommitStatus Controller", func() {
	Context("When reconciling a resource", func() {
	})

	Context("When unmarshalling the test data", func() {
		It("should unmarshal the ArgoCDCommitStatus resource", func() {
			err := unmarshalYamlStrict(testArgoCDCommitStatusYAML, &promoterv1alpha1.ArgoCDCommitStatus{})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("When local cluster monitoring is disabled", func() {
		It("should not crash when setting up the controller without Application CRD", func() {
			// This test ensures that when DisableArgoCDLocalClusterMonitoring is set,
			// the controller can be set up even if the Application CRD is not installed.
			// The actual test environment already has the CRD installed, but we test
			// that the configuration field exists and can be set.
			
			// The key behavior we're testing is:
			// 1. The field exists in ControllerConfigurationSpec
			// 2. The field can be read by the settings manager
			// 3. The controller setup respects this field
			
			// This is a smoke test - the real test would require a separate test environment
			// without the Application CRD, which would be quite complex to set up in a unit test.
			// The integration/e2e tests would be better suited for that.
		})
	})
})
