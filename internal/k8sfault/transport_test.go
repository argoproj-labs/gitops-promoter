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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

type closeTrackingBody struct {
	io.Reader
	closed bool
}

func (b *closeTrackingBody) Close() error {
	b.closed = true
	return nil
}

var _ = Describe("MaybeWrap", func() {
	It("is off unless configured", func() {
		GinkgoT().Setenv(ProbabilityEnv, "")
		cfg := &rest.Config{}
		probability, enabled := MaybeWrap(cfg)
		Expect(enabled).To(BeFalse(), "unset env enabled fault injection")
		Expect(probability).To(BeZero())
		Expect(cfg.WrapTransport).To(BeNil(), "unset env wrapped the config")

		GinkgoT().Setenv(ProbabilityEnv, "nope")
		probability, enabled = MaybeWrap(cfg)
		Expect(enabled).To(BeFalse(), "invalid env enabled fault injection")
		Expect(probability).To(BeZero())
		Expect(cfg.WrapTransport).To(BeNil(), "invalid env wrapped the config")

		GinkgoT().Setenv(ProbabilityEnv, "0.25")
		probability, enabled = MaybeWrap(cfg)
		Expect(enabled).To(BeTrue(), "valid env did not enable fault injection")
		Expect(probability).To(Equal(0.25))
		Expect(cfg.WrapTransport).NotTo(BeNil(), "valid env did not wrap the config")
	})

	DescribeTable("rejects probabilities outside (0, 1]",
		func(raw string) {
			GinkgoT().Setenv(ProbabilityEnv, raw)
			cfg := &rest.Config{}
			probability, enabled := MaybeWrap(cfg)
			Expect(enabled).To(BeFalse(), "out-of-range env enabled fault injection")
			Expect(probability).To(BeZero())
			Expect(cfg.WrapTransport).To(BeNil(), "out-of-range env wrapped the config")
		},
		Entry("zero", "0"),
		Entry("negative", "-0.1"),
		Entry("above one", "1.5"),
		Entry("NaN", "NaN"),
		Entry("Inf", "Inf"),
	)

	It("accepts exactly 1", func() {
		GinkgoT().Setenv(ProbabilityEnv, "1")
		probability, enabled := MaybeWrap(&rest.Config{})
		Expect(enabled).To(BeTrue())
		Expect(probability).To(Equal(1.0))
	})
})

var _ = Describe("faultyTransport", func() {
	DescribeTable("only fails eligible writes",
		func(method, path string, forward bool) {
			var forwarded int
			transport := &faultyTransport{
				base: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
					forwarded++
					return okResponse(req), nil
				}),
				probability: 1,
			}
			req, err := http.NewRequestWithContext(GinkgoT().Context(), method, "https://apiserver"+path, nil)
			Expect(err).NotTo(HaveOccurred())
			resp, err := transport.RoundTrip(req)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				Expect(resp.Body.Close()).To(Succeed())
			})

			if forward {
				Expect(forwarded).To(Equal(1), "expected the request to reach the API server")
				Expect(resp.StatusCode).To(Equal(http.StatusOK))
				return
			}
			Expect(forwarded).To(BeZero(), "faulted request was forwarded to the API server")
			Expect(resp.StatusCode).To(Equal(http.StatusInternalServerError))
		},
		Entry("get", http.MethodGet, "/api/v1/namespaces/default/configmaps/demo", true),
		Entry("watch", http.MethodGet, "/apis/promoter.argoproj.io/v1alpha1/pullrequests?watch=1", true),
		Entry("lease write", http.MethodPatch, "/apis/coordination.k8s.io/v1/namespaces/kube-system/leases/promoter", true),
		Entry("status patch", http.MethodPatch, "/apis/promoter.argoproj.io/v1alpha1/namespaces/default/pullrequests/demo/status", false),
		Entry("create", http.MethodPost, "/apis/promoter.argoproj.io/v1alpha1/namespaces/default/pullrequests", false),
		Entry("update", http.MethodPut, "/apis/promoter.argoproj.io/v1alpha1/namespaces/default/pullrequests/demo", false),
		Entry("delete", http.MethodDelete, "/apis/promoter.argoproj.io/v1alpha1/namespaces/default/pullrequests/demo", false),
	)

	It("closes the request body of a faulted request", func() {
		transport := &faultyTransport{
			base: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				Fail("faulted request was forwarded to the API server")
				return nil, nil
			}),
			probability: 1,
		}
		body := &closeTrackingBody{Reader: strings.NewReader(`{"metadata":{}}`)}
		req, err := http.NewRequestWithContext(GinkgoT().Context(), http.MethodPost,
			"https://apiserver/apis/promoter.argoproj.io/v1alpha1/namespaces/default/pullrequests", body)
		Expect(err).NotTo(HaveOccurred())
		resp, err := transport.RoundTrip(req)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			Expect(resp.Body.Close()).To(Succeed())
		})
		Expect(resp.StatusCode).To(Equal(http.StatusInternalServerError))
		Expect(body.closed).To(BeTrue(), "faulted request body was not closed")
	})
})
