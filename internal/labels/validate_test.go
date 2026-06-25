package labels

import (
	"strings"
	"testing"
)

func TestValidateLabelNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		labels  []string
		wantErr bool
	}{
		{name: "valid", labels: []string{"lgtm", "approved"}},
		{name: "empty list", labels: nil},
		{name: "empty string", labels: []string{""}, wantErr: true},
		{name: "too long", labels: []string{strings.Repeat("a", 51)}, wantErr: true},
		{name: "max rune length", labels: []string{strings.Repeat("\u00e9", 50)}},
		{name: "too many runes", labels: []string{strings.Repeat("\u00e9", 51)}, wantErr: true},
		{name: "newline", labels: []string{"bad\nlabel"}, wantErr: true},
		{name: "nul", labels: []string{"bad\x00label"}, wantErr: true},
		{name: "duplicate", labels: []string{"a", "a"}, wantErr: true},
		{name: "too many", labels: make([]string, 11), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.name == "too many" {
				for i := range tt.labels {
					tt.labels[i] = "label"
				}
			}
			err := ValidateLabelNames(tt.labels)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateLabelNames() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSetsEqual(t *testing.T) {
	t.Parallel()

	if !SetsEqual([]string{"b", "a"}, []string{"a", "b"}) {
		t.Fatal("expected equal sets")
	}
	if SetsEqual([]string{"a"}, []string{"b"}) {
		t.Fatal("expected unequal sets")
	}
}

func TestDiff(t *testing.T) {
	t.Parallel()

	toAdd, toRemove := Diff([]string{"a", "c"}, []string{"a", "b"})
	if len(toAdd) != 1 || toAdd[0] != "c" {
		t.Fatalf("toAdd = %v, want [c]", toAdd)
	}
	if len(toRemove) != 1 || toRemove[0] != "b" {
		t.Fatalf("toRemove = %v, want [b]", toRemove)
	}
}
