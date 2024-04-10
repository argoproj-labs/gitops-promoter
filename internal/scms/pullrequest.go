package scms

import "github.com/argoproj/promoter/api/v1alpha1"

type PullRequestProvider interface {
	Create(title, head, base, body string) (*v1alpha1.PullRequest, error)
	Close(id string) (*v1alpha1.PullRequest, error)
	Update(title, head, base, body string) (*v1alpha1.PullRequest, error)
}
