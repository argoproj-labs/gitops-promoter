package utils

import (
	"crypto/tls"
	"slices"
	"testing"
)

func TestParseTLSVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    uint16
		wantErr bool
	}{
		{name: "empty means default", input: "", want: 0},
		{name: "1.2", input: "1.2", want: tls.VersionTLS12},
		{name: "TLS1.3 case insensitive", input: "TLS1.3", want: tls.VersionTLS13},
		{name: "tls1.1 with spaces", input: "  tls1.1  ", want: tls.VersionTLS11},
		{name: "unsupported", input: "1.0", wantErr: true},
		{name: "garbage", input: "ssl3", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseTLSVersion(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got version %d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestParseTLSCiphers(t *testing.T) {
	t.Parallel()

	secure := tls.CipherSuites()
	if len(secure) < 2 {
		t.Fatal("expected at least two secure cipher suites from crypto/tls")
	}
	name1 := secure[0].Name
	name2 := secure[1].Name

	tests := []struct {
		name    string
		input   string
		want    []uint16
		wantErr bool
	}{
		{name: "empty", input: "", want: nil},
		{name: "single", input: name1, want: []uint16{secure[0].ID}},
		{name: "colon separated", input: name1 + ":" + name2, want: []uint16{secure[0].ID, secure[1].ID}},
		{name: "with spaces", input: " " + name1 + " : " + name2 + " ", want: []uint16{secure[0].ID, secure[1].ID}},
		{name: "unsupported", input: "TLS_NOT_A_REAL_CIPHER", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseTLSCiphers(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseTLSCurvePreferences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    []tls.CurveID
		wantErr bool
	}{
		{name: "empty", input: "", want: nil},
		{name: "X25519", input: "X25519", want: []tls.CurveID{tls.X25519}},
		{name: "aliases", input: "P-256:CurveP384:x25519", want: []tls.CurveID{tls.CurveP256, tls.CurveP384, tls.X25519}},
		{name: "unsupported", input: "secp256k1", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseTLSCurvePreferences(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidateTLSConfig(t *testing.T) {
	t.Parallel()

	// ECDHE-GCM suites list only VersionTLS12 in SupportedVersions, but a MinVersion of
	// 1.1 still allows TLS 1.2, so they must be accepted.
	var cipherTLS12Only uint16
	var cipherTLS12OnlyName string
	found := false
	for _, cs := range tls.CipherSuites() {
		supports12 := false
		supports13 := false
		for _, v := range cs.SupportedVersions {
			if v == tls.VersionTLS12 {
				supports12 = true
			}
			if v == tls.VersionTLS13 {
				supports13 = true
			}
		}
		if supports12 && !supports13 {
			cipherTLS12Only = cs.ID
			cipherTLS12OnlyName = cs.Name
			found = true
			break
		}
	}
	if !found {
		t.Skip("no TLS 1.2-only cipher suite available")
	}

	if err := ValidateTLSConfig(tls.VersionTLS11, []uint16{cipherTLS12Only}); err != nil {
		t.Fatalf("min TLS 1.1 with %s should pass (TLS 1.2 is still allowed): %v", cipherTLS12OnlyName, err)
	}
	if err := ValidateTLSConfig(tls.VersionTLS12, []uint16{cipherTLS12Only}); err != nil {
		t.Fatalf("expected compatible cipher to pass: %v", err)
	}
	// CipherSuites have no effect above TLS 1.2, so min 1.3 skips the check.
	if err := ValidateTLSConfig(tls.VersionTLS13, []uint16{cipherTLS12Only}); err != nil {
		t.Fatalf("min TLS 1.3 should skip cipher check: %v", err)
	}
	if err := ValidateTLSConfig(0, []uint16{cipherTLS12Only}); err != nil {
		t.Fatalf("empty min version should skip cipher check: %v", err)
	}
}

func TestTLSSettingsBuildTLSConfig(t *testing.T) {
	t.Parallel()

	t.Run("all empty returns nil", func(t *testing.T) {
		t.Parallel()
		cfg, warnings, err := TLSSettings{}.BuildTLSConfig()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg != nil || warnings != nil {
			t.Fatalf("expected nil config and warnings, got cfg=%v warnings=%v", cfg, warnings)
		}
	})

	t.Run("min version and curves", func(t *testing.T) {
		t.Parallel()
		cfg, warnings, err := TLSSettings{
			MinVersion:       "1.2",
			CurvePreferences: "X25519:P-256",
		}.BuildTLSConfig()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(warnings) != 0 {
			t.Fatalf("unexpected warnings: %v", warnings)
		}
		if cfg.MinVersion != tls.VersionTLS12 {
			t.Fatalf("MinVersion got %d, want %d", cfg.MinVersion, tls.VersionTLS12)
		}
		wantCurves := []tls.CurveID{tls.X25519, tls.CurveP256}
		if !slices.Equal(cfg.CurvePreferences, wantCurves) {
			t.Fatalf("CurvePreferences got %v, want %v", cfg.CurvePreferences, wantCurves)
		}
	})

	t.Run("ciphers ignored when min is 1.3", func(t *testing.T) {
		t.Parallel()
		name := tls.CipherSuites()[0].Name
		cfg, warnings, err := TLSSettings{
			MinVersion: "1.3",
			Ciphers:    name,
		}.BuildTLSConfig()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(warnings) != 1 {
			t.Fatalf("expected one warning, got %v", warnings)
		}
		if len(cfg.CipherSuites) != 0 {
			t.Fatalf("expected ciphers cleared, got %v", cfg.CipherSuites)
		}
	})

	t.Run("invalid min version", func(t *testing.T) {
		t.Parallel()
		_, _, err := TLSSettings{MinVersion: "9.9"}.BuildTLSConfig()
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestTLSVersionName(t *testing.T) {
	t.Parallel()
	if got := TLSVersionName(tls.VersionTLS12); got != "1.2" {
		t.Fatalf("got %q, want 1.2", got)
	}
	if got := TLSVersionName(0); got != "unknown (0)" {
		t.Fatalf("got %q, want unknown (0)", got)
	}
}

//nolint:paralleltest // mutates process-wide TLS policy shared across the package
func TestSetProcessTLSSettingsAndApply(t *testing.T) {
	t.Cleanup(ResetProcessTLSSettingsForTest)
	ResetProcessTLSSettingsForTest()

	if HasProcessTLSPolicy() {
		t.Fatal("expected empty process TLS policy")
	}
	if opt := ProcessTLSOpt(); opt != nil {
		t.Fatal("expected nil ProcessTLSOpt when unset")
	}

	warnings, err := SetProcessTLSSettings(TLSSettings{
		MinVersion:       "1.3",
		CurvePreferences: "X25519",
	})
	if err != nil {
		t.Fatalf("SetProcessTLSSettings: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if !HasProcessTLSPolicy() {
		t.Fatal("expected process TLS policy to be set")
	}

	cfg := &tls.Config{} //nolint:gosec // test applies policy below
	ApplyProcessTLSConfig(cfg)
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Fatalf("MinVersion got %d, want %d", cfg.MinVersion, tls.VersionTLS13)
	}
	if !slices.Equal(cfg.CurvePreferences, []tls.CurveID{tls.X25519}) {
		t.Fatalf("CurvePreferences got %v", cfg.CurvePreferences)
	}

	opt := ProcessTLSOpt()
	if opt == nil {
		t.Fatal("expected non-nil ProcessTLSOpt")
	}
	cfg2 := &tls.Config{} //nolint:gosec // empty config; MinVersion set by ProcessTLSOpt
	opt(cfg2)
	if cfg2.MinVersion != tls.VersionTLS13 {
		t.Fatalf("opt MinVersion got %d, want %d", cfg2.MinVersion, tls.VersionTLS13)
	}

	if _, err := SetProcessTLSSettings(TLSSettings{}); err != nil {
		t.Fatalf("clear settings: %v", err)
	}
	if HasProcessTLSPolicy() {
		t.Fatal("expected process TLS policy cleared")
	}
}
