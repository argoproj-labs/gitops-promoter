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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/resourcetemplate"
)

// complexJobTemplate computes lists, maps, optionals, quantities, and nested objects from the Promoter variables.
// Every branch of every expression is exercised by rendering it for development and for production.
const complexJobTemplate = `
apiVersion: batch/v1
kind: Job
metadata:
  labels:
    environment: ${ Branch.replace('/', '-') }
    tier: "${ Branch.endsWith('production') ? 'critical' : 'standard' }"
  annotations:
    promoter.example.com/dry-sha: ${ Environment.proposed.dry.sha }
    promoter.example.com/strategy: ${ PromotionStrategy.metadata.name }
    promoter.example.com/environments: ${ PromotionStrategy.spec.environments.map(e, e.branch).join(',') }
spec:
  backoffLimit: "${ Branch.endsWith('development') ? 0 : 2 }"
  activeDeadlineSeconds: "${ Branch.endsWith('production') ? optional.of(1800) : optional.none() }"
  ttlSecondsAfterFinished: ${ duration('24h').getSeconds() }
  parallelism: ${ PromotionStrategy.spec.environments.map(e, e.branch).indexOf(Branch) + 1 }
  completions: ${ PromotionStrategy.spec.environments.map(e, e.branch).indexOf(Branch) + 1 }
  suspend: ${ false }
  podFailurePolicy:
    rules:
      - action: FailJob
        onExitCodes:
          operator: In
          values: ${ [42, 43] }
  template:
    metadata:
      labels:
        sha: ${ ShortSha }
    spec:
      restartPolicy: Never
      serviceAccountName: smoke-${ Branch.replace('environment/', '') }
      nodeSelector: "${ Branch.endsWith('production') ? {'topology.kubernetes.io/zone': 'us-east-1a'} : {} }"
      tolerations: "${ Branch.endsWith('production') ? [{'key': 'dedicated', 'operator': 'Equal', 'value': 'ci', 'effect': 'NoSchedule'}] : [] }"
      containers:
        - name: smoke
          image: ghcr.io/acme/smoke:${ ShortSha }
          imagePullPolicy: "${ Branch.endsWith('development') ? 'Always' : 'IfNotPresent' }"
          args: "${ ['--sha', Sha, '--branch', Branch] + (Branch.endsWith('production') ? ['--strict'] : []) }"
          env: "${ [{'name': 'SHA', 'value': Sha}] + PromotionStrategy.spec.environments.map(e, {'name': 'ENV_' + e.branch.replace('environment/', '').upperAscii(), 'value': e.branch}) }"
          resources:
            requests:
              cpu: "${ Branch.endsWith('production') ? '500m' : '100m' }"
              memory: ${ quantity('128Mi').add(quantity('64Mi')).asInteger() }
            limits:
              memory: 256Mi
          securityContext:
            runAsNonRoot: ${ true }
            runAsUser: ${ 1000 + 7 }
          ports:
            - containerPort: ${ 8080 }
              name: http
      volumes:
        - name: config
          configMap:
            name: smoke-${ Branch.replace('environment/', '') }
            optional: ${ Branch.endsWith('development') }
`

const complexPhase = `
Resource.?status.?conditions.orValue([]).exists(c, c.type == 'Failed' && c.status == 'True')
  ? {'phase': 'failure', 'description': Resource.status.conditions.filter(c, c.type == 'Failed').map(c, c.message)[0]}
  : Resource.?status.?succeeded.orValue(0) >= Resource.?spec.?completions.orValue(1)
    ? {'phase': 'success'}
    : {'phase': 'pending', 'description': string(Resource.?status.?active.orValue(0)) + ' pods running'}
`

