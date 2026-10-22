package utils

import (
	"crypto/tls"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// tlsVersionMap maps version strings to tls version constants.
// TLS 1.0 is not supported as it is considered insecure.
var tlsVersionMap = map[string]uint16{
	"1.1":    tls.VersionTLS11,
	"tls1.1": tls.VersionTLS11,
	"1.2":    tls.VersionTLS12,
	"tls1.2": tls.VersionTLS12,
	"1.3":    tls.VersionTLS13,
	"tls1.3": tls.VersionTLS13,
}

// tlsCurveMap maps curve preference names to tls.CurveID values.
var tlsCurveMap = map[string]tls.CurveID{
	"curvep256": tls.CurveP256,
	"p256":      tls.CurveP256,
	"p-256":     tls.CurveP256,
	"curvep384": tls.CurveP384,
	"p384":      tls.CurveP384,
	"p-384":     tls.CurveP384,
	"curvep521": tls.CurveP521,
	"p521":      tls.CurveP521,
	"p-521":     tls.CurveP521,
	"x25519":    tls.X25519,
}

// TLSSettings holds string-form TLS policy knobs (typically from CLI flags).
// Empty fields mean "use Go defaults" — nothing is hardcoded by this package.
type TLSSettings struct {
	MinVersion       string
	Ciphers          string
	CurvePreferences string
}

// processTLS holds the parsed process-wide TLS policy set at startup.
// Empty/zero means leave that field to Go defaults.
type processTLSPolicy struct {
	minVersion       uint16
	cipherSuites     []uint16
	curvePreferences []tls.CurveID
}

var (
	processTLSMu sync.RWMutex
	processTLS   processTLSPolicy
)

// SetProcessTLSSettings parses and installs the process-wide TLS policy used by
// metrics/webhook servers, http.DefaultTransport, and outbound mTLS clients.
// Empty settings clear any previously installed policy (Go defaults).
func SetProcessTLSSettings(s TLSSettings) ([]string, error) {
	cfg, warnings, err := s.BuildTLSConfig()
	if err != nil {
		return nil, err
	}

	processTLSMu.Lock()
	defer processTLSMu.Unlock()
	if cfg == nil {
		processTLS = processTLSPolicy{}
		return warnings, nil
	}
	processTLS = processTLSPolicy{
		minVersion:       cfg.MinVersion,
		cipherSuites:     slices.Clone(cfg.CipherSuites),
		curvePreferences: slices.Clone(cfg.CurvePreferences),
	}
	return warnings, nil
}

// ResetProcessTLSSettingsForTest clears the process-wide TLS policy (tests only).
func ResetProcessTLSSettingsForTest() {
	processTLSMu.Lock()
	defer processTLSMu.Unlock()
	processTLS = processTLSPolicy{}
}

// HasProcessTLSPolicy reports whether any process-wide TLS knob is set.
func HasProcessTLSPolicy() bool {
	processTLSMu.RLock()
	defer processTLSMu.RUnlock()
	return processTLS.minVersion != 0 || len(processTLS.cipherSuites) > 0 || len(processTLS.curvePreferences) > 0
}

// ApplyProcessTLSConfig merges the process-wide TLS policy into dst.
// It only sets fields that were explicitly configured; Certificates, RootCAs,
// and other fields on dst are left untouched.
func ApplyProcessTLSConfig(dst *tls.Config) {
	if dst == nil {
		return
	}
	processTLSMu.RLock()
	defer processTLSMu.RUnlock()
	if processTLS.minVersion != 0 {
		dst.MinVersion = processTLS.minVersion
	}
	if len(processTLS.cipherSuites) > 0 {
		dst.CipherSuites = slices.Clone(processTLS.cipherSuites)
	}
	if len(processTLS.curvePreferences) > 0 {
		dst.CurvePreferences = slices.Clone(processTLS.curvePreferences)
	}
}

// ProcessTLSOpt returns a tls.Config mutator for controller-runtime TLSOpts.
// Returns nil when no process-wide TLS policy is configured.
func ProcessTLSOpt() func(*tls.Config) {
	if !HasProcessTLSPolicy() {
		return nil
	}
	return ApplyProcessTLSConfig
}

// TLSVersionName returns a human-readable name for a TLS version constant.
func TLSVersionName(version uint16) string {
	switch version {
	case tls.VersionTLS11:
		return "1.1"
	case tls.VersionTLS12:
		return "1.2"
	case tls.VersionTLS13:
		return "1.3"
	default:
		return fmt.Sprintf("unknown (%d)", version)
	}
}

// ParseTLSVersion parses a TLS version string (e.g. "1.2", "1.3", "TLS1.2") into a tls version constant.
// Returns 0 if the string is empty (meaning "use default").
func ParseTLSVersion(version string) (uint16, error) {
	if version == "" {
		return 0, nil
	}
	v, ok := tlsVersionMap[strings.ToLower(strings.TrimSpace(version))]
	if !ok {
		return 0, fmt.Errorf("unsupported TLS version: %q (supported: 1.1, 1.2, 1.3)", version)
	}
	return v, nil
}

// ParseTLSCiphers parses a colon-separated list of cipher suite names into cipher suite IDs.
// Only secure cipher suites (from tls.CipherSuites()) are allowed.
// Returns nil if the input is empty.
func ParseTLSCiphers(ciphers string) ([]uint16, error) {
	if ciphers == "" {
		return nil, nil
	}

	cipherMap := make(map[string]uint16, len(tls.CipherSuites()))
	for _, cs := range tls.CipherSuites() {
		cipherMap[cs.Name] = cs.ID
	}

	var result []uint16
	for _, name := range strings.Split(ciphers, ":") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		id, ok := cipherMap[name]
		if !ok {
			return nil, fmt.Errorf("unsupported TLS cipher suite: %q", name)
		}
		result = append(result, id)
	}
	return result, nil
}

