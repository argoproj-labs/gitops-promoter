package webhookreceiver

import (
	"context"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// PullRequestEvent is a pull request webhook (opened, closed, reopened, merged) reduced to what the receiver needs:
// the provider's pull request identifier and the repository it belongs to.
type PullRequestEvent struct {
	// ID is the provider's pull request identifier, as the PullRequest resource records it in status.id.
	ID string
	// Action is the provider's own name for what happened, for logging.
	Action string
	// Repo identifies the repository in the provider's terms.
	Repo RepoIdentity
}

// RepoIdentity identifies a repository the way the provider's webhook payload names it. Which fields are set depends
// on the provider.
type RepoIdentity struct {
	// FullName is "owner/name" (GitHub, Gitea, Forgejo, Bitbucket Cloud) or "namespace/name" (GitLab).
	FullName string
	// Project is the Azure DevOps project name.
	Project string
	// Name is the Azure DevOps repository name.
	Name string
	// ProjectID is the GitLab project ID, when the payload carries one.
	ProjectID int64
}

// ParsePullRequestEvent recognizes a pull request webhook of the detected provider and extracts the pull request's
// identity. It returns false for every other event, push events included. Gitea and Forgejo send GitHub's headers
// too and are detected as GitHub; their pull request payloads use GitHub's shape for the fields read here.
func ParsePullRequestEvent(provider string, header http.Header, body []byte) (PullRequestEvent, bool) {
	switch provider {
	case ProviderGitHub, ProviderGitea, ProviderForgejo:
		event := header.Get("X-Github-Event")
		if event == "" {
			event = header.Get("X-Gitea-Event")
		}
		if event == "" {
			event = header.Get("X-Forgejo-Event")
		}
		if event != "pull_request" {
			return PullRequestEvent{}, false
		}
		number := gjson.GetBytes(body, "pull_request.number")
		if !number.Exists() {
			number = gjson.GetBytes(body, "number")
		}
		if !number.Exists() {
			return PullRequestEvent{}, false
		}
		return PullRequestEvent{
			ID:     number.String(),
			Action: gjson.GetBytes(body, "action").String(),
			Repo:   RepoIdentity{FullName: gjson.GetBytes(body, "repository.full_name").String()},
		}, true
	case ProviderGitLab:
		if header.Get("X-Gitlab-Event") != "Merge Request Hook" && gjson.GetBytes(body, "object_kind").String() != "merge_request" {
			return PullRequestEvent{}, false
		}
		iid := gjson.GetBytes(body, "object_attributes.iid")
		if !iid.Exists() {
			return PullRequestEvent{}, false
		}
		return PullRequestEvent{
			ID:     iid.String(),
			Action: gjson.GetBytes(body, "object_attributes.action").String(),
			Repo: RepoIdentity{
				FullName:  gjson.GetBytes(body, "project.path_with_namespace").String(),
				ProjectID: gjson.GetBytes(body, "project.id").Int(),
			},
		}, true
	case ProviderAzureDevops:
		eventType := gjson.GetBytes(body, "eventType").String()
		if !strings.HasPrefix(eventType, "git.pullrequest.") {
			return PullRequestEvent{}, false
		}
		id := gjson.GetBytes(body, "resource.pullRequestId")
		if !id.Exists() {
			return PullRequestEvent{}, false
		}
		return PullRequestEvent{
			ID:     id.String(),
			Action: eventType,
			Repo: RepoIdentity{
				Name:    gjson.GetBytes(body, "resource.repository.name").String(),
				Project: gjson.GetBytes(body, "resource.repository.project.name").String(),
			},
		}, true
	case ProviderBitbucketCloud:
		eventKey := header.Get("X-Event-Key")
		if !strings.HasPrefix(eventKey, "pullrequest:") {
			return PullRequestEvent{}, false
		}
		id := gjson.GetBytes(body, "pullrequest.id")
		if !id.Exists() {
			return PullRequestEvent{}, false
		}
		return PullRequestEvent{
			ID:     id.String(),
			Action: eventKey,
			Repo:   RepoIdentity{FullName: gjson.GetBytes(body, "repository.full_name").String()},
		}, true
	default:
		return PullRequestEvent{}, false
	}
}

// MatchesGitRepository reports whether the webhook's repository is the one a GitRepository describes. The detected
// provider decides which of the GitRepository's provider blocks is compared; a GitHub-detected event may come from
// Gitea or Forgejo, which share GitHub's headers, so all three are tried.
func (ev PullRequestEvent) MatchesGitRepository(provider string, spec promoterv1alpha1.GitRepositorySpec) bool {
	fullName := func(owner, name string) bool {
		return strings.EqualFold(owner+"/"+name, ev.Repo.FullName)
	}
	switch provider {
	case ProviderGitHub, ProviderGitea, ProviderForgejo:
		switch {
		case spec.GitHub != nil && fullName(spec.GitHub.Owner, spec.GitHub.Name):
			return true
		case spec.Gitea != nil && fullName(spec.Gitea.Owner, spec.Gitea.Name):
			return true
		case spec.Forgejo != nil && fullName(spec.Forgejo.Owner, spec.Forgejo.Name):
			return true
		default:
			return false
		}
	case ProviderGitLab:
		if spec.GitLab == nil {
			return false
		}
		if ev.Repo.ProjectID != 0 && spec.GitLab.ProjectID != 0 {
			return int64(spec.GitLab.ProjectID) == ev.Repo.ProjectID
		}
		return fullName(spec.GitLab.Namespace, spec.GitLab.Name)
	case ProviderAzureDevops:
		return spec.AzureDevOps != nil &&
			strings.EqualFold(spec.AzureDevOps.Project, ev.Repo.Project) &&
			strings.EqualFold(spec.AzureDevOps.Name, ev.Repo.Name)
	case ProviderBitbucketCloud:
		return spec.BitbucketCloud != nil && fullName(spec.BitbucketCloud.Owner, spec.BitbucketCloud.Name)
	default:
		return false
	}
}

// handlePullRequestEvent wakes the PullRequest resources that track the pull request a webhook is about, so the
// PullRequest controller re-reads it from the provider at once instead of on its next requeue. A pull request the
// controller does not track is ignored. It returns how many resources were enqueued.
func (wr *WebhookReceiver) handlePullRequestEvent(ctx context.Context, provider string, ev PullRequestEvent) int {
	logger := log.FromContext(ctx).WithValues("pullRequestID", ev.ID, "action", ev.Action)
	if wr.enqueuePR == nil {
		return 0
	}
	var prs promoterv1alpha1.PullRequestList
	if err := wr.k8sClient.List(ctx, &prs); err != nil {
		logger.Error(err, "failed to list PullRequests for webhook")
		return 0
	}
	enqueued := 0
	for i := range prs.Items {
		pr := &prs.Items[i]
		if pr.Status.ID != ev.ID {
			continue
		}
		var repo promoterv1alpha1.GitRepository
		key := client.ObjectKey{Namespace: pr.Namespace, Name: pr.Spec.RepositoryReference.Name}
		if err := wr.k8sClient.Get(ctx, key, &repo); err != nil {
			logger.V(4).Info("failed to get GitRepository of PullRequest", "pullRequest", pr.Name, "error", err)
			continue
		}
		if !ev.MatchesGitRepository(provider, repo.Spec) {
			continue
		}
		wr.enqueuePR(pr.Namespace, pr.Name)
		enqueued++
		logger.Info("Triggered reconcile of PullRequest via webhook", "namespace", pr.Namespace, "name", pr.Name)
	}
	if enqueued == 0 {
		logger.V(4).Info("no PullRequest tracks the pull request of the webhook")
	}
	return enqueued
}
