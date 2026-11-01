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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
)

// These specs run the webhook against real third-party CRDs (see test/external_crds and test/crd_variants): Argo
// Workflows' Workflow in both its full (typed) and minimal (loose) variants, and Tekton's PipelineRun.
var _ = Describe("KubernetesResourceCommitStatus webhook with third-party kinds", func() {
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

	// pipelineRun is the PipelineRun from the gate's documentation.
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
    - name: branch
      value: ${ Branch }
    - name: endpoints
      value: ["api/health", "api/users"]
`

	create := func(name, template string) error {
		krcs := gate(name, template)
		err := k8sClient.Create(ctx, krcs)
		if err == nil {
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, krcs) })
		}
		return err //nolint:wrapcheck // the spec inspects the API server's error
	}

	Context("Argo Workflows, full (typed) CRD", func() {
		It("admits a Workflow", func() {
			Expect(create("wf-full", workflow("argoproj.io/v1alpha1"))).To(Succeed())
		})

		It("rejects unknown fields and invalid literal values with their paths", func() {
			err := create("wf-full-typo", workflow("argoproj.io/v1alpha1", typos...))
			Expect(k8serrors.IsInvalid(err)).To(BeTrue(), "expected Invalid, got %v", err)
			Expect(err.Error()).To(ContainSubstring(`spec.template.spec.entrypont: Invalid value: unknown field`))
			Expect(err.Error()).To(ContainSubstring(`(did you mean "entrypoint"?)`))
			Expect(err.Error()).To(ContainSubstring(`spec.template.spec.templates[0].cntainer: Invalid value: unknown field`))
			Expect(err.Error()).To(ContainSubstring(`spec.template.spec.activeDeadlineSeconds`))
		})
	})

	Context("Argo Workflows, minimal (loose) CRD", func() {
		It("admits the same typos, because its spec preserves unknown fields", func() {
			Expect(create("wf-minimal", workflow(minimalWorkflowGroup+"/v1alpha1"))).To(Succeed())
			Expect(create("wf-minimal-typo", workflow(minimalWorkflowGroup+"/v1alpha1", typos...))).To(Succeed())
		})
	})

	Context("Tekton PipelineRun", func() {
		It("admits the PipelineRun from the documentation", func() {
			Expect(create("tekton", pipelineRun)).To(Succeed())
		})

		It("rejects unknown fields where the CRD's schema declares the object", func() {
			err := create("tekton-typo", strings.Replace(pipelineRun, "taskRunTemplate:", "taskRunTemplates:", 1))
			Expect(k8serrors.IsInvalid(err)).To(BeTrue(), "expected Invalid, got %v", err)
			Expect(err.Error()).To(ContainSubstring(`spec.template.spec.taskRunTemplates: Invalid value: unknown field`))
			Expect(err.Error()).To(ContainSubstring(`(did you mean "taskRunTemplate"?)`))
		})
	})
})
