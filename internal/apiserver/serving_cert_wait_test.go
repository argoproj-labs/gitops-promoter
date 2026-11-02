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

package apiserver

import (
	"context"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/pflag"
)

var _ = Describe("waitForServingCert", func() {
	var certFile, keyFile string
	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		certFile = filepath.Join(dir, "tls.crt")
		keyFile = filepath.Join(dir, "tls.key")
	})

	It("returns immediately when both files exist", func() {
		Expect(os.WriteFile(certFile, []byte("cert"), 0o600)).To(Succeed())
		Expect(os.WriteFile(keyFile, []byte("key"), 0o600)).To(Succeed())
		Expect(waitForServingCert(context.Background(), certFile, keyFile, time.Hour)).To(Succeed())
	})

	It("waits until both files exist and are non-empty", func() {
		done := make(chan error, 1)
		go func() { done <- waitForServingCert(context.Background(), certFile, keyFile, 20*time.Millisecond) }()

		Expect(os.WriteFile(certFile, []byte("cert"), 0o600)).To(Succeed())
		Expect(os.WriteFile(keyFile, nil, 0o600)).To(Succeed())
		Consistently(done, 200*time.Millisecond).ShouldNot(Receive(), "an empty key file is not ready")

		Expect(os.WriteFile(keyFile, []byte("key"), 0o600)).To(Succeed())
		Eventually(done, 5*time.Second).Should(Receive(BeNil()))
	})

	It("returns the context error when cancelled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(waitForServingCert(ctx, certFile, keyFile, 20*time.Millisecond)).To(MatchError(context.Canceled))
	})
})

var _ = Describe("Options --wait-for-serving-cert", func() {
	It("is off by default and requires the TLS file flags", func() {
		opts := NewOptions()
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		opts.AddFlags(fs)
		Expect(opts.WaitForServingCert).To(BeFalse())
		Expect(fs.Lookup("cert-rotation")).To(BeNil(), "the apiserver no longer rotates its own cert")

		Expect(fs.Parse([]string{"--wait-for-serving-cert"})).To(Succeed())
		Expect(opts.Validate()).To(MatchError(ContainSubstring("requires --tls-cert-file and --tls-private-key-file")))

		Expect(fs.Parse([]string{"--tls-cert-file=/serving-certs/tls.crt", "--tls-private-key-file=/serving-certs/tls.key"})).To(Succeed())
		Expect(opts.Validate()).To(Succeed())
	})
})
