package azuredevops

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7"
	v1 "k8s.io/api/core/v1"
)

const (
	// azureDevOpsTokenSecretKey is the key in the secret that contains the PAT token for Azure DevOps.
	azureDevOpsTokenSecretKey = "token"
)

// AuthType represents the authentication method
type AuthType int

const (
	// AuthTypePAT indicates Personal Access Token authentication
	AuthTypePAT AuthType = iota
	// AuthTypeWorkloadIdentity indicates Azure workload identity authentication
	AuthTypeWorkloadIdentity
)

// GitAuthenticationProvider provides methods to authenticate with Azure DevOps.
type GitAuthenticationProvider struct {
	scmProvider v1alpha1.GenericScmProvider
	cfg         authConfig
}

// NewAzdoGitAuthenticationProvider creates a new instance of GitAuthenticationProvider for Azure DevOps.
// It supports both Personal Access Token (PAT) and Azure Workload Identity authentication, selected by
// the contents of the secret.
func NewAzdoGitAuthenticationProvider(scmProvider v1alpha1.GenericScmProvider, secret *v1.Secret) GitAuthenticationProvider {
	return GitAuthenticationProvider{
		scmProvider: scmProvider,
		cfg:         parseAuthConfig(*secret),
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
func (azdo GitAuthenticationProvider) GetToken(ctx context.Context) (string, error) {
	switch azdo.cfg.authType {
	case AuthTypeWorkloadIdentity:
		return tokens.Token(ctx, azdo.cfg)
	case AuthTypePAT:
		if azdo.cfg.token == "" {
			return "", errors.New("azure DevOps Personal Access Token is empty - please check your secret configuration")
		}
		return azdo.cfg.token, nil
	default:
		return "", fmt.Errorf("unknown authentication type: %v - this should not happen", azdo.cfg.authType)
	}
}

// GetUser returns the user identifier for Azure DevOps authentication.
func (azdo GitAuthenticationProvider) GetUser(ctx context.Context) (string, error) {
	return "git", nil
}

// GetClient retrieves an Azure DevOps connection for the specified organization, using either PAT or
// Workload Identity authentication as configured in the secret.
func GetClient(ctx context.Context, scmProvider v1alpha1.GenericScmProvider, secret v1.Secret, org string) (*azuredevops.Connection, *GitAuthenticationProvider, error) {
	var organizationUrl string
	if scmProvider.GetSpec().AzureDevOps.Domain != "" {
		organizationUrl = fmt.Sprintf("https://%s/%s", scmProvider.GetSpec().AzureDevOps.Domain, scmProvider.GetSpec().AzureDevOps.Organization)
	} else {
		organizationUrl = "https://dev.azure.com/" + scmProvider.GetSpec().AzureDevOps.Organization
	}

	cfg := parseAuthConfig(secret)

	var connection *azuredevops.Connection
	switch cfg.authType {
	case AuthTypeWorkloadIdentity:
		token, err := tokens.Token(ctx, cfg)
		if err != nil {
			return nil, nil, err
		}
		connection = &azuredevops.Connection{
			AuthorizationString:     "Bearer " + token,
			BaseUrl:                 normalizeURL(organizationUrl),
			SuppressFedAuthRedirect: true,
		}
	case AuthTypePAT:
		if cfg.token == "" {
			return nil, nil, errors.New("azure DevOps token not found in secret")
		}
		connection = azuredevops.NewPatConnection(organizationUrl, cfg.token)
	default:
		return nil, nil, fmt.Errorf("unknown authentication type: %v - this should not happen", cfg.authType)
	}

	authProvider := GitAuthenticationProvider{scmProvider: scmProvider, cfg: cfg}
	return connection, &authProvider, nil
}

// normalizeURL lower-cases the URL and trims a trailing slash, matching the azure-devops-go-api
// SDK's internal normalization (which we bypass when constructing a bearer Connection directly).
func normalizeURL(u string) string {
	return strings.ToLower(strings.TrimRight(u, "/"))
}
