package azuredevops

import (
	"encoding/base64"
	"errors"
	"net/http"

	v1 "k8s.io/api/core/v1"
)

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
