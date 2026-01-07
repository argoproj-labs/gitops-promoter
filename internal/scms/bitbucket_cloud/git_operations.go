package bitbucket_cloud

import (
	"context"
	"fmt"
	"net/url"

	"github.com/ktrysmt/go-bitbucket"
	v1 "k8s.io/api/core/v1"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
)

// GitAuthenticationProvider implements the scms.GitOperationsProvider interface for Bitbucket Cloud.
type GitAuthenticationProvider struct {
	scmProvider v1alpha1.GenericScmProvider
	secret      *v1.Secret
	client      *bitbucket.Client
}

var _ scms.GitOperationsProvider = &GitAuthenticationProvider{}

// NewBitbucketCloudGitAuthenticationProvider creates a new instance of GitAuthenticationProvider for Bitbucket Cloud.
func NewBitbucketCloudGitAuthenticationProvider(scmProvider v1alpha1.GenericScmProvider, secret *v1.Secret) (*GitAuthenticationProvider, error) {
	client, err := GetClient(*secret)
	if err != nil {
		return nil, fmt.Errorf("failed to create Bitbucket Cloud Client: %w", err)
	}

	return &GitAuthenticationProvider{
		scmProvider: scmProvider,
		secret:      secret,
		client:      client,
	}, nil
}

// GetGitHttpsRepoUrl constructs the HTTPS URL for a Bitbucket Cloud repository based on the provided GitRepository object.
func (GitAuthenticationProvider) GetGitHttpsRepoUrl(repo v1alpha1.GitRepository) string {
	repoUrl := fmt.Sprintf("%s/%s/%s.git", BitbucketBaseURL, repo.Spec.BitbucketCloud.Owner, repo.Spec.BitbucketCloud.Name)
	if _, err := url.Parse(repoUrl); err != nil {
		return ""
	}
	return repoUrl
}

// GetToken retrieves the Bitbucket Cloud access token from the secret.
func (bb GitAuthenticationProvider) GetToken(ctx context.Context) (string, error) {
	return string(bb.secret.Data["token"]), nil
}

// GetUser returns a placeholder user for Bitbucket Cloud authentication.
func (GitAuthenticationProvider) GetUser(ctx context.Context) (string, error) {
	return "x-token-auth", nil
}

// GetClient creates a new Bitbucket Cloud client using the provided secret.
func GetClient(secret v1.Secret) (*bitbucket.Client, error) {
	token := string(secret.Data["token"])
	if token == "" {
		return nil, fmt.Errorf("secret %q is missing required data key 'token'", secret.Name)
	}

	client, err := bitbucket.NewOAuthbearerToken(token)
	if err != nil {
		return nil, fmt.Errorf("failed to create Bitbucket client: %w", err)
	}

	return client, nil
}
