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

package cli_test

import (
	"context"
	"strings"
	"testing"

	"github.com/argoproj-labs/gitops-promoter/internal/webrequest/cli"
	"github.com/argoproj-labs/gitops-promoter/webrequestsimulator"
)

const minimalBundle = `apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: ps
  namespace: default
spec:
  repositoryReference:
    name: repo
  environments:
    - branch: dev
      proposedCommitStatuses:
        - key: k
    - branch: prod
      proposedCommitStatuses:
        - key: k
status:
  environments:
    - branch: dev
      proposed:
        hydrated:
          sha: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
    - branch: prod
      proposed:
        hydrated:
          sha: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: wrcs
  namespace: default
spec:
  promotionStrategyRef:
    name: ps
  key: k
  reportOn: proposed
  httpRequest:
    urlTemplate: "https://example.com/{{ .Branch }}"
    method: GET
  success:
    when:
      expression: "Response.StatusCode == 200"
  mode:
    polling:
      interval: 0s
---
apiVersion: simulate.gitops-promoter.io/v1alpha1
kind: SimulatorConfig
namespaceMetadata:
  labels:
    team: payments
  annotations:
    cost-center: cc-42
httpResponses:
  - branch: dev
    response:
      statusCode: 200
  - branch: prod
    response:
      statusCode: 200
      headers:
        X-Test:
          - a
          - b
`

func TestLoadBundle_UnorderedDocs(t *testing.T) {
	t.Parallel()
	in, err := cli.LoadBundle([]byte(minimalBundle))
	if err != nil {
		t.Fatal(err)
	}
	if in.WebRequestCommitStatus == nil || in.WebRequestCommitStatus.Name != "wrcs" {
		t.Fatalf("wrcs: %+v", in.WebRequestCommitStatus)
	}
	if in.PromotionStrategy == nil || in.PromotionStrategy.Name != "ps" {
		t.Fatalf("ps: %+v", in.PromotionStrategy)
	}
	if got := in.NamespaceMetadata.Labels["team"]; got != "payments" {
		t.Fatalf("labels.team = %q", got)
	}
	if got := in.NamespaceMetadata.Annotations["cost-center"]; got != "cc-42" {
		t.Fatalf("annotations = %q", got)
	}
	if len(in.HTTPResponses) != 2 {
		t.Fatalf("httpResponses len %d", len(in.HTTPResponses))
	}
	if in.HTTPResponses[1].Response.Headers["X-Test"][0] != "a" {
		t.Fatalf("headers: %+v", in.HTTPResponses[1].Response.Headers)
	}
}

func TestLoadBundle_MissingPromotionStrategy(t *testing.T) {
	t.Parallel()
	yaml := `apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: w
spec:
  promotionStrategyRef:
    name: p
  key: k
  reportOn: proposed
  httpRequest:
    urlTemplate: "http://x"
    method: GET
  success:
    when:
      expression: "true"
  mode:
    polling:
      interval: 0s
`
	_, err := cli.LoadBundle([]byte(yaml))
	if err == nil || !strings.Contains(err.Error(), "PromotionStrategy") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadBundle_DuplicateWebRequestCommitStatus(t *testing.T) {
	t.Parallel()
	dup := `apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: a
spec:
  promotionStrategyRef:
    name: p
  key: k
  reportOn: proposed
  httpRequest:
    urlTemplate: "http://x"
    method: GET
  success:
    when:
      expression: "true"
  mode:
    polling:
      interval: 0s
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: WebRequestCommitStatus
metadata:
  name: b
spec:
  promotionStrategyRef:
    name: p
  key: k
  reportOn: proposed
  httpRequest:
    urlTemplate: "http://x"
    method: GET
  success:
    when:
      expression: "true"
  mode:
    polling:
      interval: 0s
`
	_, err := cli.LoadBundle([]byte(dup))
	if err == nil || !strings.Contains(err.Error(), "duplicate WebRequestCommitStatus") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadBundle_UnsupportedKind(t *testing.T) {
	t.Parallel()
	yaml := `apiVersion: v1
kind: ConfigMap
metadata:
  name: x
`
	_, err := cli.LoadBundle([]byte(yaml))
	if err == nil || !strings.Contains(err.Error(), "unsupported kind") {
		t.Fatalf("err = %v", err)
	}
}

func TestSimulateFromBundle_Minimal(t *testing.T) {
	t.Parallel()
	in, err := cli.LoadBundle([]byte(minimalBundle))
	if err != nil {
		t.Fatal(err)
	}
	res, err := webrequestsimulator.Simulate(context.Background(), *in)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Status.Environments) != 2 {
		t.Fatalf("environments: %d", len(res.Status.Environments))
	}
	if len(res.RenderedRequests) != 2 {
		t.Fatalf("rendered: %d", len(res.RenderedRequests))
	}
}

func TestEncodeResultJSON(t *testing.T) {
	t.Parallel()
	in, err := cli.LoadBundle([]byte(minimalBundle))
	if err != nil {
		t.Fatal(err)
	}
	res, err := webrequestsimulator.Simulate(context.Background(), *in)
	if err != nil {
		t.Fatal(err)
	}
	b, err := cli.EncodeResultJSON(res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"Status"`) {
		t.Fatalf("json: %s", string(b[:min(200, len(b))]))
	}
}

func TestEncodeResultYAML(t *testing.T) {
	t.Parallel()
	in, err := cli.LoadBundle([]byte(minimalBundle))
	if err != nil {
		t.Fatal(err)
	}
	res, err := webrequestsimulator.Simulate(context.Background(), *in)
	if err != nil {
		t.Fatal(err)
	}
	b, err := cli.EncodeResultYAML(res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "Status:") {
		t.Fatalf("yaml: %s", string(b[:min(200, len(b))]))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
