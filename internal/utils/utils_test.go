package utils

import (
	"testing"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/stretchr/testify/assert"
)

func TestTruncateString(t *testing.T) {
	t.Parallel() // Enable parallel execution for the top-level test
	tests := []struct {
		name     string
		input    string
		length   int
		expected string
	}{
		{"Empty string", "", 5, ""},
		{"Short string", "abc", 5, "abc"},
		{"Exact length", "abcde", 5, "abcde"},
		{"Truncated string", "abcdef", 5, "abcde"},
		{"Negative length", "abcdef", -1, ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := TruncateString(test.input, test.length)
			assert.Equal(t, test.expected, result)
		})
	}
}

func TestUpsertEnvironmentStatus(t *testing.T) {
	t.Parallel() // Enable parallel execution for the top-level test
	tests := []struct {
		name     string
		initial  []promoterv1alpha1.EnvironmentStatus
		insert   promoterv1alpha1.EnvironmentStatus
		expected []promoterv1alpha1.EnvironmentStatus
	}{
		{
			name:     "Upsert on empty slice",
			initial:  []promoterv1alpha1.EnvironmentStatus{},
			insert:   promoterv1alpha1.EnvironmentStatus{Branch: "main"},
			expected: []promoterv1alpha1.EnvironmentStatus{{Branch: "main"}},
		},
		{
			name:     "Append new element",
			initial:  []promoterv1alpha1.EnvironmentStatus{{Branch: "main"}},
			insert:   promoterv1alpha1.EnvironmentStatus{Branch: "dev"},
			expected: []promoterv1alpha1.EnvironmentStatus{{Branch: "main"}, {Branch: "dev"}},
		},
		{
			name:     "Edge case with one element",
			initial:  []promoterv1alpha1.EnvironmentStatus{{Branch: "main"}},
			insert:   promoterv1alpha1.EnvironmentStatus{Branch: "dev"},
			expected: []promoterv1alpha1.EnvironmentStatus{{Branch: "main"}, {Branch: "dev"}},
		},
		{
			name: "Update existing element",
			initial: []promoterv1alpha1.EnvironmentStatus{{
				Branch: "main",
				Active: promoterv1alpha1.PromotionStrategyBranchStateStatus{
					Dry: promoterv1alpha1.CommitShaState{
						Sha: "old",
					},
					Hydrated: promoterv1alpha1.CommitShaState{
						Sha: "old",
					},
					CommitStatus: promoterv1alpha1.PromotionStrategyCommitStatus{
						Sha:   "old",
						Phase: "pending",
					},
				},
			}},
			insert: promoterv1alpha1.EnvironmentStatus{
				Branch: "main",
				Active: promoterv1alpha1.PromotionStrategyBranchStateStatus{
					Dry: promoterv1alpha1.CommitShaState{
						Sha: "new",
					},
					Hydrated: promoterv1alpha1.CommitShaState{
						Sha: "new",
					},
					CommitStatus: promoterv1alpha1.PromotionStrategyCommitStatus{
						Sha:   "new",
						Phase: "success",
					},
				},
			},
			expected: []promoterv1alpha1.EnvironmentStatus{{
				Branch: "main",
				Active: promoterv1alpha1.PromotionStrategyBranchStateStatus{
					Dry: promoterv1alpha1.CommitShaState{
						Sha: "new",
					},
					Hydrated: promoterv1alpha1.CommitShaState{
						Sha: "new",
					},
					CommitStatus: promoterv1alpha1.PromotionStrategyCommitStatus{
						Sha:   "new",
						Phase: "success",
					},
				},
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := UpsertEnvironmentStatus(test.initial, test.insert)
			assert.Equal(t, test.expected, result)
		})
	}
}
