package utils

import (
	"crypto/tls"
	"fmt"
	"slices"
	"sync"

	cliflag "k8s.io/component-base/cli/flag"
)

// TLSSettings holds TLS policy knobs in the same shapes as Kubernetes
// RecommendedOptions.SecureServing (typically from CLI flags).
// Empty fields mean "use Go defaults" — nothing is hardcoded by this package.
type TLSSettings struct {
	// MinVersion is a Kubernetes TLS version name (e.g. "VersionTLS12").
	MinVersion string
	// CipherSuites are cipher suite names (comma-separated on the CLI).
	CipherSuites []string
	// CurvePreferences are numeric crypto/tls CurveID values.
	CurvePreferences []int32
}

// processTLS holds the parsed process-wide TLS policy set at startup.
// Empty/zero means leave that field to Go defaults.
type processTLSPolicy struct {
	cipherSuites     []uint16
	curvePreferences []tls.CurveID
	minVersion       uint16
}

func (p processTLSPolicy) empty() bool {
	return p.minVersion == 0 && len(p.cipherSuites) == 0 && len(p.curvePreferences) == 0
}

var (
	processTLSMu sync.RWMutex
	processTLS   processTLSPolicy
)

// SetProcessTLSSettings parses and installs the process-wide TLS policy used by
// metrics/webhook servers, http.DefaultTransport, and outbound mTLS clients.
// Empty settings clear any previously installed policy (Go defaults).
func SetProcessTLSSettings(s TLSSettings) ([]string, error) {
	policy, warnings, err := s.parse()
	if err != nil {
		return nil, err
	}

	processTLSMu.Lock()
	defer processTLSMu.Unlock()
	processTLS = processTLSPolicy{
		minVersion:       policy.minVersion,
		cipherSuites:     slices.Clone(policy.cipherSuites),
		curvePreferences: slices.Clone(policy.curvePreferences),
	}
	return warnings, nil
}

// ResetProcessTLSSettingsForTest clears the process-wide TLS policy (tests only).
func ResetProcessTLSSettingsForTest() {
	processTLSMu.Lock()
	defer processTLSMu.Unlock()
	processTLS = processTLSPolicy{}
}

// ApplyProcessTLSConfig merges the process-wide TLS policy into dst.
// It only sets fields that were explicitly configured; Certificates, RootCAs,
// and other fields on dst are left untouched. An empty policy is a no-op.
func ApplyProcessTLSConfig(dst *tls.Config) {
	if dst == nil {
		return
	}
	processTLSMu.RLock()
	defer processTLSMu.RUnlock()
	// Only VersionTLS12+ is stored in processTLS (see parse); never write 0/1.0/1.1.
	if minVersion := processTLS.minVersion; minVersion >= tls.VersionTLS12 {
		dst.MinVersion = minVersion
	}
	if len(processTLS.cipherSuites) > 0 {
		dst.CipherSuites = slices.Clone(processTLS.cipherSuites)
	}
	if len(processTLS.curvePreferences) > 0 {
		dst.CurvePreferences = slices.Clone(processTLS.curvePreferences)
	}
}

// TLSVersionName returns a human-readable name for a TLS version constant.
func TLSVersionName(version uint16) string {
	switch version {
	case tls.VersionTLS10:
		return "VersionTLS10"
	case tls.VersionTLS11:
		return "VersionTLS11"
	case tls.VersionTLS12:
		return "VersionTLS12"
	case tls.VersionTLS13:
		return "VersionTLS13"
	default:
		return fmt.Sprintf("unknown (%d)", version)
	}
}

// ParseTLSVersion parses a Kubernetes TLS version name (e.g. "VersionTLS12").
// Returns 0 if the string is empty (meaning "leave MinVersion unset / Go default").
// Unlike cliflag.TLSVersion, empty does not force DefaultTLSVersion so callers can
// omit --tls-min-version without writing MinVersion onto every tls.Config.
func ParseTLSVersion(version string) (uint16, error) {
	if version == "" {
		return 0, nil
	}
	v, err := cliflag.TLSVersion(version)
	if err != nil {
		return 0, fmt.Errorf("parse TLS version: %w", err)
	}
	// cliflag also accepts VersionTLS10/11; reject them so this flag cannot lower
	// Go's default floor (TLS 1.2) and so we never assign an insecure MinVersion.
	if v < tls.VersionTLS12 {
		return 0, fmt.Errorf("unsupported TLS version %q (supported: VersionTLS12, VersionTLS13)", version)
	}
	return v, nil
}

// ParseTLSCiphers parses cipher suite names via cliflag.TLSCipherSuites.
// TLS 1.3 suite names are rejected: Go's tls.Config.CipherSuites does not
// configure TLS 1.3, and listing only those IDs leaves no usable suites for
// TLS 1.2 handshakes.
func ParseTLSCiphers(cipherNames []string) ([]uint16, error) {
	ids, err := cliflag.TLSCipherSuites(cipherNames)
	if err != nil {
		return nil, fmt.Errorf("parse TLS cipher suites: %w", err)
	}
	if err := rejectTLS13OnlyCiphers(ids); err != nil {
		return nil, err
	}
	return ids, nil
}

