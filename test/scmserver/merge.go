package scmserver

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	errMergeConflict  = errors.New("merge conflict")
	errHeadMismatch   = errors.New("head of the source branch does not match the expected SHA")
	errBranchNotFound = errors.New("branch not found")
	errNotOpen        = errors.New("pull request is not open")
)

// providerBot is the identity a provider uses as committer of merge commits.
func providerBot(p Provider) signature {
	switch p {
	case GitHub:
		return signature{name: "GitHub", email: "noreply@github.com"}
	case GitLab:
		return signature{name: "GitLab", email: "noreply@gitlab.com"}
	case AzureDevOps:
		return signature{name: "Azure DevOps", email: "noreply@dev.azure.com"}
	case BitbucketCloud:
		return signature{name: "Bitbucket", email: "noreply@bitbucket.org"}
	case Gitea:
		return signature{name: "Gitea", email: "noreply@gitea.io"}
	case Forgejo:
		return signature{name: "Forgejo", email: "noreply@forgejo.org"}
	default:
		return signature{name: "SCM", email: "noreply@example.com"}
	}
}

// actor returns the signature of an API principal acting on provider p.
func actor(p Provider, principal string) signature {
	if principal == "" {
		principal = "scmserver"
	}
	return signature{name: principal, email: principal + "@users.noreply." + string(p) + ".test"}
}

// mergeTree merges head into base without touching any ref and returns the resulting tree. conflict is true when the
// merge has conflicts, in which case tree is the conflicted tree.
func (g *gitCLI) mergeTree(ctx context.Context, dir, base, head string) (tree string, conflict bool, err error) {
	out, err := g.run(ctx, dir, nil, nil, "merge-tree", "--write-tree", "--no-messages", base, head)
	if err != nil {
		if exitCode(err) == 1 {
			first, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
			return first, true, nil
		}
		return "", false, err
	}
	first, _, _ := strings.Cut(out, "\n")
	return first, false, nil
}

// setRef points ref at sha unconditionally.
func (g *gitCLI) setRef(ctx context.Context, dir, ref, sha string) error {
	_, err := g.run(ctx, dir, nil, nil, "update-ref", ref, sha)
	return err
}

// deleteRef removes ref if it exists.
func (g *gitCLI) deleteRef(ctx context.Context, dir, ref string) error {
	_, err := g.run(ctx, dir, nil, nil, "update-ref", "-d", ref)
	return err
}

// pullRefPrefix is where a provider exposes pull request heads and test merges.
func pullRefPrefix(p Provider, number int) string {
	if p == GitLab {
		return "refs/merge-requests/" + strconv.Itoa(number)
	}
	return "refs/pull/" + strconv.Itoa(number)
}

// previewMergeMessage is the message of the test-merge commit a provider computes for an open pull request.
func previewMergeMessage(pr *pullRequest, head, base string) string {
	switch pr.repo.provider {
	case AzureDevOps:
		return fmt.Sprintf("Merge pull request %d from %s into %s", pr.number, pr.source, pr.target)
	default:
		return fmt.Sprintf("Merge %s into %s", head, base)
	}
}

// defaultMergeMessage is the commit message a provider uses when the merge request does not supply one. requested is
// the message passed by the API client and may be empty.
func defaultMergeMessage(pr *pullRequest, requested string) string {
	r := pr.repo
	switch r.provider {
	case GitHub:
		body := requested
		if body == "" {
			body = pr.title
		}
		return fmt.Sprintf("Merge pull request #%d from %s/%s\n\n%s", pr.number, r.owner, pr.source, body)
	case GitLab:
		if requested != "" {
			return requested
		}
		return fmt.Sprintf("Merge branch '%s' into '%s'\n\n%s\n\nSee merge request %s!%d", pr.source, pr.target, pr.title, r.fullName(), pr.number)
	case AzureDevOps:
		if requested != "" {
			return requested
		}
		return fmt.Sprintf("Merged PR %d: %s", pr.number, pr.title)
	case BitbucketCloud:
		if requested != "" {
			return requested
		}
		return fmt.Sprintf("Merged in %s (pull request #%d)\n\n%s", pr.source, pr.number, pr.title)
	case Gitea, Forgejo:
		title := fmt.Sprintf("Merge pull request '%s' (#%d) from %s into %s", pr.title, pr.number, pr.source, pr.target)
		if requested != "" {
			return title + "\n\n" + requested
		}
		return title
	default:
		return requested
	}
}

