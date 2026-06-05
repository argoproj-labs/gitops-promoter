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

// Command notification-echo is a tiny standalone webhook receiver for demoing and eyeballing
// the GitOps Promoter notification framework. It logs every incoming request (method, headers,
// and body) and can be told to fail on purpose so you can watch retry and dead-letter behavior
// live.
//
// It is intentionally self-contained: it imports only the Go standard library, so you can copy
// this single file anywhere and run it without the rest of the repo.
//
// # Usage
//
//	go run ./hack/notification-echo --addr :8080
//
// Failure modes (to exercise the dispatcher's retry/backoff and dead-letter paths):
//
//	# Always succeed (default).
//	go run ./hack/notification-echo
//
//	# Return 500 for the first 2 requests, then 200 — drives "retry then success".
//	go run ./hack/notification-echo --fail-first 2
//
//	# Always return 500 — drives "dead-letter after max attempts".
//	go run ./hack/notification-echo --always-fail
//
// HMAC signature verification (proves end-to-end signing works):
//
//	# Verify the X-Promoter-Signature header against the same HMAC-SHA256 scheme the
//	# dispatcher uses: header value is "sha256=<lowercase-hex>" of HMAC-SHA256(secret, body).
//	go run ./hack/notification-echo --hmac-secret "my-shared-secret"
//
// All flags may also be set via env vars: ECHO_ADDR, ECHO_FAIL_FIRST, ECHO_ALWAYS_FAIL,
// ECHO_HMAC_SECRET (flags take precedence).
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"slices"
	"strconv"
	"sync/atomic"
	"time"
)

// signatureHeader and signaturePrefix mirror internal/notification/payload so the demo verifies
// exactly what the dispatcher signs. Kept as literals so this file stays dependency-free.
const (
	signatureHeader = "X-Promoter-Signature"
	signaturePrefix = "sha256="
)

type config struct {
	addr       string
	hmacSecret string
	failFirst  int
	alwaysFail bool
}

func loadConfig() config {
	cfg := config{
		addr:       envOr("ECHO_ADDR", ":8080"),
		failFirst:  envOrInt("ECHO_FAIL_FIRST", 0),
		alwaysFail: envOrBool("ECHO_ALWAYS_FAIL", false),
		hmacSecret: os.Getenv("ECHO_HMAC_SECRET"),
	}

	flag.StringVar(&cfg.addr, "addr", cfg.addr, "address to listen on, e.g. :8080")
	flag.IntVar(&cfg.failFirst, "fail-first", cfg.failFirst,
		"return HTTP 500 for the first N requests, then 200 (retry-then-success drill)")
	flag.BoolVar(&cfg.alwaysFail, "always-fail", cfg.alwaysFail,
		"always return HTTP 500 (dead-letter drill); overrides --fail-first")
	flag.StringVar(&cfg.hmacSecret, "hmac-secret", cfg.hmacSecret,
		"if set, verify the X-Promoter-Signature header (HMAC-SHA256, sha256=<hex>) against this secret")
	flag.Parse()
	return cfg
}

func main() {
	cfg := loadConfig()

	// requestCount is the number of requests received so far (1-based once incremented).
	var requestCount atomic.Int64

	handler := func(w http.ResponseWriter, r *http.Request) {
		n := requestCount.Add(1)

		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("[req #%d] error reading body: %v", n, err)
			http.Error(w, "cannot read body", http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()

		logRequest(n, r, body)

		// Signature verification (optional).
		if cfg.hmacSecret != "" {
			got := r.Header.Get(signatureHeader)
			if got == "" {
				log.Printf("[req #%d] SIGNATURE: MISSING (%s header not present)", n, signatureHeader)
			} else if verifySignature([]byte(cfg.hmacSecret), body, got) {
				log.Printf("[req #%d] SIGNATURE: OK (%s verified)", n, signatureHeader)
			} else {
				log.Printf("[req #%d] SIGNATURE: INVALID (expected %s, got %q)",
					n, computeSignature([]byte(cfg.hmacSecret), body), got)
			}
		}

		// Decide the response based on the configured failure mode.
		switch {
		case cfg.alwaysFail:
			log.Printf("[req #%d] -> 500 (always-fail mode)", n)
			http.Error(w, "always-fail mode", http.StatusInternalServerError)
		case int(n) <= cfg.failFirst:
			log.Printf("[req #%d] -> 500 (fail-first: %d of first %d)", n, n, cfg.failFirst)
			http.Error(w, "fail-first mode", http.StatusInternalServerError)
		default:
			log.Printf("[req #%d] -> 200 OK", n)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "ok\n")
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handler)

	srv := &http.Server{
		Addr:              cfg.addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("notification-echo listening on %s", cfg.addr)
	switch {
	case cfg.alwaysFail:
		log.Print("mode: ALWAYS-FAIL (every request -> 500; drives dead-letter)")
	case cfg.failFirst > 0:
		log.Printf("mode: FAIL-FIRST=%d (first %d requests -> 500, then 200; drives retry-then-success)",
			cfg.failFirst, cfg.failFirst)
	default:
		log.Print("mode: SUCCEED (every request -> 200)")
	}
	if cfg.hmacSecret != "" {
		log.Printf("HMAC verification: ENABLED (header %s, scheme sha256=<hex>)", signatureHeader)
	}

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

// logRequest prints the request line, sorted headers, and body.
func logRequest(n int64, r *http.Request, body []byte) {
	log.Printf("[req #%d] %s %s", n, r.Method, r.URL.Path)

	keys := make([]string, 0, len(r.Header))
	for k := range r.Header {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		for _, v := range r.Header[k] {
			log.Printf("[req #%d]   %s: %s", n, k, v)
		}
	}
	log.Printf("[req #%d] body: %s", n, string(body))
}

// computeSignature returns "sha256=<lowercase-hex>" of HMAC-SHA256(secret, body), matching
// internal/notification/payload.ComputeSignature.
func computeSignature(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

// verifySignature reports whether signatureHeaderValue is a valid signature for body, using a
// constant-time comparison. Mirrors internal/notification/payload.VerifySignature.
func verifySignature(secret, body []byte, signatureHeaderValue string) bool {
	expected := computeSignature(secret, body)
	return hmac.Equal([]byte(expected), []byte(signatureHeaderValue))
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envOrInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		fmt.Fprintf(os.Stderr, "warning: %s=%q is not an int, using default %d\n", key, v, def)
	}
	return def
}

func envOrBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
		fmt.Fprintf(os.Stderr, "warning: %s=%q is not a bool, using default %v\n", key, v, def)
	}
	return def
}
