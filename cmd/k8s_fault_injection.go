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

package main

import (
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"strings"

	"k8s.io/client-go/rest"
)

// e2eK8sFaultProbabilityEnv names the environment variable that enables fault injection against the
// Kubernetes API server client this controller builds from ctrl.GetConfigOrDie(). It is read once at
// startup. No real deployment sets it; the e2e suite is the only caller, patching it into the manager
// Deployment the same way it does E2E_COVERAGE (see test/e2e/e2e_test.go). CI does not set it yet,
// pending a probability sweep like the one that picked 0.02 for E2E_SCM_FAULT_PROBABILITY.
const e2eK8sFaultProbabilityEnv = "E2E_K8S_FAULT_PROBABILITY"

// maybeInjectKubernetesAPIFaults wraps cfg's transport with a RoundTripper that fails a fraction of
// write requests (PATCH, POST, PUT, DELETE) to the Kubernetes API server with a synthetic 500, when
// E2E_K8S_FAULT_PROBABILITY is set to a value greater than 0. The write never reaches the API server:
// the synthetic response is returned directly, so a faulted request has no partial side effect to
// reason about.
//
// This exercises the same reconcile-error-then-requeue path the rate limiter already drives for SCM
// and git failures (see test/e2e/scm_providers_test.go's configureRequeue), but for the status,
// finalizer and spec writes every controller makes against the real API server instead.
//
// GET and WATCH requests are never faulted: informer-backed reads never reach this transport anyway
// (they're served from the manager's cache), and faulting watches would flap every controller's cache
// and leader election's own watches along with it. Writes to coordination.k8s.io (leases) are exempted
// outright so leader election stays reliable; making that path resilient to API faults is out of scope
// here.
//
// No-op, and cfg is left untouched, when the env var is unset, empty, zero, or not a valid float.
func maybeInjectKubernetesAPIFaults(cfg *rest.Config) {
	raw := os.Getenv(e2eK8sFaultProbabilityEnv)
	if raw == "" {
		return
	}
	probability, err := strconv.ParseFloat(raw, 64)
	if err != nil || probability <= 0 {
		return
	}
	setupLog.Info("fault injection enabled for the Kubernetes API client", e2eK8sFaultProbabilityEnv, probability)
	cfg.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return &faultyKubernetesTransport{base: rt, probability: probability}
	})
}

// faultyKubernetesTransport is an http.RoundTripper that fails a fraction of write requests against
// the Kubernetes API server with a synthetic 500, without forwarding them to the base transport. Reads
// and lease writes (coordination.k8s.io, used by leader election) always pass through unchanged.
type faultyKubernetesTransport struct {
	base        http.RoundTripper
	probability float64
}

func (t *faultyKubernetesTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.shouldFault(req) {
		return t.base.RoundTrip(req) //nolint:wrapcheck // pass-through to the real transport's error, nothing to add
	}
	return &http.Response{
		Status:     "500 Internal Server Error",
		StatusCode: http.StatusInternalServerError,
		Proto:      req.Proto,
		ProtoMajor: req.ProtoMajor,
		ProtoMinor: req.ProtoMinor,
		Header:     http.Header{"Content-Type": []string{"text/plain; charset=utf-8"}},
		Body:       io.NopCloser(strings.NewReader("injected fault (" + e2eK8sFaultProbabilityEnv + ")")),
		Request:    req,
	}, nil
}

// shouldFault reports whether req should be failed. Only write methods are eligible; GET and WATCH
// requests, and any write to a coordination.k8s.io lease, always return false.
func (t *faultyKubernetesTransport) shouldFault(req *http.Request) bool {
	switch req.Method {
	case http.MethodPatch, http.MethodPost, http.MethodPut, http.MethodDelete:
	default:
		return false
	}
	if strings.Contains(req.URL.Path, "/coordination.k8s.io/") {
		return false
	}
	return rand.Float64() < t.probability //nolint:gosec // test-only fault sampling, not a security context
}
