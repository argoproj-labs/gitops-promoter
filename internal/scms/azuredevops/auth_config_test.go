package azuredevops

import (
	"testing"

	v1 "k8s.io/api/core/v1"
)

func TestParseAuthConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data map[string][]byte
		want authConfig
	}{
		{
			name: "PAT by default",
			data: map[string][]byte{"token": []byte("pat-123")},
			want: authConfig{authType: AuthTypePAT, token: "pat-123"},
		},
		{
			name: "workload identity marker true",
			data: map[string][]byte{"workloadIdentity": []byte("true")},
			want: authConfig{authType: AuthTypeWorkloadIdentity},
		},
		{
			name: "workload identity with overrides",
			data: map[string][]byte{
				"workloadIdentity": []byte("true"),
				"azureClientID":    []byte("client-1"),
				"azureTenantID":    []byte("tenant-1"),
			},
			want: authConfig{authType: AuthTypeWorkloadIdentity, clientID: "client-1", tenantID: "tenant-1"},
		},
		{
			name: "marker not true falls back to PAT",
			data: map[string][]byte{"workloadIdentity": []byte("yes"), "token": []byte("pat-x")},
			want: authConfig{authType: AuthTypePAT, token: "pat-x"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := parseAuthConfig(v1.Secret{Data: tc.data})
			if got != tc.want {
				t.Fatalf("parseAuthConfig() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
