package azuredevops

import (
	"errors"
	"net/http"
	"testing"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
)

func TestAzureDevOpsHTTPStatusCode(t *testing.T) {
	t.Parallel()

	notFound := http.StatusNotFound
	serverError := http.StatusInternalServerError

	tests := []struct {
		err      error
		name     string
		wantCode int
		wantOK   bool
		want404  bool
	}{
		{
			name:   "nil error",
			err:    nil,
			wantOK: false,
		},
		{
			name:     "pointer wrapped error",
			err:      &azuredevops.WrappedError{StatusCode: &notFound},
			wantCode: http.StatusNotFound,
			wantOK:   true,
			want404:  true,
		},
		{
			name:     "value wrapped error",
			err:      azuredevops.WrappedError{StatusCode: &serverError},
			wantCode: http.StatusInternalServerError,
			wantOK:   true,
		},
		{
			name:   "unrelated error",
			err:    errors.New("network timeout"),
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotCode, gotOK := azureDevOpsHTTPStatusCode(tt.err)
			if gotOK != tt.wantOK || gotCode != tt.wantCode {
				t.Fatalf("azureDevOpsHTTPStatusCode() = (%d, %v), want (%d, %v)", gotCode, gotOK, tt.wantCode, tt.wantOK)
			}
			if isAzureDevOpsNotFound(tt.err) != tt.want404 {
				t.Fatalf("isAzureDevOpsNotFound() = %v, want %v", isAzureDevOpsNotFound(tt.err), tt.want404)
			}
		})
	}
}
