package reviewers

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

const (
	maxReviewerCount = 10
	maxNameLength    = 100
)

var reviewerNamePattern = regexp.MustCompile(`^[^\s\x00]+$`)

// Validate checks reviewers against the same rules enforced by PullRequest CRD admission.
func Validate(list []promoterv1alpha1.PullRequestReviewer) error {
	if len(list) > maxReviewerCount {
		return fmt.Errorf("at most %d reviewers allowed, got %d", maxReviewerCount, len(list))
	}

	seen := make(map[promoterv1alpha1.PullRequestReviewer]struct{}, len(list))
	for i, reviewer := range list {
		if (reviewer.User == "") == (reviewer.Group == "") {
			return fmt.Errorf("reviewer at index %d must set exactly one of user or group", i)
		}

		name := reviewer.User
		if name == "" {
			name = reviewer.Group
		}
		if utf8.RuneCountInString(name) > maxNameLength {
			return fmt.Errorf("reviewer %q exceeds maximum length of %d characters", name, maxNameLength)
		}
		if !reviewerNamePattern.MatchString(name) {
			return fmt.Errorf("reviewer %q contains invalid characters (whitespace and NUL are not allowed)", name)
		}
		if _, ok := seen[reviewer]; ok {
			return fmt.Errorf("duplicate reviewer %q", name)
		}
		seen[reviewer] = struct{}{}
	}

	return nil
}

// SetsEqual reports whether two reviewer sets contain the same elements (order ignored).
func SetsEqual(a, b []promoterv1alpha1.PullRequestReviewer) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	aCopy := slices.Clone(a)
	bCopy := slices.Clone(b)
	slices.SortFunc(aCopy, compareReviewers)
	slices.SortFunc(bCopy, compareReviewers)
	return slices.Equal(aCopy, bCopy)
}

// Diff returns reviewers to add and remove when moving from applied to desired.
// Only reviewers in applied are candidates for removal (promoter ownership).
func Diff(desired, applied []promoterv1alpha1.PullRequestReviewer) (toAdd, toRemove []promoterv1alpha1.PullRequestReviewer) {
	appliedSet := make(map[promoterv1alpha1.PullRequestReviewer]struct{}, len(applied))
	for _, r := range applied {
		appliedSet[r] = struct{}{}
	}
	desiredSet := make(map[promoterv1alpha1.PullRequestReviewer]struct{}, len(desired))
	for _, r := range desired {
		desiredSet[r] = struct{}{}
		if _, ok := appliedSet[r]; !ok {
			toAdd = append(toAdd, r)
		}
	}
	for _, r := range applied {
		if _, ok := desiredSet[r]; !ok {
			toRemove = append(toRemove, r)
		}
	}
	slices.SortFunc(toAdd, compareReviewers)
	slices.SortFunc(toRemove, compareReviewers)
	return toAdd, toRemove
}

func compareReviewers(a, b promoterv1alpha1.PullRequestReviewer) int {
	if cmp := strings.Compare(a.User, b.User); cmp != 0 {
		return cmp
	}
	return strings.Compare(a.Group, b.Group)
}