// templateData returns the variables for branch, with a three-environment PromotionStrategy.
func templateData(gate *promoterv1alpha1.KubernetesResourceCommitStatus, branch string) resourcetemplate.TemplateData {
	GinkgoHelper()
	ps := &promoterv1alpha1.PromotionStrategy{
		ObjectMeta: metav1.ObjectMeta{Name: "deploy", Namespace: "default"},
		Spec: promoterv1alpha1.PromotionStrategySpec{Environments: []promoterv1alpha1.Environment{
			{Branch: "environment/development"}, {Branch: "environment/staging"}, {Branch: "environment/production"},
		}},
	}
	sha := strings.Repeat("a", 40)
	env := promoterv1alpha1.EnvironmentStatus{Branch: branch, Proposed: promoterv1alpha1.CommitBranchState{
		Dry: promoterv1alpha1.CommitShaState{Sha: strings.Repeat("d", 40)}, Hydrated: promoterv1alpha1.CommitShaState{Sha: sha},
	}}
	psObject, err := resourcetemplate.ToActivationObject(ps)
	Expect(err).NotTo(HaveOccurred())
	envObject, err := resourcetemplate.ToActivationObject(env)
	Expect(err).NotTo(HaveOccurred())
	gateObject, err := resourcetemplate.ToActivationObject(gate)
	Expect(err).NotTo(HaveOccurred())
	return resourcetemplate.TemplateData{
		Branch: branch, Sha: sha, ShortSha: resourcetemplate.ShortSha(sha), ReportOn: "proposed",
		Environment: envObject, PromotionStrategy: psObject, KubernetesResourceCommitStatus: gateObject,
	}
}

// renderAndCreate renders gate's template for branch the way the controller does and asks the API server to create
// the result, returning the rendered object and the first error on the way.
func renderAndCreate(gate *promoterv1alpha1.KubernetesResourceCommitStatus, branch, name string) (*unstructured.Unstructured, error) {
	GinkgoHelper()
	template, gvk, err := resourcetemplate.ParseTemplate(gate.Spec.Template)
	Expect(err).NotTo(HaveOccurred())
	obj, err := resourcetemplate.BuildObject(resourcetemplate.BuildParams{
		Template:     template,
		Compiler:     validator.Compiler,
		ResourceType: validator.Compiler.ResourceType(gvk),
		SourceGVK:    gvk,
		Name:         name,
		Namespace:    "default",
		Labels:       map[string]string{"promoter.argoproj.io/sha": strings.Repeat("a", 40)},
		Owner:        *metav1.NewControllerRef(gate, promoterv1alpha1.GroupVersion.WithKind("KubernetesResourceCommitStatus")),
		Data:         templateData(gate, branch),
	})
	if err != nil {
		return nil, fmt.Errorf("render: %w", err)
	}
	if err := k8sClient.Create(ctx, obj); err != nil {
		return obj, fmt.Errorf("api server: %w", err)
	}
	return obj, nil
}

