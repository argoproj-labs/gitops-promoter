package utils

import (
	"regexp"
)

// Git ref validation regex components
// See: https://git-scm.com/docs/git-check-ref-format
const (
	// baseForbidden contains all characters forbidden by git-check-ref-format (except slash and dot)
	// FORBIDDEN characters (Rules 4, 5, 10):
	// - \x00-\x1F = ASCII control characters (bytes < 0x20)
	// - \x7F = DEL character (byte 127)
	// - Space = \x20
	// - ~^:?*[\\ = tilde, caret, colon, question mark, asterisk, open bracket, backslash
	//
	// NOTE: We do NOT forbid { and } here, even though Rule 8 prohibits @{ sequences.
	//       Enforcing this would require negative lookahead (@(?!\{)) which Go regex doesn't support.
	//       Rule 8 is documented as a limitation.
	baseForbidden = `\x00-\x1F\x7F ~^:?*\[\\`

	// Components in the MIDDLE of a ref (not the last component)
	// These can end with dots and can be the single character @
	componentStart  = `[^` + baseForbidden + `/.]` // can't start with dot (Rule 1)
	componentMiddle = `[^` + baseForbidden + `/]`  // allows dot in middle
	componentEnd    = `[^` + baseForbidden + `/]`  // allows dot at end (for middle components)

	middleSingleChar = `[^` + baseForbidden + `/.]` // single char can't be dot (would violate Rule 1)
	middleMultiChar  = componentStart + componentMiddle + `*` + componentEnd
	middleComponent  = `(?:` + middleSingleChar + `|` + middleMultiChar + `)`

	// The LAST component of a ref (or the only component)
	// Rule 7: Cannot end with dot - applies to the entire ref, so last component can't end with dot
	// Rule 9: Entire ref cannot be "@" - so if there's only one component, it can't be @
	lastComponentEnd = `[^` + baseForbidden + `/.]`  // can't end with dot (Rule 7)
	lastSingleChar   = `[^` + baseForbidden + `/.@]` // can't be @ (Rule 9) or dot
	lastMultiChar    = componentStart + componentMiddle + `*` + lastComponentEnd
	lastComponent    = `(?:` + lastSingleChar + `|` + lastMultiChar + `)`

	// GitRefPatternString is the full regex pattern for validating git references.
	// This is exported for documentation and for using the pattern in other contexts.
	//
	// GIT REF FORMAT RULES:
	// See: https://git-scm.com/docs/git-check-ref-format
	//
	// RULES ENFORCED BY REGEX:
	// ✓ Rule 1 (partial): Components cannot begin with dot (.)
	// ✓ Rule 4: Cannot have ASCII control chars, space, ~, ^, :
	// ✓ Rule 5: Cannot have ?, *, or [
	// ✓ Rule 6: Cannot begin/end with slash or have consecutive slashes (//)
	// ✓ Rule 7: Entire ref cannot end with dot (.) - middle components may end with dot
	// ✓ Rule 9: Entire ref cannot be "@" - @ is allowed as component in middle
	// ✓ Rule 10: Cannot contain backslash (\)
	//
	// RULES ENFORCED BY CEL (in CRD validation):
	// ✓ Rule 1 (partial): Cannot end with .lock
	//
	// RULES NOT ENFORCEABLE (Go RE2 regex limitations):
	// ✗ Rule 3: Cannot have consecutive dots (..) - requires lookahead
	// ✗ Rule 8: Cannot contain @{ - requires lookahead
	//
	// NOTE: Rule 2 (must contain slash) is NOT enforced.
	//       This validator accepts one-level refs (e.g., "main", "v1.0.0")
	//       This is equivalent to git check-ref-format --allow-onelevel
	//
	// GOLANG REGEX LIMITATIONS:
	// Go uses the RE2 regex engine which does NOT support:
	// - Lookahead/lookbehind assertions (?=, ?!, ?<=, ?<!)
	// - Backreferences (\1, \2, etc.)
	//
	// These limitations prevent pure regex validation of:
	// - Consecutive dots (..) - would need \.(?!\.)
	// - @{ sequences - would need @(?!\{)
	// - .lock endings - would need (?<!\.lock)
	//
	// The .lock rule is enforced via CEL validation in the CRD definitions.
	// The other two rules (.., @{) remain as known limitations and are documented
	// as pending tests in gitref_test.go
	//
	// PATTERN STRUCTURE:
	//   ^(?:middleComponent/)*lastComponent$
	//
	// WHERE:
	//   - middleComponent: can end with dot, can be @
	//   - lastComponent: cannot end with dot, cannot be single @
	//
	// This allows refs like "a/@/b" (@ in middle is OK) but blocks "@" alone (Rule 9).
	// This allows refs like "refs/heads./main" (component can end with .) but blocks "refs/heads/main." (Rule 7).
	GitRefPatternString = `^(?:` + middleComponent + `/)*` + lastComponent + `$`
)

// gitRefPattern is the compiled regex for validating git references.
var gitRefPattern = regexp.MustCompile(GitRefPatternString)

// IsValidGitRef validates a git reference name according to git-check-ref-format rules.
// Returns true if the ref name is valid, false otherwise.
//
// See GitRefPatternString for detailed documentation of which rules are enforced.
func IsValidGitRef(ref string) bool {
	return gitRefPattern.MatchString(ref)
}
