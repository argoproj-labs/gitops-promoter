package azuredevops

import (
	"context"
	"errors"
	"fmt"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	v1 "k8s.io/api/core/v1"
)

const (
	// azureDevOpsTokenSecretKey is the key in the secret that contains the PAT token for Azure DevOps.
	azureDevOpsTokenSecretKey = "token"
)

var _ scms.GitHTTPHeaderProvider = GitAuthenticationProvider{}

// GitAuthenticationProvider provides methods to authenticate with Azure DevOps.
type GitAuthenticationProvider struct {
	scmProvider      v1alpha1.GenericScmProvider
	workloadIdentity *v1alpha1.AzureWorkloadIdentity
	token            string
	authType         AuthType
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
// It supports both Personal Access Token (PAT) and Azure workload identity authentication.
func NewAzdoGitAuthenticationProvider(scmProvider v1alpha1.GenericScmProvider, secret *v1.Secret) (GitAuthenticationProvider, error) {
	if wi := workloadIdentityConfig(scmProvider); wi != nil {
		return GitAuthenticationProvider{
			scmProvider:      scmProvider,
			authType:         AuthTypeWorkloadIdentity,
			workloadIdentity: wi,
		}, nil
	}

	return GitAuthenticationProvider{
		scmProvider: scmProvider,
		token:       string(secret.Data[azureDevOpsTokenSecretKey]),
		authType:    AuthTypePAT,
	}, nil
}

// workloadIdentityConfig returns the workload identity configuration of the provider, or nil when it uses a PAT.
// Always nil for an ScmProvider, whose spec has no workloadIdentity field.
func workloadIdentityConfig(scmProvider v1alpha1.GenericScmProvider) *v1alpha1.AzureWorkloadIdentity {
	if azdo := scmProvider.GetSpec().AzureDevOps; azdo != nil {
		return azdo.WorkloadIdentity
	}
	return nil
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
		return workloadIdentityTokenFunc(ctx, azdo.workloadIdentity)
	case AuthTypePAT:
		if azdo.token == "" {
			return "", errors.New("azure DevOps Personal Access Token is empty - please check your secret configuration")
		}
		return azdo.token, nil
	default:
		return "", fmt.Errorf("unknown authentication type: %v - this should not happen", azdo.authType)
	}
}

// GetGitHTTPHeader returns a Bearer authorization header scoped to the Azure DevOps domain when using workload
// identity. Azure DevOps expects Microsoft Entra ID access tokens as a Bearer header rather than a Basic auth password.
func (azdo GitAuthenticationProvider) GetGitHTTPHeader(ctx context.Context) (string, string, error) {
	if azdo.authType != AuthTypeWorkloadIdentity {
		return "", "", nil
	}
	token, err := workloadIdentityTokenFunc(ctx, azdo.workloadIdentity)
	if err != nil {
		return "", "", err
	}
	domain := azureDevopsDomain
	if d := azdo.scmProvider.GetSpec().AzureDevOps.Domain; d != "" {
		domain = d
	}
	return "https://" + domain + "/", "Authorization: Bearer " + token, nil
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

	if wi := workloadIdentityConfig(scmProvider); wi != nil {
		connection, authProvider, err = createWorkloadIdentityConnection(ctx, scmProvider, wi, organizationUrl)
	} else {
		connection, authProvider, err = createPATConnection(scmProvider, secret, organizationUrl)
	}
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

// createWorkloadIdentityConnection creates an Azure DevOps connection authenticated with a Microsoft Entra ID
// access token obtained through Azure workload identity.
func createWorkloadIdentityConnection(ctx context.Context, scmProvider v1alpha1.GenericScmProvider, wi *v1alpha1.AzureWorkloadIdentity, organizationUrl string) (*azuredevops.Connection, GitAuthenticationProvider, error) {
	token, err := workloadIdentityTokenFunc(ctx, wi)
	if err != nil {
		return nil, GitAuthenticationProvider{}, err
	}
	connection := azuredevops.NewAnonymousConnection(organizationUrl)
	connection.AuthorizationString = "Bearer " + token
	authProvider := GitAuthenticationProvider{
		scmProvider:      scmProvider,
		authType:         AuthTypeWorkloadIdentity,
		workloadIdentity: wi,
	}
	return connection, authProvider, nil
}
