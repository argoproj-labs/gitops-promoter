package azuredevops

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"

	v1 "k8s.io/api/core/v1"
)

// ApplyHTTPAuth applies Azure DevOps authentication to the HTTP request. In PAT mode it uses Basic
// auth with an empty username and the PAT as the password; in Workload Identity mode it sets a
// Bearer token obtained from Entra. The mode is selected by the secret contents.
func ApplyHTTPAuth(ctx context.Context, secret v1.Secret, req *http.Request) error {
	cfg := parseAuthConfig(secret)

	if cfg.authType == AuthTypeWorkloadIdentity {
		token, err := tokens.Token(ctx, cfg)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}

	if cfg.token == "" {
		return errors.New("azure DevOps Personal Access Token is empty - please check your secret configuration")
	}
	credentials := base64.StdEncoding.EncodeToString([]byte(":" + cfg.token))
	req.Header.Set("Authorization", "Basic "+credentials)
	return nil
}
