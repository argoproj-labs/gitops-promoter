package utils

import (
	"testing"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/stretchr/testify/assert"
)

func TestTruncateString(t *testing.T) {
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := TruncateString(tt.input, tt.length)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestUpsertEnvironmentStatus(t *testing.T) {
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Call the function under test
			result := UpsertEnvironmentStatus(tt.initial, tt.insert)

			// Assert the result matches the expected value using testify assert
			assert.Equal(t, tt.expected, result)
		})
	}
}
