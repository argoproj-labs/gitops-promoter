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
	"os"
	"path/filepath"
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
		expectDenied(err, "spec.template.spec.paralellism", "unknown field", `(did you mean "parallelism"?)`)
	})

	It("rejects an object in the template that lacks a required field", func() {
		err := create(newGate("missing-required", `
apiVersion: batch/v1
kind: Job
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
        - image: busybox
`, jobPhase))
		expectDenied(err, "spec.template.spec.template.spec.containers[0].name: Required value")
	})

	It("accepts required fields computed by an expression", func() {
		Expect(create(newGate("required-from-expression", `
apiVersion: batch/v1
kind: Job
spec:
  template:
    spec:
      restartPolicy: Never
      containers: "${ [{'name': 'smoke', 'image': 'busybox'}] }"
`, jobPhase))).To(Succeed())
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

// These specs run the webhook against real third-party CRDs (see test/external_crds and test/crd_variants): Argo
// Workflows' Workflow in both its full (typed) and minimal (loose) variants, and Tekton's PipelineRun. Adapted from
// PR #2343.
var _ = Describe("KubernetesResourceCommitStatus validating webhook with third-party kinds", func() {
	// workflowTemplate is a Workflow as a gate would create it; apiVersion is filled in per variant.
	const workflowTemplate = `
apiVersion: %s
kind: Workflow
metadata:
  labels: {sha: "${ ShortSha }"}
spec:
  entrypoint: smoke
  activeDeadlineSeconds: 600
  arguments:
    parameters:
      - name: sha
        value: "${ Sha }"
      - name: branch
        value: "${ Branch }"
  templates:
    - name: smoke
      container:
        image: ghcr.io/acme/smoke:latest
        args: ["--sha", "{{workflow.parameters.sha}}"]
`
	workflow := func(apiVersion string, replacements ...string) string {
		out := strings.Replace(workflowTemplate, "%s", apiVersion, 1)
		for i := 0; i+1 < len(replacements); i += 2 {
			out = strings.Replace(out, replacements[i], replacements[i+1], 1)
		}
		return out
	}
	typos := []string{"entrypoint:", "entrypont:", "container:", "cntainer:", "activeDeadlineSeconds: 600", "activeDeadlineSeconds: soon"}

	// pipelineRun is the template of the documented Tekton example, which the "documented examples" spec admits
	// as published; here it is the base for a typo.
	const pipelineRun = `
apiVersion: tekton.dev/v1
kind: PipelineRun
spec:
  pipelineRef:
    name: security-scan
  taskRunTemplate:
    serviceAccountName: tekton-runner
  params:
    - name: gitUrl
      value: "https://github.com/myorg/myrepo/commit/${ Sha }"
`
	const workflowPhase = "Resource.?status.?phase.orValue('') == 'Succeeded'"

	create := func(name, template, phase string) error {
		gate := newGate(name, template, phase)
		err := k8sClient.Create(ctx, gate)
		if err == nil {
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gate) })
		}
		return err //nolint:wrapcheck // the spec inspects the API server's error
	}

	Context("Argo Workflows, full (typed) CRD", func() {
		It("admits a Workflow", func() {
			Expect(create("wf-full", workflow("argoproj.io/v1alpha1"), workflowPhase)).To(Succeed())
		})

		It("rejects unknown fields and invalid literal values with their paths", func() {
			err := create("wf-full-typo", workflow("argoproj.io/v1alpha1", typos...), workflowPhase)
			Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected Invalid, got %v", err)
			Expect(err.Error()).To(ContainSubstring(`spec.template.spec.entrypont: Invalid value: "entrypont": unknown field`))
			Expect(err.Error()).To(ContainSubstring(`(did you mean "entrypoint"?)`))
			Expect(err.Error()).To(ContainSubstring(`spec.template.spec.templates[0].cntainer: Invalid value: "cntainer": unknown field`))
			Expect(err.Error()).To(ContainSubstring(`spec.template.spec.activeDeadlineSeconds`))
		})

		It("types Resource from the CRD's schema in the phase expression", func() {
			err := create("wf-full-phase", workflow("argoproj.io/v1alpha1"), "Resource.?status.?phse.orValue('') == 'Succeeded'")
			Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected Invalid, got %v", err)
			Expect(err.Error()).To(ContainSubstring("spec.phase.expression"))
		})
	})

	Context("Argo Workflows, minimal (loose) CRD", func() {
		It("admits the same typos, because its spec preserves unknown fields", func() {
			Expect(create("wf-minimal", workflow(minimalWorkflowGroup+"/v1alpha1"), workflowPhase)).To(Succeed())
			Expect(create("wf-minimal-typo", workflow(minimalWorkflowGroup+"/v1alpha1", typos...), workflowPhase)).To(Succeed())
		})
	})

	Context("Tekton PipelineRun", func() {
		It("rejects unknown fields where the CRD's schema declares the object", func() {
			err := create("tekton-typo", strings.Replace(pipelineRun, "taskRunTemplate:", "taskRunTemplates:", 1), "true")
			Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected Invalid, got %v", err)
			Expect(err.Error()).To(ContainSubstring(`spec.template.spec.taskRunTemplates: Invalid value: "taskRunTemplates": unknown field`))
			Expect(err.Error()).To(ContainSubstring(`(did you mean "taskRunTemplate"?)`))
		})
	})
})

// The gate's documentation includes internal/controller/testdata/KubernetesResourceCommitStatus*.yaml verbatim; the
// controller suite runs them, and this spec checks that the webhook admits them as published, against the real
// third-party CRDs.
var _ = Describe("KubernetesResourceCommitStatus validating webhook with the documented examples", func() {
	examples, err := filepath.Glob(filepath.Join("..", "controller", "testdata", "KubernetesResourceCommitStatus*.yaml"))
	if err != nil {
		panic(err)
	}
	if len(examples) < 4 {
		panic(fmt.Sprintf("expected the Job, Argo Workflows, Tekton, and Argo Rollouts examples, found %v", examples))
	}
	for _, path := range examples {
		It("admits "+filepath.Base(path)+" without warnings", func() {
			raw, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			var gate promoterv1alpha1.KubernetesResourceCommitStatus
			Expect(yaml.UnmarshalStrict(raw, &gate)).To(Succeed())
			gate.Namespace = "default"
			Expect(warnClient.Create(ctx, &gate)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, &gate) })
			Expect(warnings.drain()).To(BeEmpty())
		})
	}
})
