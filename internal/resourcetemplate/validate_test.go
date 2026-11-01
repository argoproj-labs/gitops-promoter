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

package resourcetemplate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/client-go/openapi"
	"sigs.k8s.io/yaml"
)

// fakeOpenAPIGroupVersion serves one GroupVersion document.
type fakeOpenAPIGroupVersion struct {
	doc   []byte
	calls *int
}

func (f fakeOpenAPIGroupVersion) Schema(string) ([]byte, error) {
	*f.calls++
	return f.doc, nil
}

func (f fakeOpenAPIGroupVersion) ServerRelativeURL() string { return "" }

// fakeOpenAPIClient serves testdata documents keyed by API path (apis/<group>/<version>).
type fakeOpenAPIClient struct {
	docs  map[string][]byte
	calls int
}

func (f *fakeOpenAPIClient) Paths() (map[string]openapi.GroupVersion, error) {
	out := map[string]openapi.GroupVersion{}
	for path, doc := range f.docs {
		out[path] = fakeOpenAPIGroupVersion{doc: doc, calls: &f.calls}
	}
	return out, nil
}

func loadOpenAPIFixture(name string) []byte {
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	Expect(err).NotTo(HaveOccurred())
	doc, err := yaml.YAMLToJSON(raw)
	Expect(err).NotTo(HaveOccurred())
	return doc
}

