package azuredevops

import (
	"context"
	"fmt"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// azureDevOpsScope is the Microsoft Entra ID scope of the Azure DevOps resource (a fixed, well-known application ID).
const azureDevOpsScope = "499b84ac-1321-427f-aa17-267ca6975798/.default"

// Credentials are cached per identity so the token cache inside each credential survives across reconciles.
var (
	workloadIdentityCredentials   = map[string]azcore.TokenCredential{}
	workloadIdentityCredentialsMu sync.Mutex
)

// workloadIdentityTokenFunc returns a Microsoft Entra ID access token for Azure DevOps. It is a variable so tests can replace it.
var workloadIdentityTokenFunc = getWorkloadIdentityToken

func getWorkloadIdentityToken(ctx context.Context, wi *v1alpha1.AzureWorkloadIdentity) (string, error) {
	cred, err := workloadIdentityCredential(wi)
	if err != nil {
		return "", err
	}
	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{azureDevOpsScope}})
	if err != nil {
		return "", fmt.Errorf("failed to get Azure DevOps access token using workload identity: %w", err)
	}
	return token.Token, nil
}

func workloadIdentityCredential(wi *v1alpha1.AzureWorkloadIdentity) (azcore.TokenCredential, error) {
	workloadIdentityCredentialsMu.Lock()
	defer workloadIdentityCredentialsMu.Unlock()

	key := wi.TenantID + "/" + wi.ClientID
	if cred, ok := workloadIdentityCredentials[key]; ok {
		return cred, nil
	}
	cred, err := azidentity.NewWorkloadIdentityCredential(&azidentity.WorkloadIdentityCredentialOptions{
		ClientID: wi.ClientID,
		TenantID: wi.TenantID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure workload identity credential: %w", err)
	}
	workloadIdentityCredentials[key] = cred
	return cred, nil
}