var _ = Describe("Complex dynamic templates", func() {
	var created []*unstructured.Unstructured
	AfterEach(func() {
		for _, obj := range created {
			_ = k8sClient.Delete(ctx, obj)
		}
		created = nil
		_ = k8sClient.Delete(ctx, newGate("complex", complexJobTemplate, complexPhase))
	})

	It("accepts the complex template and the rendered Jobs are accepted by the API server", func() {
		gate := newGate("complex", complexJobTemplate, complexPhase)
		gate.Spec.Description = "${ Phase == 'success' ? 'Smoke test passed' : ExpressionDescription }"
		Expect(warnClient.Create(ctx, gate)).To(Succeed())
		Expect(warnings.drain()).To(BeEmpty())
		for _, branch := range []string{"environment/development", "environment/production"} {
			obj, err := renderAndCreate(gate, branch, "complex-"+strings.TrimPrefix(branch, "environment/"))
			Expect(err).NotTo(HaveOccurred())
			created = append(created, obj)
			printable := obj.DeepCopy()
			unstructured.RemoveNestedField(printable.Object, "metadata", "managedFields")
			unstructured.RemoveNestedField(printable.Object, "status")
			out, _ := yaml.Marshal(printable.Object)
			GinkgoWriter.Printf("----- rendered for %s -----\n%s\n", branch, out)
		}
	})

	// A mistake is a one-line edit of the complex template. rejected is a substring of the webhook's denial; an
	// empty rejected means the webhook lets it through, and apiServer then names what the API server says about the
	// rendered Job (empty when the API server accepts it too).
	type mistake struct {
		name, old, new, rejected, apiServer string
	}
	mistakes := []mistake{
		{"strings into an int through a ternary", `? 0 : 2 }`, `? '0' : '2' }`, "spec.template.spec.backoffLimit: Invalid value: \"${ Branch.endsWith('development') ? '0' : '2' }\": expression \"Branch.endsWith('development') ? '0' : '2'\" returns string but the field expects int", ""},
		{"map where a list is expected", `tolerations: "${ Branch.endsWith('production') ? [{'key': 'dedicated', 'operator': 'Equal', 'value': 'ci', 'effect': 'NoSchedule'}] : [] }"`, `tolerations: "${ {'key': 'dedicated'} }"`, "tolerations: Invalid value: \"${ {'key': 'dedicated'} }\": expression \"{'key': 'dedicated'}\" returns map of string but the field expects a list of an object", ""},
		{"list where a map is expected", `nodeSelector: "${ Branch.endsWith('production') ? {'topology.kubernetes.io/zone': 'us-east-1a'} : {} }"`, `nodeSelector: "${ ['a'] }"`, "returns list of string but the field expects a map of string", ""},
		{"list items of the wrong type", `args: "${ ['--sha', Sha, '--branch', Branch] + (Branch.endsWith('production') ? ['--strict'] : []) }"`, `args: "${ [1, 2] }"`, "containers[0].args: Invalid value: \"${ [1, 2] }\": expression \"[1, 2]\" returns list of int but the field expects a list of string", ""},
		{"list of objects with a mixed-type map literal", `env: "${ [{'name': 'SHA', 'value': Sha}] + PromotionStrategy.spec.environments.map(e, {'name': 'ENV_' + e.branch.replace('environment/', '').upperAscii(), 'value': e.branch}) }"`, `env: "${ [{'name': 'X', 'value': 42}] }"`, "expected type 'string' but found 'int'", ""},
		{"map values of the wrong type", `nodeSelector: "${ Branch.endsWith('production') ? {'topology.kubernetes.io/zone': 'us-east-1a'} : {} }"`, `nodeSelector: "${ {'zone': 1} }"`, "returns map of int but the field expects a map of string", ""},
		{"enum violation in a literal", `imagePullPolicy: "${ Branch.endsWith('development') ? 'Always' : 'IfNotPresent' }"`, `imagePullPolicy: Sometimes`, "containers[0].imagePullPolicy: Unsupported value: \"Sometimes\": supported values: \"Always\", \"IfNotPresent\", \"Never\"", ""},
		{"enum violation in an expression", `imagePullPolicy: "${ Branch.endsWith('development') ? 'Always' : 'IfNotPresent' }"`, `imagePullPolicy: "${ 'Sometimes' }"`, "", "Unsupported value: \"Sometimes\""},
		{"embedded text in a list field", `args: "${ ['--sha', Sha, '--branch', Branch] + (Branch.endsWith('production') ? ['--strict'] : []) }"`, `args: "--sha ${ Sha }"`, "the field expects a list of string but the value is text with embedded expressions", ""},
		{"mixed-type ternary", `? optional.of(1800) : optional.none() }`, `? 1800 : null }`, "found no matching overload for '_?_:_' applied to '(bool, int, null)'", ""},
		{"duration object into an int", `ttlSecondsAfterFinished: ${ duration('24h').getSeconds() }`, `ttlSecondsAfterFinished: ${ duration('24h') }`, "returns google.protobuf.Duration but the field expects int", ""},
		{"double into an int", `parallelism: ${ PromotionStrategy.spec.environments.map(e, e.branch).indexOf(Branch) + 1 }`, `parallelism: ${ 2.0 }`, "returns double but the field expects int", ""},
		{"misspelled field inside a list item", `imagePullPolicy: "${`, `imagePullPolcy: "${`, "containers[0].imagePullPolcy: Invalid value: \"imagePullPolcy\": unknown field", ""},
		{"Resource referenced in the template", `image: ghcr.io/acme/smoke:${ ShortSha }`, `image: ghcr.io/acme/smoke:${ Resource.metadata.name }`, "undeclared reference to 'Resource'", ""},
		{"unknown function", `environment: ${ Branch.replace('/', '-') }`, `environment: ${ Branch.upper() }`, "undeclared reference to 'upper'", ""},
		{"null in a field the schema requires", "      containers:\n        - name: smoke\n", "      containers: \"${ null }\"\n      initContainers:\n        - name: smoke\n", "containers: Invalid value: \"${ null }\": expression \"null\" returns null, which would remove a required field", ""},
		{"null in a field only validation requires", `restartPolicy: Never`, `restartPolicy: ${ null }`, "", "spec.template.spec.restartPolicy: Required value"},
		{"numeric literal into a string", `image: ghcr.io/acme/smoke:${ ShortSha }`, `image: 42`, "literal returns int but the field expects string", ""},
		{"object literal into a scalar", `suspend: ${ false }`, `suspend: {a: 1}`, "the field expects bool but the value is an object", ""},
		{"whole containers list computed with a bad item", "      containers:\n        - name: smoke\n", "      containers: \"${ [{'name': 'x', 'image': 1}] }\"\n      initContainers:\n        - name: smoke\n", "expected type 'string' but found 'int'", ""},
		{"quantity with a unit the API server rejects", `memory: 256Mi`, `memory: 256Mib`, "", "quantities must match the regular expression"},
		{"int-or-string gets a map", `cpu: "${ Branch.endsWith('production') ? '500m' : '100m' }"`, `cpu: "${ {'a': 1} }"`, "", "quantities must match the regular expression"},
		{"optional in a map value", `cpu: "${ Branch.endsWith('production') ? '500m' : '100m' }"`, `cpu: "${ Branch.endsWith('production') ? optional.of('500m') : optional.none() }"`, "", ""},
		{"label from a list", `environment: ${ Branch.replace('/', '-') }`, `environment: ${ PromotionStrategy.spec.environments }`, "labels[environment]: Invalid value: \"${ PromotionStrategy.spec.environments }\": expression \"PromotionStrategy.spec.environments\" returns list of object, but this value must be text", ""},
		{"map-typed field from a list comprehension", `nodeSelector: "${ Branch.endsWith('production') ? {'topology.kubernetes.io/zone': 'us-east-1a'} : {} }"`, `nodeSelector: "${ PromotionStrategy.spec.environments.map(e, e.branch) }"`, "returns list of string but the field expects a map of string", ""},
	}

	It("rejects each mistake at admission, or shows that the API server catches what gets through", func() {
		var report strings.Builder
		for i, m := range mistakes {
			Expect(complexJobTemplate).To(ContainSubstring(m.old), m.name)
			template := strings.Replace(complexJobTemplate, m.old, m.new, 1)
			gate := newGate(fmt.Sprintf("mistake-%d", i), template, complexPhase)
			err := k8sClient.Create(ctx, gate)
			switch {
			case m.rejected != "":
				Expect(err).To(HaveOccurred(), m.name)
				Expect(err.Error()).To(ContainSubstring("denied the request"), m.name)
				Expect(err.Error()).To(ContainSubstring(m.rejected), m.name)
				fmt.Fprintf(&report, "%2d. %-48s rejected by the webhook\n", i+1, m.name)
			default:
				Expect(err).NotTo(HaveOccurred(), m.name)
				obj, rerr := renderAndCreate(gate, "environment/production", fmt.Sprintf("mistake-%d", i))
				if m.apiServer == "" {
					Expect(rerr).NotTo(HaveOccurred(), m.name)
					created = append(created, obj)
					fmt.Fprintf(&report, "%2d. %-48s accepted by the webhook and by the API server (not a mistake)\n", i+1, m.name)
				} else {
					Expect(rerr).To(MatchError(ContainSubstring(m.apiServer)), m.name)
					fmt.Fprintf(&report, "%2d. %-48s accepted by the webhook, rejected by the API server\n", i+1, m.name)
				}
				Expect(k8sClient.Delete(ctx, gate)).To(Succeed())
			}
		}
		GinkgoWriter.Print("\n===== MISTAKE REPORT =====\n" + report.String() + "===== END =====\n")
	})
})
