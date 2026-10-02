package azuredevops

import (
	"encoding/base64"
	"errors"
	"net/http"

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	v1 "k8s.io/api/core/v1"
)

// azureDevOpsHTTPStatusCode extracts the HTTP status code from an Azure DevOps REST API error.
// The SDK wraps failed responses in azuredevops.WrappedError with StatusCode set.
func azureDevOpsHTTPStatusCode(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	if wrapped, ok := errors.AsType[*azuredevops.WrappedError](err); ok && wrapped.StatusCode != nil {
		return *wrapped.StatusCode, true
	}
	if wrapped, ok := errors.AsType[azuredevops.WrappedError](err); ok && wrapped.StatusCode != nil {
		return *wrapped.StatusCode, true
	}
	return 0, false
}

func isAzureDevOpsNotFound(err error) bool {
	code, ok := azureDevOpsHTTPStatusCode(err)
	return ok && code == http.StatusNotFound
}

// ApplyHTTPAuth applies Azure DevOps authentication to the HTTP request using Basic auth
// with an empty username and the PAT as the password.
func ApplyHTTPAuth(secret v1.Secret, req *http.Request) error {
	token := string(secret.Data[azureDevOpsTokenSecretKey])
	if token == "" {
		return errors.New("non-empty token required in secret for Azure DevOps SCM auth")
	}
	credentials := base64.StdEncoding.EncodeToString([]byte(":" + token))
	req.Header.Set("Authorization", "Basic "+credentials)
	return nil
}