var _ = Describe("Validator", func() {
	var (
		client    *fakeOpenAPIClient
		validator *Validator
	)

	BeforeEach(func() {
		client = &fakeOpenAPIClient{docs: map[string][]byte{
			"apis/batch/v1":      loadOpenAPIFixture("openapi-batch-v1.yaml"),
			"apis/example.io/v1": loadOpenAPIFixture("openapi-example-v1.yaml"),
		}}
		validator = NewValidator(NewSchemaSource(client, time.Hour))
	})

	parse := func(yamlTemplate string) (map[string]any, schema.GroupVersionKind) {
		raw, err := yaml.YAMLToJSON([]byte(yamlTemplate))
		Expect(err).NotTo(HaveOccurred())
		template, gvk, err := ParseTemplate(apiextensionsv1.JSON{Raw: raw})
		Expect(err).NotTo(HaveOccurred())
		return template, gvk
	}

	validate := func(yamlTemplate string) field.ErrorList {
		template, gvk := parse(yamlTemplate)
		errs, err := validator.Validate(context.Background(), template, gvk)
		Expect(err).NotTo(HaveOccurred())
		return errs
	}

	messages := func(errs field.ErrorList) []string {
		out := make([]string, 0, len(errs))
		for _, e := range errs {
			out = append(out, e.Error())
		}
		return out
	}

	const validJob = `
apiVersion: batch/v1
kind: Job
metadata:
  name: ignored
  labels: {team: a, sha: "${ ShortSha }"}
spec:
  backoffLimit: 0
  completionMode: NonIndexed
  template:
    spec:
      restartPolicy: Never
      nodeSelector: {disk: ssd}
      containers:
        - name: test
          image: ghcr.io/acme/smoke:latest
          args: ["--sha", "${ Sha }"]
          ports: [{containerPort: 8080, protocol: TCP}]
          resources: {limits: {cpu: 1, memory: 512Mi}}
          readinessProbe: {httpGet: {path: /healthz, port: http}}
status:
  anything: goes
`

	It("accepts a valid template", func() {
		Expect(validate(validJob)).To(BeEmpty())
	})

	It("reports unknown fields at every depth with a suggestion", func() {
		errs := validate(`
apiVersion: batch/v1
kind: Job
metadata:
  labls: {team: a}
spec:
  backofLimit: 0
  template:
    spec:
      containers:
        - name: test
          imag: x
        - name: other
          image: busybox
          ports: [{containerPort: 80, portocol: TCP}]
`)
		Expect(messages(errs)).To(ConsistOf(
			`spec.template.metadata.labls: Invalid value: unknown field: the schema does not declare it (did you mean "labels"?)`,
			`spec.template.spec.backofLimit: Invalid value: unknown field: the schema does not declare it (did you mean "backoffLimit"?)`,
			`spec.template.spec.template.spec.containers[0].imag: Invalid value: unknown field: the schema does not declare it (did you mean "image"?)`,
			`spec.template.spec.template.spec.containers[1].ports[0].portocol: Invalid value: unknown field: the schema does not declare it (did you mean "protocol"?)`,
		))
	})

	It("checks the name of an expression-valued field but not what it renders", func() {
		errs := validate(`
apiVersion: batch/v1
kind: Job
spec:
  parallelsm: "${ 1 + 1 }"
  template: "${ {spec: {containers: [{name: 'x', imag: 'y'}]}} }"
`)
		Expect(messages(errs)).To(ConsistOf(ContainSubstring("spec.template.spec.parallelsm: Invalid value: unknown field")))
	})

	It("does not descend into free-form maps, int-or-string, or preserve-unknown-fields nodes", func() {
		Expect(validate(`
apiVersion: example.io/v1
kind: Scaler
spec:
  target: web
  extra: {anything: {nested: [1, 2]}}
`)).To(BeEmpty())
	})

	It("reports literal values that violate the schema", func() {
		errs := validate(`
apiVersion: batch/v1
kind: Job
spec:
  backoffLimit: abc
  suspend: "yes"
  template:
    spec:
      restartPolicy: Nevr
      containers:
        - name: test
          ports: [{containerPort: "80"}]
          resources: {limits: {cpu: [1]}}
`)
		Expect(messages(errs)).To(ConsistOf(
			ContainSubstring(`spec.template.spec.backoffLimit: Invalid value: "string": spec.backoffLimit in body must be of type integer`),
			ContainSubstring(`spec.template.spec.suspend: Invalid value: "string": spec.suspend in body must be of type boolean`),
			ContainSubstring(`spec.template.spec.template.spec.restartPolicy: Unsupported value: "Nevr"`),
			ContainSubstring(`spec.template.spec.template.spec.containers[0].ports[0].containerPort: Invalid value: "string"`),
			ContainSubstring(`spec.template.spec.template.spec.containers[0].resources.limits.cpu`),
		))
	})

	It("does not check expression values, and does not report missing required fields", func() {
		Expect(validate(`
apiVersion: batch/v1
kind: Job
spec:
  backoffLimit: "${ Branch == 'environment/production' ? 0 : 2 }"
  completionMode: "mode-${ ShortSha }"
  template:
    spec:
      containers:
        - image: x
          args: "${ ['--sha', Sha] }"
`)).To(BeEmpty())
	})

	It("treats $${ as a literal value", func() {
		errs := validate(`
apiVersion: batch/v1
kind: Job
spec:
  completionMode: "$${ not an expression }"
`)
		Expect(messages(errs)).To(ConsistOf(ContainSubstring(`Unsupported value: "${ not an expression }"`)))
	})

	It("checks literal values against CRD constraints", func() {
		errs := validate(`
apiVersion: example.io/v1
kind: Scaler
spec:
  target: Web-1
  mode: Medium
  replicas: {min: -1, max: 11}
  params: [{name: a}, {name: b}, {name: c}]
`)
		Expect(messages(errs)).To(ConsistOf(
			ContainSubstring(`spec.template.spec.target: Invalid value: "Web-1": spec.target in body should match '^[a-z]+$'`),
			ContainSubstring(`spec.template.spec.mode: Unsupported value: "Medium"`),
			ContainSubstring(`spec.template.spec.replicas.min: Invalid value: -1: spec.replicas.min in body should be greater than or equal to 0`),
			ContainSubstring(`spec.template.spec.replicas.max: Invalid value: 11: spec.replicas.max in body should be less than or equal to 10`),
			ContainSubstring(`spec.template.spec.params: Too many: 3: must have at most 2 items`),
		))
	})

	It("evaluates CEL rules over literal values, with schema defaults applied", func() {
		errs := validate(`
apiVersion: example.io/v1
kind: Scaler
spec:
  target: web
  replicas: {min: 5, max: 2}
  window: {seconds: 90}
`)
		Expect(messages(errs)).To(ConsistOf(
			`spec.template.spec.replicas: Invalid value: min must not exceed max`,
			`spec.template.spec.window: Invalid value: seconds must be whole minutes`,
		))

		// window.seconds defaults to 120, a whole number of minutes.
		Expect(validate(`
apiVersion: example.io/v1
kind: Scaler
spec:
  target: web
  window: {}
`)).To(BeEmpty())
	})

	It("skips CEL rules that could read an expression value, but not rules on sibling literal subtrees", func() {
		errs := validate(`
apiVersion: example.io/v1
kind: Scaler
spec:
  target: web
  replicas: {min: 5, max: "${ 2 }"}
  window: {seconds: 90}
`)
		Expect(messages(errs)).To(ConsistOf(`spec.template.spec.window: Invalid value: seconds must be whole minutes`))
	})

	It("never evaluates root rules, which can read metadata the controller sets", func() {
		Expect(validate(`
apiVersion: example.io/v1
kind: Scaler
metadata: {name: not-prefixed}
spec: {target: web}
`)).To(BeEmpty())
	})

	It("removes a list holding an expression element from value validation", func() {
		Expect(validate(`
apiVersion: example.io/v1
kind: Scaler
spec:
  target: web
  params: [{name: a}, {name: b}, "${ {name: 'c'} }"]
`)).To(BeEmpty())
	})

	It("returns an error when the API server publishes no schema for the kind", func() {
		template, _ := parse(validJob)
		_, err := validator.Validate(context.Background(), template, schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "CronJob"})
		Expect(err).To(MatchError(ContainSubstring("publishes no OpenAPI schema for batch/v1, Kind=CronJob")))
		_, err = validator.Validate(context.Background(), template, schema.GroupVersionKind{Group: "missing.io", Version: "v1", Kind: "Thing"})
		Expect(err).To(HaveOccurred())
	})

	It("caches schemas and re-fetches once when a cached schema reports unknown fields", func() {
		Expect(validate(validJob)).To(BeEmpty())
		Expect(validate(validJob)).To(BeEmpty())
		Expect(client.calls).To(Equal(1))

		// The cached schema is younger than refreshMinAge, so unknown fields are reported from it.
		Expect(validate(strings.Replace(validJob, "backoffLimit", "backofLimit", 1))).To(HaveLen(1))
		Expect(client.calls).To(Equal(1))

		source := validator.schemas
		source.now = func() time.Time { return time.Now().Add(time.Minute) }
		Expect(validate(strings.Replace(validJob, "backoffLimit", "backofLimit", 1))).To(HaveLen(1))
		Expect(client.calls).To(Equal(2))
	})
})
