package github

import (
	"context"
	"net/http"
	"strconv"

	v1 "k8s.io/api/core/v1"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v61/github"
)

type GitAuthenticationProvider struct {
	secret *v1.Secret
}

func NewGithubGitAuthenticationProvider(secret *v1.Secret) GitAuthenticationProvider {
	return GitAuthenticationProvider{
		secret: secret,
	}
}

func (gh GitAuthenticationProvider) GetGitAuthentication(ctx context.Context) (username string, password string, err error) {
	appID, err := strconv.ParseInt(string(gh.secret.Data["appID"]), 10, 64)
	if err != nil {
		panic(err)
	}

	installationID, err := strconv.ParseInt(string(gh.secret.Data["installationID"]), 10, 64)
	if err != nil {
		panic(err)
	}

	itr, _ := ghinstallation.New(http.DefaultTransport, appID, installationID, gh.secret.Data["privateKey"])
	token, err := itr.Token(ctx)
	if err != nil {
		return "", "", err
	}

	return "git", token, nil
}

func (gh GitAuthenticationProvider) GetClient() *github.Client {

	appID, err := strconv.ParseInt(string(gh.secret.Data["appID"]), 10, 64)
	if err != nil {
		panic(err)
	}

	installationID, err := strconv.ParseInt(string(gh.secret.Data["installationID"]), 10, 64)
	if err != nil {
		panic(err)
	}

	itr, _ := ghinstallation.New(http.DefaultTransport, appID, installationID, gh.secret.Data["privateKey"])
	client := github.NewClient(&http.Client{Transport: itr})
	return client
}

func GetClient(secret v1.Secret) *github.Client {

	appID, err := strconv.ParseInt(string(secret.Data["appID"]), 10, 64)
	if err != nil {
		panic(err)
	}

	installationID, err := strconv.ParseInt(string(secret.Data["installationID"]), 10, 64)
	if err != nil {
		panic(err)
	}

	itr, _ := ghinstallation.New(http.DefaultTransport, appID, installationID, secret.Data["privateKey"])
	client := github.NewClient(&http.Client{Transport: itr})
	return client
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
