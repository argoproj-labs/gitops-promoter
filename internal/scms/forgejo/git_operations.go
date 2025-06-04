package forgejo

import (
	"context"
	"fmt"
	"net/url"

	forgejo "codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v2"
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	k8sV1 "k8s.io/api/core/v1"
)

type GitAuthenticationProvider struct {
	scmProvider promoterv1alpha1.GenericScmProvider
	secret      *k8sV1.Secret
	client      *forgejo.Client
}

func NewForgejoGitAuthenticationProvider(scmProvider promoterv1alpha1.GenericScmProvider, secret *k8sV1.Secret) *GitAuthenticationProvider {
	client, err := GetClient(scmProvider.GetSpec().Forgejo.Domain, *secret)
	if err != nil {
		panic(err)
	}

	return &GitAuthenticationProvider{
		scmProvider: scmProvider,
		secret:      secret,
		client:      client,
	}
}

func (gap GitAuthenticationProvider) GetGitHttpsRepoUrl(repo promoterv1alpha1.GitRepository) string {
	repoUrl := fmt.Sprintf(
		"https://%s/%s/%s.git",
		gap.scmProvider.GetSpec().Forgejo.Domain,
		repo.Spec.Forgejo.Owner,
		repo.Spec.Forgejo.Name,
	)
	if _, err := url.Parse(repoUrl); err != nil {
		return ""
	}
	return repoUrl
}

func (gap GitAuthenticationProvider) GetToken(ctx context.Context) (string, error) {
	return string(gap.secret.Data["token"]), nil
}

func (gap GitAuthenticationProvider) GetUser(ctx context.Context) (string, error) {
	return "oauth2", nil
}

func GetClient(domain string, secret k8sV1.Secret) (*forgejo.Client, error) {
	options := make([]forgejo.ClientOption, 0)

	token := string(secret.Data["token"])
	if token != "" {
		options = append(options, forgejo.SetToken(token))
	}

	basicAuthUser := string(secret.Data["user"])
	basicAuthPassword := string(secret.Data["password"])
	if basicAuthUser != "" && basicAuthPassword != "" {
		options = append(options, forgejo.SetBasicAuth(basicAuthUser, basicAuthPassword))
	}

	client, err := forgejo.NewClient(
		"https://"+domain,
		options...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Forgejo client: %w", err)
	}

	return client, nil
}
