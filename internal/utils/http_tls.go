package utils

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
)

// ConfigureDefaultHTTPTransport replaces http.DefaultTransport with a clone that
// applies the process-wide TLS policy (min version / ciphers / curves) and, when
// SSL_CERT_FILE is set, custom RootCAs.
//
// On macOS, Go's default verifier uses the platform trust store and does not honor
// SSL_CERT_FILE for http.DefaultTransport. That breaks MITM proxies unless
// we install an explicit root pool. Git subprocesses use GIT_SSL_CAINFO instead;
// see internal/git.proxyRelatedEnvVars.
//
// No-op when neither SSL_CERT_FILE nor a process TLS policy is configured.
func ConfigureDefaultHTTPTransport() error {
	caPath := os.Getenv("SSL_CERT_FILE")
	if caPath == "" && !HasProcessTLSPolicy() {
		return nil
	}

	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return fmt.Errorf("http.DefaultTransport is %T, expected *http.Transport", http.DefaultTransport)
	}
	cloned := base.Clone()
	if cloned.TLSClientConfig == nil {
		cloned.TLSClientConfig = &tls.Config{} //nolint:gosec // policy applied below; empty means Go defaults
	}
	ApplyProcessTLSConfig(cloned.TLSClientConfig)

	if caPath != "" {
		pem, err := os.ReadFile(caPath)
		if err != nil {
			return fmt.Errorf("read TLS CA file %q: %w", caPath, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return fmt.Errorf("parse TLS CA file %q: no certificates found", caPath)
		}
		cloned.TLSClientConfig.RootCAs = pool
	}

	http.DefaultTransport = cloned
	return nil
}
