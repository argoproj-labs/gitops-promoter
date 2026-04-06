package utils_test

import (
	"context"
	"regexp"
	"testing"
	"unicode/utf8"

	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"k8s.io/apimachinery/pkg/util/validation"
)

// Lowercase hex suffix after the final hyphen (FNV-1a 32-bit via strconv.FormatUint).
var kubeSafeUniqueNameHashSuffix = regexp.MustCompile(`-[0-9a-f]+$`)

func FuzzTruncateStringFromBeginning(f *testing.F) {
	f.Add("abcdef", 3)
	f.Add("日本語", 2)
	f.Add("a\xc3", 1) // incomplete UTF-8 tail

	f.Fuzz(func(t *testing.T, s string, length int) {
		if len(s) > 4096 {
			t.Skip()
		}
		out := utils.TruncateStringFromBeginning(s, length)
		if !utf8.ValidString(out) {
			t.Fatalf("TruncateStringFromBeginning produced invalid UTF-8: in=%q length=%d out=%q", s, length, out)
		}
		if length <= 0 {
			if out != "" {
				t.Fatalf("length<=0 must yield empty string, got %q", out)
			}
			return
		}
		inRunes := utf8.RuneCountInString(s)
		outRunes := utf8.RuneCountInString(out)
		want := inRunes
		if length < want {
			want = length
		}
		if outRunes != want {
			t.Fatalf("rune count: in=%d length=%d want out runes %d got %d out=%q",
				inRunes, length, want, outRunes, out)
		}
	})
}

func FuzzKubeSafeUniqueName(f *testing.F) {
	ctx := context.Background()
	f.Add("ab")
	f.Add("foo-bar-baz")

	f.Fuzz(func(t *testing.T, name string) {
		if len(name) > 4096 {
			t.Skip()
		}
		out := utils.KubeSafeUniqueName(ctx, name)
		if !utf8.ValidString(out) {
			t.Fatalf("KubeSafeUniqueName produced invalid UTF-8: in=%q out=%q", name, out)
		}
		if errs := validation.IsDNS1123Subdomain(out); len(errs) > 0 {
			t.Fatalf("KubeSafeUniqueName(%q) => %q: not a DNS1123 subdomain: %v", name, out, errs)
		}
		if !kubeSafeUniqueNameHashSuffix.MatchString(out) {
			t.Fatalf("KubeSafeUniqueName(%q) => %q: want trailing -<hex> hash suffix (truncation may have dropped the hash)", name, out)
		}
	})
}
