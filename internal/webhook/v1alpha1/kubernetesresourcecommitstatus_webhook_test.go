/*
Copyright 2026.

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

package v1alpha1

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("KubernetesResourceCommitStatus webhook", func() {
	gate := func(name, templateYAML string) *promoterv1alpha1.KubernetesResourceCommitStatus {
		raw, err := yaml.YAMLToJSON([]byte(templateYAML))
		Expect(err).NotTo(HaveOccurred())
		return &promoterv1alpha1.KubernetesResourceCommitStatus{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: promoterv1alpha1.KubernetesResourceCommitStatusSpec{
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "ps"},
				Key:                  "smoke",
				Template:             apiextensionsv1.JSON{Raw: raw},
				Phase:                promoterv1alpha1.KubernetesResourcePhaseSpec{Expression: "true"},
			},
		}
	}

	const validJob = `
apiVersion: batch/v1
kind: Job
metadata:
  labels: {sha: "${ ShortSha }"}
spec:
  backoffLimit: 0
  parallelism: "${ 1 + 1 }"
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: test
          image: busybox
          args: ["--sha", "${ Sha }"]
          resources: {limits: {cpu: 1, memory: 64Mi}}
`

	It("admits a valid template and updates of it", func() {
		krcs := gate("valid", validJob)
		Expect(k8sClient.Create(ctx, krcs)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, krcs) })

		krcs.Labels = map[string]string{"team": "a"}
		Expect(k8sClient.Update(ctx, krcs)).To(Succeed())
	})

	It("rejects unknown fields and invalid literal values on create with their paths", func() {
		err := k8sClient.Create(ctx, gate("typo", `
apiVersion: batch/v1
kind: Job
spec:
  backofLimit: 0
  completionMode: Sometimes
  suspend: "${ false }"
  template:
    spec:
      restartPolicy: Nevr
      containers:
        - name: test
          imag: busybox
`))
		Expect(k8serrors.IsInvalid(err)).To(BeTrue(), "expected Invalid, got %v", err)
		Expect(err.Error()).To(ContainSubstring(`spec.template.spec.backofLimit: Invalid value: unknown field: the schema does not declare it (did you mean "backoffLimit"?)`))
		Expect(err.Error()).To(ContainSubstring(`spec.template.spec.template.spec.containers[0].imag: Invalid value: unknown field`))
		Expect(err.Error()).To(ContainSubstring(`spec.template.spec.completionMode: Unsupported value: "Sometimes"`))
		Expect(err.Error()).To(ContainSubstring(`spec.template.spec.template.spec.restartPolicy: Unsupported value: "Nevr"`))
		Expect(err.Error()).NotTo(ContainSubstring("suspend"))
	})

	It("rejects an update that introduces an unknown field", func() {
		krcs := gate("update", validJob)
		Expect(k8sClient.Create(ctx, krcs)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, krcs) })

		raw, err := yaml.YAMLToJSON([]byte(`{apiVersion: batch/v1, kind: Job, spec: {parallelsm: 1}}`))
		Expect(err).NotTo(HaveOccurred())
		krcs.Spec.Template = apiextensionsv1.JSON{Raw: raw}
		err = k8sClient.Update(ctx, krcs)
		Expect(k8serrors.IsInvalid(err)).To(BeTrue(), "expected Invalid, got %v", err)
		Expect(err.Error()).To(ContainSubstring(`spec.template.spec.parallelsm`))
	})

	It("rejects templates the controller would not create", func() {
		err := k8sClient.Create(ctx, gate("no-kind", `{apiVersion: batch/v1, spec: {}}`))
		Expect(k8serrors.IsInvalid(err)).To(BeTrue(), "expected Invalid, got %v", err)
		Expect(err.Error()).To(ContainSubstring("template is missing kind"))

		err = k8sClient.Create(ctx, gate("unconfigured", `{apiVersion: apps/v1, kind: Deployment}`))
		Expect(k8serrors.IsInvalid(err)).To(BeTrue(), "expected Invalid, got %v", err)
		Expect(err.Error()).To(ContainSubstring("not create this kind"))
	})
})
