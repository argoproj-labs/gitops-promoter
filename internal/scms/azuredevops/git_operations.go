package azuredevops

import (
	"context"
	"errors"
	"fmt"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	v1 "k8s.io/api/core/v1"
)

const (
	// azureDevOpsTokenSecretKey is the key in the secret that contains the PAT token for Azure DevOps.
	azureDevOpsTokenSecretKey = "token"
	// azureDevOpsScope is the OAuth2 scope required for Azure DevOps API access
	// This guid is fixed for Azure DevOps and should not be changed, see https://learn.microsoft.com/en-us/rest/api/azure/devops/tokens
	azureDevOpsScope = "499b84ac-1321-427f-aa17-267ca6975798/.default"
)

// GitAuthenticationProvider provides methods to authenticate with Azure DevOps.
type GitAuthenticationProvider struct {
	scmProvider v1alpha1.GenericScmProvider
	token       string
	authType    AuthType
}

// AuthType represents the authentication method
type AuthType int

const (
	// AuthTypePAT indicates Personal Access Token authentication
	AuthTypePAT AuthType = iota
	// AuthTypeWorkloadIdentity indicates Azure workload identity authentication
	AuthTypeWorkloadIdentity
)

// NewAzdoGitAuthenticationProvider creates a new instance of GitAuthenticationProvider for Azure DevOps.
// It supports both Personal Access Token (PAT)
func NewAzdoGitAuthenticationProvider(scmProvider v1alpha1.GenericScmProvider, secret *v1.Secret) GitAuthenticationProvider {
	token := string(secret.Data[azureDevOpsTokenSecretKey])

	return GitAuthenticationProvider{
		scmProvider: scmProvider,
		token:       token,
		authType:    AuthTypePAT,
	}
}

// GetGitHttpsRepoUrl constructs the HTTPS URL for an Azure DevOps repository.
func (azdo GitAuthenticationProvider) GetGitHttpsRepoUrl(gitRepository v1alpha1.GitRepository) string {
	if azdo.scmProvider.GetSpec().AzureDevOps != nil && azdo.scmProvider.GetSpec().AzureDevOps.Domain != "" {
		return fmt.Sprintf("https://%s/%s/%s/_git/%s",
			azdo.scmProvider.GetSpec().AzureDevOps.Domain,
			azdo.scmProvider.GetSpec().AzureDevOps.Organization,
			gitRepository.Spec.AzureDevOps.Project,
			gitRepository.Spec.AzureDevOps.Name)
	}
	return fmt.Sprintf("https://dev.azure.com/%s/%s/_git/%s",
		azdo.scmProvider.GetSpec().AzureDevOps.Organization,
		gitRepository.Spec.AzureDevOps.Project,
		gitRepository.Spec.AzureDevOps.Name)
}

// GetToken retrieves the authentication token for Azure DevOps
func (azdo GitAuthenticationProvider) GetToken(ctx context.Context) (string, error) {
	switch azdo.authType {
	case AuthTypeWorkloadIdentity:
		// Get access token using workload identity token manager
		return "AuthTypeWorkloadIdentity not implemented", nil
	case AuthTypePAT:
		// Return the stored PAT token
		if azdo.token == "" {
			return "", errors.New("azure DevOps Personal Access Token is empty - please check your secret configuration")
		}
		return azdo.token, nil
	default:
		return "", fmt.Errorf("unknown authentication type: %v - this should not happen", azdo.authType)
	}
}

// GetUser returns the user identifier for Azure DevOps authentication.
func (azdo GitAuthenticationProvider) GetUser(ctx context.Context) (string, error) {
	return "git", nil
}

// GetClient retrieves an Azure DevOps connection for the specified organization.
func GetClient(ctx context.Context, scmProvider v1alpha1.GenericScmProvider, secret v1.Secret, org string) (*azuredevops.Connection, *GitAuthenticationProvider, error) {
	// Create the organization URL
	var organizationUrl string
	if scmProvider.GetSpec().AzureDevOps.Domain != "" {
		organizationUrl = fmt.Sprintf("https://%s/%s", scmProvider.GetSpec().AzureDevOps.Domain, scmProvider.GetSpec().AzureDevOps.Organization)
	} else {
		organizationUrl = "https://dev.azure.com/" + scmProvider.GetSpec().AzureDevOps.Organization
	}

	var connection *azuredevops.Connection
	var authProvider GitAuthenticationProvider
	var err error

	connection, authProvider, err = createPATConnection(scmProvider, secret, organizationUrl)
	if err != nil {
		return nil, nil, err
	}

	return connection, &authProvider, nil
}

// createPATConnection creates an Azure DevOps connection using PAT authentication
func createPATConnection(scmProvider v1alpha1.GenericScmProvider, secret v1.Secret, organizationUrl string) (*azuredevops.Connection, GitAuthenticationProvider, error) {
	// Use PAT authentication
	token := string(secret.Data[azureDevOpsTokenSecretKey])
	if token == "" {
		return nil, GitAuthenticationProvider{}, errors.New("azure DevOps token not found in secret")
	}
	connection := azuredevops.NewPatConnection(organizationUrl, token)
	authProvider := GitAuthenticationProvider{
		scmProvider: scmProvider,
		token:       token,
		authType:    AuthTypePAT,
	}
	return connection, authProvider, nil
}
