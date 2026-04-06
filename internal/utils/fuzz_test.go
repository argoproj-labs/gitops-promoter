package utils_test

import (
	"testing"
	"unicode/utf8"

	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

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
