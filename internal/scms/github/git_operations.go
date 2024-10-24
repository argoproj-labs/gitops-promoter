package github

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"

	v1 "k8s.io/api/core/v1"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v61/github"
)

type GitAuthenticationProvider struct {
	scmProvider *v1alpha1.ScmProvider
	secret      *v1.Secret
	transport   *ghinstallation.Transport
}

func NewGithubGitAuthenticationProvider(scmProvider *v1alpha1.ScmProvider, secret *v1.Secret) GitAuthenticationProvider {
	appID, err := strconv.ParseInt(string(secret.Data["appID"]), 10, 64)
	if err != nil {
		panic(err)
	}

	installationID, err := strconv.ParseInt(string(secret.Data["installationID"]), 10, 64)
	if err != nil {
		panic(err)
	}

	itr, err := ghinstallation.New(http.DefaultTransport, appID, installationID, secret.Data["privateKey"])
	if err != nil {
		panic(err)
	}

	return GitAuthenticationProvider{
		scmProvider: scmProvider,
		secret:      secret,
		transport:   itr,
	}
}

func (gh GitAuthenticationProvider) GetGitHttpsRepoUrl(gitRepository v1alpha1.GitRepository) string {
	if gh.scmProvider.Spec.GitHub != nil && gh.scmProvider.Spec.GitHub.Domain == "" {
		return fmt.Sprintf("https://git@github.com/%s/%s.git", gitRepository.Spec.Owner, gitRepository.Spec.Name)
	}
	return fmt.Sprintf("https://git@%s/%s/%s.git", gh.scmProvider.Spec.GitHub.Domain, gitRepository.Spec.Owner, gitRepository.Spec.Name)
}

func (gh GitAuthenticationProvider) GetToken(ctx context.Context) (string, error) {
	return gh.transport.Token(ctx)
}

func (gh GitAuthenticationProvider) GetUser(ctx context.Context) (string, error) {
	return "git", nil
}

func GetClient(secret v1.Secret, domain string) (*github.Client, error) {

	appID, err := strconv.ParseInt(string(secret.Data["appID"]), 10, 64)
	if err != nil {
		panic(err)
	}

	installationID, err := strconv.ParseInt(string(secret.Data["installationID"]), 10, 64)
	if err != nil {
		panic(err)
	}

	itr, err := ghinstallation.New(http.DefaultTransport, appID, installationID, secret.Data["privateKey"])
	if err != nil {
		return nil, err
	}

	var client *github.Client
	if domain == "" || domain == "github.com" {
		client = github.NewClient(&http.Client{Transport: itr})
	} else {
		baseURL := fmt.Sprintf("https://%s/api/v3", domain)
		uploadsURL := fmt.Sprintf("https://%s/api/uploads", domain)
		client, err = github.NewClient(&http.Client{Transport: itr}).WithEnterpriseURLs(baseURL, uploadsURL)
		if err != nil {
			return nil, err
		}
	}

	return client, nil
}
