package webhookreceiver

import (
	"testing"
)

func TestExtractBitbucketDCPayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		payload   string
		wantSHA   string
		wantRef   string
	}{
		{
			name: "valid repo:refs_changed payload with single change",
			payload: `{
				"eventKey": "repo:refs_changed",
				"changes": [
					{
						"ref": {
							"id": "refs/heads/main",
							"displayId": "main",
							"type": "BRANCH"
						},
						"fromHash": "ecddabb624beac4c10cb214fe07e50d1fc850d36",
						"toHash": "178864a7d521b6f5e720b386b2c2b0ef8563e0dc",
						"type": "UPDATE"
					}
				]
			}`,
			wantSHA: "ecddabb624beac4c10cb214fe07e50d1fc850d36",
			wantRef: "refs/heads/main",
		},
		{
			name: "multiple changes - only first entry is used",
			payload: `{
				"eventKey": "repo:refs_changed",
				"changes": [
					{
						"ref": {"id": "refs/heads/main"},
						"fromHash": "aaabbbccc"
					},
					{
						"ref": {"id": "refs/heads/feature"},
						"fromHash": "dddeeefff"
					}
				]
			}`,
			wantSHA: "aaabbbccc",
			wantRef: "refs/heads/main",
		},
		{
			name: "missing eventKey - returns empty strings",
			payload: `{
				"changes": [
					{
						"ref": {"id": "refs/heads/main"},
						"fromHash": "ecddabb624beac4c10cb214fe07e50d1fc850d36"
					}
				]
			}`,
			wantSHA: "",
			wantRef: "",
		},
		{
			name: "missing changes field - returns empty strings",
			payload: `{
				"eventKey": "repo:refs_changed"
			}`,
			wantSHA: "",
			wantRef: "",
		},
		{
			name: "empty changes array - returns empty strings",
			payload: `{
				"eventKey": "repo:refs_changed",
				"changes": []
			}`,
			wantSHA: "",
			wantRef: "",
		},
		{
			name: "change without ref.id - sha extracted, ref empty",
			payload: `{
				"eventKey": "repo:refs_changed",
				"changes": [
					{
						"fromHash": "ecddabb624beac4c10cb214fe07e50d1fc850d36"
					}
				]
			}`,
			wantSHA: "ecddabb624beac4c10cb214fe07e50d1fc850d36",
			wantRef: "",
		},
		{
			name:    "empty payload - returns empty strings",
			payload: `{}`,
			wantSHA: "",
			wantRef: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotSHA, gotRef := extractBitbucketDCPayload([]byte(tt.payload))
			if gotSHA != tt.wantSHA {
				t.Errorf("extractBitbucketDCPayload() sha = %q, want %q", gotSHA, tt.wantSHA)
			}
			if gotRef != tt.wantRef {
				t.Errorf("extractBitbucketDCPayload() ref = %q, want %q", gotRef, tt.wantRef)
			}
		})
	}
}
