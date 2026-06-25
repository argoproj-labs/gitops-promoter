package labels

import (
	"fmt"
	"regexp"
	"slices"
	"unicode/utf8"
)

const (
	maxLabelCount  = 10
	maxLabelLength = 50
)

var labelNamePattern = regexp.MustCompile(`^[^\n\r\x00]+$`)

// ValidateLabelNames checks SCM label names against the same rules enforced by PullRequest CRD admission.
func ValidateLabelNames(names []string) error {
	if len(names) > maxLabelCount {
		return fmt.Errorf("at most %d labels allowed, got %d", maxLabelCount, len(names))
	}

	seen := make(map[string]struct{}, len(names))
	for i, name := range names {
		if name == "" {
			return fmt.Errorf("label at index %d must not be empty", i)
		}
		if utf8.RuneCountInString(name) > maxLabelLength {
			return fmt.Errorf("label %q exceeds maximum length of %d characters", name, maxLabelLength)
		}
		if !labelNamePattern.MatchString(name) {
			return fmt.Errorf("label %q contains invalid characters (newlines and NUL are not allowed)", name)
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("duplicate label %q", name)
		}
		seen[name] = struct{}{}
	}

	return nil
}

// SetsEqual reports whether two label sets contain the same elements (order ignored).
func SetsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	aCopy := slices.Clone(a)
	bCopy := slices.Clone(b)
	slices.Sort(aCopy)
	slices.Sort(bCopy)
	return slices.Equal(aCopy, bCopy)
}

// ObservedManaged returns SCM labels within the promoter's managed set (union of desired and applied).
// Used to refresh status.appliedLabels from FindOpen without treating third-party SCM labels as owned.
func ObservedManaged(desired, applied, scm []string) []string {
	managed := unionLabelSet(desired, applied)
	if len(managed) == 0 {
		return nil
	}
	observed := make([]string, 0, len(scm))
	for _, name := range scm {
		if _, ok := managed[name]; ok {
			observed = append(observed, name)
		}
	}
	slices.Sort(observed)
	return observed
}

func unionLabelSet(a, b []string) map[string]struct{} {
	out := make(map[string]struct{}, len(a)+len(b))
	for _, name := range a {
		out[name] = struct{}{}
	}
	for _, name := range b {
		out[name] = struct{}{}
	}
	return out
}

// Diff returns labels to add and remove when moving from applied to desired.
// Only labels in applied are candidates for removal (promoter ownership).
func Diff(desired, applied []string) (toAdd, toRemove []string) {
	appliedSet := make(map[string]struct{}, len(applied))
	for _, l := range applied {
		appliedSet[l] = struct{}{}
	}
	desiredSet := make(map[string]struct{}, len(desired))
	for _, l := range desired {
		desiredSet[l] = struct{}{}
		if _, ok := appliedSet[l]; !ok {
			toAdd = append(toAdd, l)
		}
	}
	for _, l := range applied {
		if _, ok := desiredSet[l]; !ok {
			toRemove = append(toRemove, l)
		}
	}
	slices.Sort(toAdd)
	slices.Sort(toRemove)
	return toAdd, toRemove
}
