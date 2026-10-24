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

// Package k8sfault fails a fraction of the controller's writes to the Kubernetes API server.
//
// envtest has no fault-injection feature. The integration suite builds a real API server and a real
// client-go transport, so the only hook is rest.Config.Wrap. internal/controller/suite_test.go calls
// MaybeWrap on the manager's config after the suite's own client is already built, so specs keep a
// reliable client for fixtures and assertions while the reconcilers' status, spec, and finalizer
// writes can fail and retry.
//
// MaybeWrap is a no-op unless ProbabilityEnv is set to a probability greater than 0. CI sets
// K8S_FAULT_PROBABILITY to 0.02 for the integration test job.
package k8sfault

import (
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"strings"

	"k8s.io/client-go/rest"
)

// ProbabilityEnv is the environment variable that enables fault injection. Set it to a fraction in
// (0, 1] to fail that fraction of PATCH, POST, PUT, and DELETE requests with a synthetic 500.
const ProbabilityEnv = "K8S_FAULT_PROBABILITY"

// MaybeWrap wraps cfg's transport so a fraction of write requests fail before they reach the API
// server. A faulted request has no partial side effect. GET and WATCH are never faulted: informer
// reads are served from the cache, and a faulted watch would drop every controller's cache. Writes
// to coordination.k8s.io are exempt so leader election stays reliable.
//
// Returns the parsed probability and true when injection is enabled. Returns 0 and false, and leaves
// cfg untouched, when the variable is unset, empty, zero, or not a float.
func MaybeWrap(cfg *rest.Config) (probability float64, enabled bool) {
	raw := os.Getenv(ProbabilityEnv)
	if raw == "" {
		return 0, false
	}
	probability, err := strconv.ParseFloat(raw, 64)
	if err != nil || probability <= 0 {
		return 0, false
	}
	cfg.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return &faultyTransport{base: rt, probability: probability}
	})
	return probability, true
}

// faultyTransport fails a fraction of write requests with a synthetic 500 and does not forward those
// requests. Reads and coordination.k8s.io writes always pass through.
type faultyTransport struct {
	base        http.RoundTripper
	probability float64
}

func (t *faultyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.shouldFault(req) {
		return t.base.RoundTrip(req) //nolint:wrapcheck // the base transport's error is the result
	}
	return &http.Response{
		Status:     "500 Internal Server Error",
		StatusCode: http.StatusInternalServerError,
		Proto:      req.Proto,
		ProtoMajor: req.ProtoMajor,
		ProtoMinor: req.ProtoMinor,
		Header:     http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}},
		Body:       io.NopCloser(strings.NewReader("injected fault (" + ProbabilityEnv + ")")),
		Request:    req,
	}, nil
}

// shouldFault reports whether req is an eligible write that this sample faults.
func (t *faultyTransport) shouldFault(req *http.Request) bool {
	switch req.Method {
	case http.MethodPatch, http.MethodPost, http.MethodPut, http.MethodDelete:
	default:
		return false
	}
	if strings.Contains(req.URL.Path, "/coordination.k8s.io/") {
		return false
	}
	return rand.Float64() < t.probability //nolint:gosec // test-only sampling, not a security boundary
}
