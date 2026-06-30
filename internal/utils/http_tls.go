package utils

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
)

// ConfigureDefaultTransportFromEnv replaces http.DefaultTransport with a clone whose
// RootCAs are loaded from SSL_CERT_FILE when set.
//
// On macOS, Go's default verifier uses the platform trust store and does not honor
// SSL_CERT_FILE for http.DefaultTransport. That breaks MITM proxies unless
// we install an explicit root pool. Git subprocesses use GIT_SSL_CAINFO instead;
// see internal/git.proxyRelatedEnvVars.
func ConfigureDefaultTransportFromEnv() error {
	caPath := os.Getenv("SSL_CERT_FILE")
	if caPath == "" {
		return nil
	}

	pem, err := os.ReadFile(caPath)
	if err != nil {
		return fmt.Errorf("read TLS CA file %q: %w", caPath, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return fmt.Errorf("parse TLS CA file %q: no certificates found", caPath)
	}

	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return fmt.Errorf("http.DefaultTransport is %T, expected *http.Transport", http.DefaultTransport)
	}
	cloned := base.Clone()
	if cloned.TLSClientConfig == nil {
		cloned.TLSClientConfig = &tls.Config{}
	}
	cloned.TLSClientConfig.RootCAs = pool
	http.DefaultTransport = cloned
	return nil
}
