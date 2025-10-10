package azuredevops

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

const (
	// azureDevOpsTokenSecretKey is the key in the secret that contains the PAT token for Azure DevOps.
	azureDevOpsTokenSecretKey = "token"
	// azureDevOpsScope is the OAuth2 scope required for Azure DevOps API access
	azureDevOpsScope = "499b84ac-1321-427f-aa17-267ca6975798/.default"
	// defaultServiceAccountTokenPath is the default path to the service account token
	defaultServiceAccountTokenPath = "/var/run/secrets/azure/tokens/azure-identity-token"
)

// GitAuthenticationProvider provides methods to authenticate with Azure DevOps.
type GitAuthenticationProvider struct {
	scmProvider v1alpha1.GenericScmProvider
	// tokenManager manages workload identity tokens with automatic refresh
	tokenManager *TokenManager
	token        string
	// authType indicates the authentication method being used
	authType AuthType
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
// It supports both Personal Access Token (PAT) and Azure Workload Identity authentication methods.
// When workloadIdentity is configured, it will be used as the primary authentication method with PAT as fallback.
func NewAzdoGitAuthenticationProvider(ctx context.Context, k8sClient client.Client, scmProvider v1alpha1.GenericScmProvider, secret *v1.Secret, repoRef client.ObjectKey) GitAuthenticationProvider {
	logger := log.FromContext(ctx).WithName("azuredevops-auth")

	// Check if workload identity is configured and enabled
	workloadIdentity := scmProvider.GetSpec().AzureDevOps.WorkloadIdentity
	if workloadIdentity == nil || !workloadIdentity.Enabled {
		// Use PAT authentication
		logger.Info("Configuring Azure DevOps authentication with Personal Access Token")
		token := string(secret.Data[azureDevOpsTokenSecretKey])
		return GitAuthenticationProvider{
			scmProvider: scmProvider,
			token:       token,
			authType:    AuthTypePAT,
		}
	}

	logger.Info("Configuring Azure DevOps authentication with workload identity")

	// Create workload identity credential
	credential, err := createWorkloadIdentityCredential(workloadIdentity)
	if err != nil {
		logger.Error(err, "Failed to create workload identity credential, falling back to PAT authentication")

		// Fall back to PAT if workload identity fails and PAT is available
		token := string(secret.Data[azureDevOpsTokenSecretKey])
		if token == "" {
			logger.Error(errors.New("no PAT token available for fallback"), "Both workload identity and PAT authentication unavailable")
			// Return provider that will fail on token requests with clear error
			return GitAuthenticationProvider{
				scmProvider: scmProvider,
				authType:    AuthTypePAT,
				token:       "", // Empty token will cause authentication to fail with clear error
			}
		}

		logger.Info("Successfully configured PAT authentication as fallback")
		return GitAuthenticationProvider{
			scmProvider: scmProvider,
			token:       token,
			authType:    AuthTypePAT,
		}
	}

	logger.Info("Successfully configured workload identity authentication")
	return GitAuthenticationProvider{
		scmProvider:  scmProvider,
		tokenManager: NewTokenManager(credential),
		authType:     AuthTypeWorkloadIdentity,
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

// GetToken retrieves the authentication token for Azure DevOps.
// It handles both PAT and workload identity authentication methods transparently.
func (azdo GitAuthenticationProvider) GetToken(ctx context.Context) (string, error) {
	switch azdo.authType {
	case AuthTypeWorkloadIdentity:
		// Get access token using workload identity token manager
		if azdo.tokenManager == nil {
			return "", errors.New("workload identity token manager not initialized")
		}

		token, err := azdo.tokenManager.GetToken(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to get access token from Azure workload identity: %w", err)
		}
		return token, nil
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

// createWorkloadIdentityCredential creates an Azure workload identity credential
// It supports fallback to environment variables when config fields are not specified
func createWorkloadIdentityCredential(config *v1alpha1.AzureWorkloadIdentity) (azcore.TokenCredential, error) {
	// Get tenant ID from config or environment variable
	tenantID := config.TenantID
	if tenantID == "" {
		tenantID = os.Getenv("AZURE_TENANT_ID")
		if tenantID == "" {
			return nil, errors.New("tenantId not specified in config and AZURE_TENANT_ID environment variable not set")
		}
	}

	// Get client ID from config or environment variable
	clientID := config.ClientID
	if clientID == "" {
		clientID = os.Getenv("AZURE_CLIENT_ID")
		if clientID == "" {
			return nil, errors.New("clientId not specified in config and AZURE_CLIENT_ID environment variable not set")
		}
	}

	// Get service account token path from config, environment variable, or default
	tokenPath := config.ServiceAccountTokenPath
	if tokenPath == "" {
		tokenPath = os.Getenv("AZURE_FEDERATED_TOKEN_FILE")
		if tokenPath == "" {
			tokenPath = defaultServiceAccountTokenPath
		}
	}

	// Check if the token file exists
	if _, err := os.Stat(tokenPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("service account token file not found at %s (configure serviceAccountTokenPath or set AZURE_FEDERATED_TOKEN_FILE)", tokenPath)
	}

	// Create workload identity credential
	options := &azidentity.WorkloadIdentityCredentialOptions{
		TenantID:      tenantID,
		ClientID:      clientID,
		TokenFilePath: tokenPath,
	}

	credential, err := azidentity.NewWorkloadIdentityCredential(options)
	if err != nil {
		return nil, fmt.Errorf("failed to create workload identity credential: %w", err)
	}

	return credential, nil
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

	// Check if workload identity is configured and enabled
	if scmProvider.GetSpec().AzureDevOps.WorkloadIdentity != nil && scmProvider.GetSpec().AzureDevOps.WorkloadIdentity.Enabled {
		connection, authProvider, err = createWorkloadIdentityConnection(ctx, scmProvider, secret, organizationUrl)
		if err != nil {
			return nil, nil, err
		}
	} else {
		connection, authProvider, err = createPATConnection(scmProvider, secret, organizationUrl)
		if err != nil {
			return nil, nil, err
		}
	}

	return connection, &authProvider, nil
}

// createWorkloadIdentityConnection creates an Azure DevOps connection using workload identity
func createWorkloadIdentityConnection(ctx context.Context, scmProvider v1alpha1.GenericScmProvider, secret v1.Secret, organizationUrl string) (*azuredevops.Connection, GitAuthenticationProvider, error) {
	// Create workload identity credential
	credential, err := createWorkloadIdentityCredential(scmProvider.GetSpec().AzureDevOps.WorkloadIdentity)
	if err != nil {
		// Fall back to PAT authentication
		token := string(secret.Data[azureDevOpsTokenSecretKey])
		if token == "" {
			return nil, GitAuthenticationProvider{}, fmt.Errorf("both workload identity and PAT authentication failed: workload identity error: %w, PAT token not found in secret", err)
		}
		connection := azuredevops.NewPatConnection(organizationUrl, token)
		authProvider := GitAuthenticationProvider{
			scmProvider: scmProvider,
			token:       token,
			authType:    AuthTypePAT,
		}
		return connection, authProvider, nil
	}

	// Get initial access token for connection
	tokenResult, err := credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{azureDevOpsScope},
	})
	if err != nil {
		return nil, GitAuthenticationProvider{}, fmt.Errorf("failed to get initial access token from workload identity: %w", err)
	}

	// Create connection using the access token
	connection := azuredevops.NewPatConnection(organizationUrl, tokenResult.Token)
	authProvider := GitAuthenticationProvider{
		scmProvider:  scmProvider,
		tokenManager: NewTokenManager(credential),
		authType:     AuthTypeWorkloadIdentity,
	}
	return connection, authProvider, nil
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
