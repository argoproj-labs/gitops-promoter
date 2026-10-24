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

package k8sfault

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"k8s.io/client-go/rest"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func okResponse(req *http.Request) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("ok")),
		Header:     make(http.Header),
		Request:    req,
	}
}

func TestMaybeWrapIsOffUnlessConfigured(t *testing.T) {
	t.Setenv(ProbabilityEnv, "")
	cfg := &rest.Config{}
	probability, enabled := MaybeWrap(cfg)
	if enabled || probability != 0 || cfg.WrapTransport != nil {
		t.Fatalf("unset env wrapped the config: enabled=%v probability=%v", enabled, probability)
	}

	t.Setenv(ProbabilityEnv, "nope")
	probability, enabled = MaybeWrap(cfg)
	if enabled || probability != 0 || cfg.WrapTransport != nil {
		t.Fatalf("invalid env wrapped the config: enabled=%v probability=%v", enabled, probability)
	}

	t.Setenv(ProbabilityEnv, "0.25")
	probability, enabled = MaybeWrap(cfg)
	if !enabled || probability != 0.25 || cfg.WrapTransport == nil {
		t.Fatalf("valid env did not wrap the config: enabled=%v probability=%v", enabled, probability)
	}
}

func TestFaultyTransportOnlyFailsEligibleWrites(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		method  string
		path    string
		forward bool
	}{
		{name: "get", method: http.MethodGet, path: "/api/v1/namespaces/default/configmaps/demo", forward: true},
		{name: "watch", method: http.MethodGet, path: "/apis/promoter.argoproj.io/v1alpha1/pullrequests?watch=1", forward: true},
		{name: "lease write", method: http.MethodPatch, path: "/apis/coordination.k8s.io/v1/namespaces/kube-system/leases/promoter", forward: true},
		{name: "status patch", method: http.MethodPatch, path: "/apis/promoter.argoproj.io/v1alpha1/namespaces/default/pullrequests/demo/status", forward: false},
		{name: "create", method: http.MethodPost, path: "/apis/promoter.argoproj.io/v1alpha1/namespaces/default/pullrequests", forward: false},
		{name: "update", method: http.MethodPut, path: "/apis/promoter.argoproj.io/v1alpha1/namespaces/default/pullrequests/demo", forward: false},
		{name: "delete", method: http.MethodDelete, path: "/apis/promoter.argoproj.io/v1alpha1/namespaces/default/pullrequests/demo", forward: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var forwarded int
			transport := &faultyTransport{
				base: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
					forwarded++
					return okResponse(req), nil
				}),
				probability: 1,
			}
			req, err := http.NewRequestWithContext(t.Context(), tc.method, "https://apiserver"+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := transport.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := resp.Body.Close(); err != nil {
					t.Error(err)
				}
			})

			if tc.forward {
				if forwarded != 1 {
					t.Fatal("expected the request to reach the API server")
				}
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("status = %d, want 200", resp.StatusCode)
				}
				return
			}
			if forwarded != 0 {
				t.Fatal("faulted request was forwarded to the API server")
			}
			if resp.StatusCode != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", resp.StatusCode)
			}
		})
	}
}