// ParseTLSCurvePreferences parses a colon-separated list of elliptic curve names into CurveIDs.
// Returns nil if the input is empty.
func ParseTLSCurvePreferences(curves string) ([]tls.CurveID, error) {
	if curves == "" {
		return nil, nil
	}

	var result []tls.CurveID
	for _, name := range strings.Split(curves, ":") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		id, ok := tlsCurveMap[strings.ToLower(name)]
		if !ok {
			return nil, fmt.Errorf("unsupported TLS curve preference: %q (supported: CurveP256, CurveP384, CurveP521, X25519)", name)
		}
		result = append(result, id)
	}
	return result, nil
}

// ValidateTLSConfig validates the TLS configuration parameters.
// It checks that all configured cipher suites are compatible with the minimum TLS version.
func ValidateTLSConfig(minVersion uint16, cipherSuites []uint16) error {
	if len(cipherSuites) == 0 || minVersion == 0 {
		return nil
	}

	availableCiphers := tls.CipherSuites()
	for _, cipherID := range cipherSuites {
		for _, cs := range availableCiphers {
			if cs.ID != cipherID {
				continue
			}
			supported := false
			for _, v := range cs.SupportedVersions {
				if v == minVersion {
					supported = true
					break
				}
			}
			if !supported {
				return fmt.Errorf("cipher suite %s is not supported by minimum TLS version %s",
					cs.Name, TLSVersionName(minVersion))
			}
			break
		}
	}

	return nil
}

// BuildTLSConfig creates a *tls.Config from TLSSettings.
// Returns (nil, nil, nil) when no settings are configured (use Go defaults).
// The second return value is optional warning messages (e.g. ignored ciphers with TLS 1.3).
func (t TLSSettings) BuildTLSConfig() (*tls.Config, []string, error) {
	if t.MinVersion == "" && t.Ciphers == "" && t.CurvePreferences == "" {
		return nil, nil, nil
	}

	tlsCfg := &tls.Config{} //nolint:gosec // MinVersion comes from caller config; empty means Go default

	minVer, err := ParseTLSVersion(t.MinVersion)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid TLS min version: %w", err)
	}
	tlsCfg.MinVersion = minVer

	ciphers, err := ParseTLSCiphers(t.Ciphers)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid TLS ciphers: %w", err)
	}

	var warnings []string
	// Go's tls.Config.CipherSuites only applies to TLS 1.0–1.2.
	// TLS 1.3 cipher suites are not configurable and are always enabled.
	if len(ciphers) > 0 && minVer >= tls.VersionTLS13 {
		warnings = append(warnings,
			"TLS ciphers have no effect when TLS min version is 1.3 or higher (TLS 1.3 cipher suites are not configurable); ignoring")
		ciphers = nil
	}
	tlsCfg.CipherSuites = ciphers

	curves, err := ParseTLSCurvePreferences(t.CurvePreferences)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid TLS curve preferences: %w", err)
	}
	tlsCfg.CurvePreferences = curves

	if err := ValidateTLSConfig(minVer, ciphers); err != nil {
		return nil, nil, err
	}

	return tlsCfg, warnings, nil
}

// TLSOpt returns a tls.Config mutator suitable for controller-runtime TLSOpts.
// When no settings are configured, it returns nil.
func (t TLSSettings) TLSOpt() (func(*tls.Config), []string, error) {
	cfg, warnings, err := t.BuildTLSConfig()
	if err != nil {
		return nil, nil, err
	}
	if cfg == nil {
		return nil, nil, nil
	}
	return func(c *tls.Config) {
		if cfg.MinVersion != 0 {
			c.MinVersion = cfg.MinVersion
		}
		if len(cfg.CipherSuites) > 0 {
			c.CipherSuites = cfg.CipherSuites
		}
		if len(cfg.CurvePreferences) > 0 {
			c.CurvePreferences = cfg.CurvePreferences
		}
	}, warnings, nil
}
