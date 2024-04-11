package github

import (
	"net/http"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v61/github"
)

func GetClient() *github.Client {
	// Wrap the shared transport for use with the integration ID 1 authenticating with installation ID 99.
	itr, _ := ghinstallation.NewKeyFromFile(http.DefaultTransport, 865488, 49029877, "/Users/zaller/Development/promoter/argoproj-promoter.2024-04-09.private-key.pem")
	client := github.NewClient(&http.Client{Transport: itr})
	return client
}

func GetEnterpriseClient(URL string) (*github.Client, error) {
	// Wrap the shared transport for use with the integration ID 1 authenticating with installation ID 99.
	itr, err := ghinstallation.NewKeyFromFile(http.DefaultTransport, 865488, 49029877, "/Users/zaller/Development/promoter/argoproj-promoter.2024-04-09.private-key.pem")
	if err != nil {
		return nil, err
	}

	client, err := github.NewClient(&http.Client{Transport: itr}).WithEnterpriseURLs("https://github.example.com/api/v3", "https://github.example.com/api/v3")
	if err != nil {
		return nil, err
	}

	return client, nil
}
