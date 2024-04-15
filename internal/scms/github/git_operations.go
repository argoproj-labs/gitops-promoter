package github

import (
	"context"
	"fmt"
	"github.com/argoproj/promoter/api/v1alpha1"
	"net/http"
	"strconv"

	v1 "k8s.io/api/core/v1"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v61/github"
)

type GitAuthenticationProvider struct {
	scmProvider *v1alpha1.ScmProvider
	secret      *v1.Secret
}

func NewGithubGitAuthenticationProvider(scmProvider *v1alpha1.ScmProvider, secret *v1.Secret) GitAuthenticationProvider {
	return GitAuthenticationProvider{
		scmProvider: scmProvider,
		secret:      secret,
	}
}

func (gh GitAuthenticationProvider) GetGitHttpsRepoUrl(repoRef v1alpha1.RepositoryRef) string {
	if gh.scmProvider.Spec.GitHub != nil && gh.scmProvider.Spec.GitHub.Domain == "" {
		return fmt.Sprintf("https://github.com/%s/%s.git", repoRef.Owner, repoRef.Name)
	}
	return fmt.Sprintf("https://%s/%s/%s.git", gh.scmProvider.Spec.GitHub.Domain, repoRef.Owner, repoRef.Name)
}

func (gh GitAuthenticationProvider) GetToken(ctx context.Context) (string, error) {
	return GetToken(ctx, *gh.secret)
}

func GetClient(secret v1.Secret) (*github.Client, error) {

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
	client := github.NewClient(&http.Client{Transport: itr})
	return client, nil
}

func GetToken(ctx context.Context, secret v1.Secret) (string, error) {

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
		return "", err
	}
	return itr.Token(ctx)
}

//func GetEnterpriseClient(secret v1.Secret) (*github.Client, error) {
//
//	appID, err := strconv.ParseInt(string(secret.Data["appID"]), 10, 64)
//	if err != nil {
//		panic(err)
//	}
//
//	installationID, err := strconv.ParseInt(string(secret.Data["installationID"]), 10, 64)
//	if err != nil {
//		panic(err)
//	}
//
//	itr, _ := ghinstallation.New(http.DefaultTransport, appID, installationID, secret.Data["privateKey"])
//
//	client, err := github.NewClient(&http.Client{Transport: itr}).WithEnterpriseURLs("https://github.example.com/api/v3", "https://github.example.com/api/v3")
//	if err != nil {
//		return nil, err
//	}
//
//	return client, nil
//}
