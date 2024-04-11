package github

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-github/v61/github"
)

func TestGetClient(t *testing.T) {
	client := GetClient()

	//client.PullRequests.Get(context.Background())

	newPR := &github.NewPullRequest{
		Title: github.String("My awesome pull request"),
		Head:  github.String("environment/development-next"),
		Base:  github.String("environment/development"),
		Body:  github.String("This is the description of the PR created with the package `github. com/ google/ go-github/ github`"),
	}

	pr, response, err := client.PullRequests.Create(context.Background(), "zachaller", "promoter-testing", newPR)
	fmt.Println(pr)
	fmt.Println(response)
	fmt.Println(err)

	pr.Body = github.String("update")

	pr, response, err = client.PullRequests.Edit(context.Background(), "zachaller", "promoter-testing", *pr.Number, pr)
	fmt.Println(pr)
	fmt.Println(response)
	fmt.Println(err)

	pr.State = github.String("closed")
	pr.Body = github.String("update closed")
	pr, response, err = client.PullRequests.Edit(context.Background(), "zachaller", "promoter-testing", *pr.Number, pr)
	fmt.Println(pr)
	fmt.Println(response)
	fmt.Println(err)
}