func rejectTLS13OnlyCiphers(ids []uint16) error {
	for _, id := range ids {
		for _, cs := range tls.CipherSuites() {
			if cs.ID != id {
				continue
			}
			supportsTLS12 := false
			for _, v := range cs.SupportedVersions {
				if v <= tls.VersionTLS12 {
					supportsTLS12 = true
					break
				}
			}
			if !supportsTLS12 {
				return fmt.Errorf("TLS 1.3 cipher suites are not configurable in Go: %q", cs.Name)
			}
			break
		}
	}
	return nil
}

// ParseTLSCurvePreferences parses numeric crypto/tls CurveID values via
// cliflag.TLSCurvePreferences (same shape as --tls-curve-preferences on apiserver).
func ParseTLSCurvePreferences(curveIDs []int32) ([]tls.CurveID, error) {
	curves, err := cliflag.TLSCurvePreferences(curveIDs)
	if err != nil {
		return nil, fmt.Errorf("parse TLS curve preferences: %w", err)
	}
	return curves, nil
}

// ValidateTLSConfig validates the TLS configuration parameters.
// It checks that each configured cipher suite can be negotiated under the given
// minimum TLS version. A cipher is accepted when it supports any version in the
// inclusive range [minVersion, TLS 1.2]: MinVersion is a floor (higher versions
// up to TLS 1.2 remain allowed), and Go's tls.Config.CipherSuites has no effect
// above TLS 1.2. When minVersion is 0, Go's default floor (TLS 1.2) is used so
// cipher lists are validated even when --tls-min-version is unset.
// Callers must clear cipherSuites when minVersion is TLS 1.3+ before calling;
// CipherSuites have no effect at those versions.
func ValidateTLSConfig(minVersion uint16, cipherSuites []uint16) error {
	if len(cipherSuites) == 0 {
		return nil
	}
	if minVersion == 0 {
		minVersion = tls.VersionTLS12
	}

	availableCiphers := append([]*tls.CipherSuite(nil), tls.CipherSuites()...)
	availableCiphers = append(availableCiphers, tls.InsecureCipherSuites()...)
	for _, cipherID := range cipherSuites {
		for _, cs := range availableCiphers {
			if cs.ID != cipherID {
				continue
			}
			compatible := false
			for _, v := range cs.SupportedVersions {
				if v >= minVersion && v <= tls.VersionTLS12 {
					compatible = true
					break
				}
			}
			if !compatible {
				return fmt.Errorf("cipher suite %s is not supported for minimum TLS version %s",
					cs.Name, TLSVersionName(minVersion))
			}
			break
		}
	}

	return nil
}

// parse converts TLSSettings into a processTLSPolicy.
// Returns a zero policy when no settings are configured.
func (t TLSSettings) parse() (processTLSPolicy, []string, error) {
	if t.MinVersion == "" && len(t.CipherSuites) == 0 && len(t.CurvePreferences) == 0 {
		return processTLSPolicy{}, nil, nil
	}

	minVer, err := ParseTLSVersion(t.MinVersion)
	if err != nil {
		return processTLSPolicy{}, nil, fmt.Errorf("invalid TLS min version: %w", err)
	}

	ciphers, err := ParseTLSCiphers(t.CipherSuites)
	if err != nil {
		return processTLSPolicy{}, nil, fmt.Errorf("invalid TLS ciphers: %w", err)
	}

	var warnings []string
	// Go's tls.Config.CipherSuites only applies to TLS 1.0–1.2.
	// TLS 1.3 cipher suites are not configurable and are always enabled.
	if len(ciphers) > 0 && minVer >= tls.VersionTLS13 {
		warnings = append(warnings,
			"TLS ciphers have no effect when min version is VersionTLS13 or higher; ignoring")
		ciphers = nil
	}

	curves, err := ParseTLSCurvePreferences(t.CurvePreferences)
	if err != nil {
		return processTLSPolicy{}, nil, fmt.Errorf("invalid TLS curve preferences: %w", err)
	}

	if err := ValidateTLSConfig(minVer, ciphers); err != nil {
		return processTLSPolicy{}, nil, err
	}

	return processTLSPolicy{
		minVersion:       minVer,
		cipherSuites:     ciphers,
		curvePreferences: curves,
	}, warnings, nil
}

// BuildTLSConfig creates a *tls.Config from TLSSettings.
// Returns (nil, nil, nil) when no settings are configured (use Go defaults).
// The second return value is optional warning messages (e.g. ignored ciphers with TLS 1.3).
func (t TLSSettings) BuildTLSConfig() (*tls.Config, []string, error) {
	policy, warnings, err := t.parse()
	if err != nil {
		return nil, nil, err
	}
	if policy.empty() {
		return nil, warnings, nil
	}
	cfg := &tls.Config{
		CipherSuites:     slices.Clone(policy.cipherSuites),
		CurvePreferences: slices.Clone(policy.curvePreferences),
	}
	if policy.minVersion >= tls.VersionTLS12 {
		cfg.MinVersion = policy.minVersion
	}
	return cfg, warnings, nil
}
