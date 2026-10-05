package bitbucket_datacenter

import (
	"context"
	"fmt"
	"net/url"

	corev1 "k8s.io/api/core/v1"

	v1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
)

// GitAuthenticationProvider implements the scms.GitOperationsProvider interface for Bitbucket DataCenter/Server.
type GitAuthenticationProvider struct {
	scmProvider v1alpha1.GenericScmProvider
	secret      *corev1.Secret
}

var (
	_ scms.GitOperationsProvider = &GitAuthenticationProvider{}
	_ scms.GitHTTPHeaderProvider = &GitAuthenticationProvider{}
)

// NewBitbucketDataCenterGitAuthenticationProvider creates a new instance of GitAuthenticationProvider
// for Bitbucket DataCenter/Server.
func NewBitbucketDataCenterGitAuthenticationProvider(scmProvider v1alpha1.GenericScmProvider, secret *corev1.Secret) (*GitAuthenticationProvider, error) {
	if scmProvider.GetSpec().BitbucketDataCenter == nil {
		return nil, fmt.Errorf("scmProvider %q does not have BitbucketDataCenter configuration", scmProvider.GetName())
	}
	return &GitAuthenticationProvider{
		scmProvider: scmProvider,
		secret:      secret,
	}, nil
}

// GetGitHttpsRepoUrl constructs the HTTPS URL for a Bitbucket DataCenter/Server repository.
func (g GitAuthenticationProvider) GetGitHttpsRepoUrl(repo v1alpha1.GitRepository) string {
	domain := g.scmProvider.GetSpec().BitbucketDataCenter.Domain
	project := repo.Spec.BitbucketDataCenter.Project
	name := repo.Spec.BitbucketDataCenter.Name
	repoURL := fmt.Sprintf("https://%s/scm/%s/%s.git", domain, project, name)
	if _, err := url.Parse(repoURL); err != nil {
		return ""
	}
	return repoURL
}

// GetToken retrieves the authentication token from the secret.
// If the secret has a "token" key it is returned; otherwise the password is returned for
// use as the HTTP Basic auth password. Returns an error when neither credential is present.
func (g GitAuthenticationProvider) GetToken(ctx context.Context) (string, error) {
	if token := string(g.secret.Data["token"]); token != "" {
		return token, nil
	}
	password := string(g.secret.Data["password"])
	if password == "" {
		return "", fmt.Errorf("secret %q must contain either 'token' or both 'username' and 'password'", g.secret.Name)
	}
	return password, nil
}

// GetUser returns the username for Bitbucket DataCenter/Server authentication.
// When a token is present git authenticates with the Bearer header from GetGitHTTPHeader, so the
// returned "x-token-auth" is only a placeholder; otherwise the username from the secret is returned.
// Returns an error when neither credential is present.
func (g GitAuthenticationProvider) GetUser(ctx context.Context) (string, error) {
	if string(g.secret.Data["token"]) != "" {
		return "x-token-auth", nil
	}
	username := string(g.secret.Data["username"])
	if username == "" {
		return "", fmt.Errorf("secret %q must contain either 'token' or both 'username' and 'password'", g.secret.Name)
	}
	return username, nil
}

// GetGitHTTPHeader returns a Bearer authorization header scoped to the Bitbucket DataCenter/Server domain when the
// secret holds a token. Bitbucket DataCenter/Server rejects a token in HTTP Basic auth unless it is paired with the
// username that owns it, which the secret does not carry.
func (g GitAuthenticationProvider) GetGitHTTPHeader(ctx context.Context) (string, string, error) {
	token := string(g.secret.Data["token"])
	if token == "" {
		return "", "", nil
	}
	return "https://" + g.scmProvider.GetSpec().BitbucketDataCenter.Domain + "/", "Authorization: Bearer " + token, nil
}
