package azuredevops

import (
	v1 "k8s.io/api/core/v1"
)

const (
	// workloadIdentitySecretKey, when set to "true", selects Azure Workload Identity auth.
	workloadIdentitySecretKey = "workloadIdentity"
	// azureClientIDSecretKey optionally overrides the injected AZURE_CLIENT_ID.
	azureClientIDSecretKey = "azureClientID"
	// azureTenantIDSecretKey optionally overrides the injected AZURE_TENANT_ID.
	azureTenantIDSecretKey = "azureTenantID"
)

// authConfig holds the Azure DevOps authentication parameters parsed from a secret.
type authConfig struct {
	token    string
	clientID string
	tenantID string
	authType AuthType
}

// parseAuthConfig reads the Azure DevOps auth configuration from the secret. It selects Workload
// Identity when the "workloadIdentity" key equals "true"; otherwise it uses PAT authentication.
func parseAuthConfig(secret v1.Secret) authConfig {
	if string(secret.Data[workloadIdentitySecretKey]) == "true" {
		return authConfig{
			authType: AuthTypeWorkloadIdentity,
			clientID: string(secret.Data[azureClientIDSecretKey]),
			tenantID: string(secret.Data[azureTenantIDSecretKey]),
		}
	}
	return authConfig{
		authType: AuthTypePAT,
		token:    string(secret.Data[azureDevOpsTokenSecretKey]),
	}
}