// refreshPR recomputes the head, base, conflict state and test-merge commit of an open pull request, and restarts
// the provider's asynchronous mergeability check.
func (s *Server) refreshPR(ctx context.Context, pr *pullRequest) error {
	s.store.mu.Lock()
	r := pr.repo
	state, source, target, number := pr.state, pr.source, pr.target, pr.number
	s.store.mu.Unlock()
	if state != prOpen {
		return nil
	}

	r.gitMu.Lock()
	defer r.gitMu.Unlock()

	head, headErr := s.git.branchSHA(ctx, r.dir, source)
	base, baseErr := s.git.branchSHA(ctx, r.dir, target)
	if headErr != nil || baseErr != nil {
		// A missing branch leaves the previous state in place, as providers do until the branch reappears.
		return nil
	}
	tree, conflict, err := s.git.mergeTree(ctx, r.dir, base, head)
	if err != nil {
		return err
	}
	prefix := pullRefPrefix(r.provider, number)
	if err := s.git.setRef(ctx, r.dir, prefix+"/head", head); err != nil {
		return err
	}
	preview := ""
	if conflict {
		if err := s.git.deleteRef(ctx, r.dir, prefix+"/merge"); err != nil {
			return err
		}
	} else {
		s.store.mu.Lock()
		message := previewMergeMessage(pr, head, base)
		s.store.mu.Unlock()
		bot := providerBot(r.provider)
		bot.when = s.now()
		preview, err = s.git.commitTree(ctx, r.dir, tree, []string{base, head}, message, bot, bot)
		if err != nil {
			return err
		}
		if err := s.git.setRef(ctx, r.dir, prefix+"/merge", preview); err != nil {
			return err
		}
	}

	behavior := s.behavior.get(r.provider)
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if pr.headSHA != head || pr.baseSHA != base || pr.mergeableAt.IsZero() {
		pr.mergeableAt = s.now().Add(behavior.MergeabilityDelay.Duration)
	}
	pr.headSHA = head
	pr.baseSHA = base
	pr.conflicts = conflict
	pr.previewMergeSHA = preview
	return nil
}

// mergeRequest describes a merge of a pull request into its target branch.
type mergeRequest struct {
	pr           *pullRequest
	expectedHead string
	message      string
	author       signature
}

// landMerge merges a pull request into its target branch with a merge commit, marks it merged and notifies
// subscribers. It returns the SHA of the merge commit.
func (s *Server) landMerge(ctx context.Context, req mergeRequest) (string, error) {
	pr := req.pr
	s.store.mu.Lock()
	r := pr.repo
	state, source, target := pr.state, pr.source, pr.target
	s.store.mu.Unlock()
	if state != prOpen {
		return "", errNotOpen
	}

	r.gitMu.Lock()
	head, err := s.git.branchSHA(ctx, r.dir, source)
	if err != nil {
		r.gitMu.Unlock()
		return "", fmt.Errorf("%w: %s", errBranchNotFound, source)
	}
	if req.expectedHead != "" && !strings.EqualFold(req.expectedHead, head) {
		r.gitMu.Unlock()
		return "", errHeadMismatch
	}
	base, err := s.git.branchSHA(ctx, r.dir, target)
	if err != nil {
		r.gitMu.Unlock()
		return "", fmt.Errorf("%w: %s", errBranchNotFound, target)
	}
	tree, conflict, err := s.git.mergeTree(ctx, r.dir, base, head)
	if err != nil {
		r.gitMu.Unlock()
		return "", err
	}
	if conflict {
		r.gitMu.Unlock()
		s.store.mu.Lock()
		pr.conflicts = true
		s.store.mu.Unlock()
		return "", errMergeConflict
	}
	now := s.now()
	author := req.author
	author.when = now
	committer := providerBot(r.provider)
	committer.when = now
	mergeSHA, err := s.git.commitTree(ctx, r.dir, tree, []string{base, head}, req.message, author, committer)
	if err != nil {
		r.gitMu.Unlock()
		return "", err
	}
	if err := s.git.updateRef(ctx, r.dir, "refs/heads/"+target, mergeSHA, base); err != nil {
		r.gitMu.Unlock()
		return "", err
	}
	_ = s.git.deleteRef(ctx, r.dir, pullRefPrefix(r.provider, pr.number)+"/merge")
	r.gitMu.Unlock()

	s.store.mu.Lock()
	pr.state = prMerged
	pr.headSHA = head
	pr.baseSHA = base
	pr.mergeCommitSHA = mergeSHA
	pr.mergedAt = now
	pr.closedAt = now
	pr.updatedAt = now
	pr.completionQueued = false
	pr.asyncMergePending = false
	s.store.mu.Unlock()

	s.afterRefChanges(ctx, r, []refChange{{ref: "refs/heads/" + target, before: base, after: mergeSHA}}, author.name)
	return mergeSHA, nil
}

// afterRefChanges reacts to branch updates: it notifies webhook subscribers and refreshes affected pull requests.
func (s *Server) afterRefChanges(ctx context.Context, r *repo, changes []refChange, pusher string) {
	if len(changes) == 0 {
		return
	}
	s.webhooks.notifyPush(ctx, s, r, changes, pusher)

	changed := map[string]bool{}
	for _, c := range changes {
		if branch, ok := strings.CutPrefix(c.ref, "refs/heads/"); ok {
			changed[branch] = true
		}
	}
	s.store.mu.Lock()
	var affected []*pullRequest
	for _, pr := range s.store.prs[r.id] {
		if pr.state == prOpen && (changed[pr.source] || changed[pr.target]) {
			affected = append(affected, pr)
		}
	}
	s.store.mu.Unlock()
	for _, pr := range affected {
		if err := s.refreshPR(ctx, pr); err != nil {
			s.log.Error("refresh pull request after push", "repo", r.fullName(), "number", pr.number, "error", err)
		}
	}
}

func isSuccessState(cs *commitStatus) bool {
	switch strings.ToLower(cs.state) {
	case "success", "successful", "succeeded":
		return true
	case "completed":
		return cs.conclusion == "success"
	default:
		return false
	}
}

// now returns the server clock.
func (s *Server) now() time.Time {
	return time.Now().UTC()
}
