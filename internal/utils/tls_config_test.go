package utils

import (
	"crypto/tls"
	"slices"
	"strings"
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
		{name: "VersionTLS12", input: "VersionTLS12", want: tls.VersionTLS12},
		{name: "VersionTLS13", input: "VersionTLS13", want: tls.VersionTLS13},
		{name: "VersionTLS11 rejected", input: "VersionTLS11", wantErr: true},
		{name: "VersionTLS10 rejected", input: "VersionTLS10", wantErr: true},
		{name: "short form rejected", input: "1.2", wantErr: true},
		{name: "unsupported", input: "VersionTLS99", wantErr: true},
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

	var tls12Suites []*tls.CipherSuite
	var tls13OnlyName string
	for _, cs := range tls.CipherSuites() {
		supports12 := false
		for _, v := range cs.SupportedVersions {
			if v <= tls.VersionTLS12 {
				supports12 = true
				break
			}
		}
		if supports12 {
			tls12Suites = append(tls12Suites, cs)
			continue
		}
		if tls13OnlyName == "" {
			tls13OnlyName = cs.Name
		}
	}
	if len(tls12Suites) < 2 {
		t.Fatal("expected at least two TLS 1.2 cipher suites from crypto/tls")
	}
	if tls13OnlyName == "" {
		t.Fatal("expected at least one TLS 1.3-only cipher suite from crypto/tls")
	}
	name1 := tls12Suites[0].Name
	name2 := tls12Suites[1].Name

	tests := []struct {
		name       string
		input      []string
		want       []uint16
		wantErr    bool
		errContain string
	}{
		{name: "empty", input: nil, want: nil},
		{name: "single", input: []string{name1}, want: []uint16{tls12Suites[0].ID}},
		{name: "multiple", input: []string{name1, name2}, want: []uint16{tls12Suites[0].ID, tls12Suites[1].ID}},
		{name: "unsupported", input: []string{"TLS_NOT_A_REAL_CIPHER"}, wantErr: true},
		{
			name:       "TLS 1.3 suite rejected",
			input:      []string{tls13OnlyName},
			wantErr:    true,
			errContain: "TLS 1.3 cipher suites are not configurable in Go",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseTLSCiphers(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				if tc.errContain != "" && !strings.Contains(err.Error(), tc.errContain) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.errContain)
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
		input   []int32
		want    []tls.CurveID
		wantErr bool
	}{
		{name: "empty", input: nil, want: nil},
		{name: "X25519", input: []int32{int32(tls.X25519)}, want: []tls.CurveID{tls.X25519}},
		{
			name:  "X25519MLKEM768 with classical fallback",
			input: []int32{int32(tls.X25519MLKEM768), int32(tls.X25519)},
			want:  []tls.CurveID{tls.X25519MLKEM768, tls.X25519},
		},
		{name: "P-256 and P-384", input: []int32{23, 24}, want: []tls.CurveID{tls.CurveP256, tls.CurveP384}},
		{name: "unsupported", input: []int32{99999}, wantErr: true},
		{name: "duplicate", input: []int32{29, 29}, wantErr: true},
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

	var cipherTLS12Only uint16
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
			found = true
			break
		}
	}
	if !found {
		t.Skip("no TLS 1.2-only cipher suite available")
	}

	if err := ValidateTLSConfig(tls.VersionTLS12, []uint16{cipherTLS12Only}); err != nil {
		t.Fatalf("expected compatible cipher to pass: %v", err)
	}
	// Unset min version uses Go's default floor (TLS 1.2).
	if err := ValidateTLSConfig(0, []uint16{cipherTLS12Only}); err != nil {
		t.Fatalf("empty min version should validate against TLS 1.2: %v", err)
	}
	// Callers must clear ciphers before validating when min is TLS 1.3+;
	// otherwise every suite fails the [min, TLS1.2] range check.
	if err := ValidateTLSConfig(tls.VersionTLS13, []uint16{cipherTLS12Only}); err == nil {
		t.Fatal("expected TLS 1.2 cipher to fail validation against min TLS 1.3")
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
			MinVersion:       "VersionTLS12",
			CurvePreferences: []int32{int32(tls.X25519), int32(tls.CurveP256)},
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

	t.Run("ciphers ignored when min is VersionTLS13", func(t *testing.T) {
		t.Parallel()
		var tls12Name string
		for _, cs := range tls.CipherSuites() {
			for _, v := range cs.SupportedVersions {
				if v <= tls.VersionTLS12 {
					tls12Name = cs.Name
					break
				}
			}
			if tls12Name != "" {
				break
			}
		}
		if tls12Name == "" {
			t.Fatal("expected a TLS 1.2 cipher suite")
		}
		cfg, warnings, err := TLSSettings{
			MinVersion:   "VersionTLS13",
			CipherSuites: []string{tls12Name},
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

	t.Run("TLS 1.3 cipher rejected without min version", func(t *testing.T) {
		t.Parallel()
		var tls13Name string
		for _, cs := range tls.CipherSuites() {
			supports12 := false
			for _, v := range cs.SupportedVersions {
				if v <= tls.VersionTLS12 {
					supports12 = true
					break
				}
			}
			if !supports12 {
				tls13Name = cs.Name
				break
			}
		}
		if tls13Name == "" {
			t.Fatal("expected a TLS 1.3-only cipher suite")
		}
		_, _, err := TLSSettings{CipherSuites: []string{tls13Name}}.BuildTLSConfig()
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "TLS 1.3 cipher suites are not configurable in Go") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("invalid min version", func(t *testing.T) {
		t.Parallel()
		_, _, err := TLSSettings{MinVersion: "1.2"}.BuildTLSConfig()
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestTLSVersionName(t *testing.T) {
	t.Parallel()
	if got := TLSVersionName(tls.VersionTLS12); got != "VersionTLS12" {
		t.Fatalf("got %q, want VersionTLS12", got)
	}
	if got := TLSVersionName(0); got != "unknown (0)" {
		t.Fatalf("got %q, want unknown (0)", got)
	}
}

//nolint:paralleltest // mutates process-wide TLS policy shared across the package
func TestSetProcessTLSSettingsAndApply(t *testing.T) {
	t.Cleanup(ResetProcessTLSSettingsForTest)
	ResetProcessTLSSettingsForTest()

	empty := &tls.Config{}
	ApplyProcessTLSConfig(empty)
	if empty.MinVersion != 0 || len(empty.CipherSuites) != 0 || len(empty.CurvePreferences) != 0 {
		t.Fatalf("empty policy should be a no-op, got %+v", empty)
	}

	warnings, err := SetProcessTLSSettings(TLSSettings{
		MinVersion:       "VersionTLS13",
		CurvePreferences: []int32{int32(tls.X25519)},
	})
	if err != nil {
		t.Fatalf("SetProcessTLSSettings: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}

	cfg := &tls.Config{}
	ApplyProcessTLSConfig(cfg)
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Fatalf("MinVersion got %d, want %d", cfg.MinVersion, tls.VersionTLS13)
	}
	if !slices.Equal(cfg.CurvePreferences, []tls.CurveID{tls.X25519}) {
		t.Fatalf("CurvePreferences got %v", cfg.CurvePreferences)
	}

	if _, err := SetProcessTLSSettings(TLSSettings{}); err != nil {
		t.Fatalf("clear settings: %v", err)
	}
	cleared := &tls.Config{MinVersion: tls.VersionTLS12}
	ApplyProcessTLSConfig(cleared)
	if cleared.MinVersion != tls.VersionTLS12 {
		t.Fatalf("cleared policy should leave existing MinVersion, got %d", cleared.MinVersion)
	}
}
