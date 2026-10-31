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

package webhook

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// jobTemplate returns a batch/v1 Job template with extra YAML merged under spec.
func jobTemplate(specExtra string) string {
	return `
apiVersion: batch/v1
kind: Job
metadata:
  labels:
    short-sha: ${ ShortSha }
spec:
  backoffLimit: 0
  parallelism: ${ 1 + 1 }
` + indent(specExtra, "  ") + `
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: smoke
          image: busybox
          args: ["${ Sha }", "--branch=${ Branch }"]
`
}

func indent(s, prefix string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

const jobPhase = "Resource.?status.?succeeded.orValue(0) > 0"

func newGate(name, template, phase string) *promoterv1alpha1.KubernetesResourceCommitStatus {
	GinkgoHelper()
	raw, err := yaml.YAMLToJSON([]byte(template))
	Expect(err).NotTo(HaveOccurred())
	return &promoterv1alpha1.KubernetesResourceCommitStatus{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: promoterv1alpha1.KubernetesResourceCommitStatusSpec{
			PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "ps"},
			Key:                  "smoke",
			Template:             apiextensionsv1.JSON{Raw: raw},
			Phase:                promoterv1alpha1.KubernetesResourcePhaseSpec{Expression: phase},
		},
	}
}

var _ = Describe("KubernetesResourceCommitStatus validating webhook", func() {
	var created []client.Object
	AfterEach(func() {
		for _, obj := range created {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, obj))).To(Succeed())
		}
		created = nil
	})
	create := func(gate *promoterv1alpha1.KubernetesResourceCommitStatus) error {
		if err := warnClient.Create(ctx, gate); err != nil {
			return fmt.Errorf("create: %w", err)
		}
		created = append(created, gate)
		return nil
	}
	expectDenied := func(err error, substrings ...string) {
		GinkgoHelper()
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), err.Error())
		Expect(err.Error()).To(ContainSubstring("denied the request"))
		for _, s := range substrings {
			Expect(err.Error()).To(ContainSubstring(s))
		}
	}

	It("accepts a gate whose template and expressions match the kind's schema", func() {
		Expect(create(newGate("valid", jobTemplate("suspend: ${ Branch == 'environment/development' }"), jobPhase))).To(Succeed())
		Expect(warnings.drain()).To(BeEmpty())
	})

	It("rejects an expression whose type does not match the field", func() {
		err := create(newGate("wrong-type", jobTemplate("parallelism: ${ Sha }"), jobPhase))
		expectDenied(err, "spec.template.spec.parallelism", `expression "Sha" returns string but the field expects int`)
	})

	It("rejects a literal of the wrong type", func() {
		err := create(newGate("wrong-literal", jobTemplate(`parallelism: "2"`), jobPhase))
		expectDenied(err, "spec.template.spec.parallelism", "literal returns string but the field expects int")
	})

	It("rejects template fields the kind's schema does not know", func() {
		err := create(newGate("unknown-field", jobTemplate("paralellism: 1"), jobPhase))
		expectDenied(err, "spec.template.spec.paralellism", "unknown field")
	})

	It("rejects an unknown variable and an unknown field of a typed variable", func() {
		err := create(newGate("unknown-variable", jobTemplate("activeDeadlineSeconds: ${ Environment.propsed.hydrated.sha.size() }"), jobPhase))
		expectDenied(err, "spec.template.spec.activeDeadlineSeconds", "undefined field 'propsed'")
	})

	It("rejects a template without a literal kind", func() {
		err := create(newGate("no-kind", "{apiVersion: batch/v1, spec: {}}", jobPhase))
		expectDenied(err, "spec.template", "template is missing kind")
	})

	It("rejects a kind the controller is not configured to create", func() {
		err := create(newGate("unconfigured", "{apiVersion: apps/v1, kind: Deployment, spec: {}}", "true"))
		expectDenied(err, "spec.template.kind", "not listed in ControllerConfiguration")
	})

	It("rejects a kind the cluster does not serve", func() {
		err := create(newGate("unserved", "{apiVersion: example.com/v1, kind: Widget, spec: {}}", "true"))
		expectDenied(err, "spec.template.kind", "the cluster does not serve this kind")
	})

	It("rejects a phase expression that does not compile or returns the wrong type", func() {
		err := create(newGate("bad-phase", jobTemplate(""), "Resource.status.nope == 1"))
		expectDenied(err, "spec.phase.expression", "undefined field 'nope'")
		err = create(newGate("string-phase", jobTemplate(""), "'success'"))
		expectDenied(err, "spec.phase.expression", "must return a boolean or an object")
	})

	It("rejects description and url expressions that do not compile", func() {
		gate := newGate("bad-report", jobTemplate(""), jobPhase)
		gate.Spec.Description = "Job ${ ResourceName } is ${ Phasee }"
		gate.Spec.Url = "https://ci.example.com/${ Resource.status.nope }"
		err := create(gate)
		expectDenied(err, "spec.description", "undeclared reference to 'Phasee'", "spec.url", "undefined field 'nope'")
	})

	It("warns about template fields the controller ignores", func() {
		Expect(create(newGate("warned", `
apiVersion: v1
kind: ConfigMap
metadata:
  name: ignored
  labels:
    branch: ${ Branch }
data:
  sha: ${ Sha }
status: {}
`, "true"))).To(Succeed())
		Expect(warnings.drain()).To(ConsistOf(
			ContainSubstring("spec.template.metadata.name is ignored"),
			ContainSubstring("spec.template.status is ignored"),
		))
	})

	It("validates spec changes on update but lets metadata-only updates through", func() {
		gate := newGate("updated", jobTemplate(""), jobPhase)
		Expect(create(gate)).To(Succeed())

		// Simulate the kind disappearing from the controller's configuration after the gate was created.
		delete(validator.Configured, jobGVK)
		defer func() { validator.Configured[jobGVK] = struct{}{} }()

		gate.Labels = map[string]string{"team": "a"}
		Expect(k8sClient.Update(ctx, gate)).To(Succeed(), "metadata-only update is not validated")

		gate.Spec.Key = "smoke-2"
		err := k8sClient.Update(ctx, gate)
		expectDenied(err, "spec.template.kind", "not listed in ControllerConfiguration")
	})

	It("rejects an update that introduces a type error", func() {
		gate := newGate("updated-type", jobTemplate(""), jobPhase)
		Expect(create(gate)).To(Succeed())
		broken := newGate("updated-type", jobTemplate("parallelism: ${ Sha }"), jobPhase)
		gate.Spec.Template = broken.Spec.Template
		err := k8sClient.Update(ctx, gate)
		expectDenied(err, "spec.template.spec.parallelism", "returns string but the field expects int")
	})

	It("ignores deletes", func() {
		gate := newGate("deleted", jobTemplate(""), jobPhase)
		Expect(create(gate)).To(Succeed())
		Expect(k8sClient.Delete(ctx, gate)).To(Succeed())
	})
})
