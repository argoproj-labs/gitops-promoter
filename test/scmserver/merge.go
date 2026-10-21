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
	case BitbucketDataCenter:
		return signature{name: "Bitbucket", email: "noreply@bitbucket.example.com"}
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
	switch p {
	case GitLab:
		return "refs/merge-requests/" + strconv.Itoa(number)
	case BitbucketDataCenter:
		return "refs/pull-requests/" + strconv.Itoa(number)
	default:
		return "refs/pull/" + strconv.Itoa(number)
	}
}

// pullHeadRef is the ref under pullRefPrefix that points at a pull request's head: Bitbucket Data Center calls it
// "from", every other provider "head".
func pullHeadRef(p Provider, number int) string {
	if p == BitbucketDataCenter {
		return pullRefPrefix(p, number) + "/from"
	}
	return pullRefPrefix(p, number) + "/head"
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
	case BitbucketDataCenter:
		// The auto-generated subject is prepended to the requested message unless the merge sets autoSubject false.
		subject := bitbucketDCMergeSubject(pr)
		if requested != "" {
			return subject + "\n\n" + requested
		}
		return subject
	default:
		return requested
	}
}

// defaultSquashMessage is the commit message a provider uses for a squash merge when the request does not supply one.
// requested is the message passed by the client and may be empty.
func defaultSquashMessage(pr *pullRequest, requested string) string {
	if requested != "" {
		return requested
	}
	switch pr.repo.provider {
	case GitHub, Gitea, Forgejo:
		return fmt.Sprintf("%s (#%d)", pr.title, pr.number)
	case AzureDevOps:
		return fmt.Sprintf("Merged PR %d: %s", pr.number, pr.title)
	case BitbucketCloud:
		return fmt.Sprintf("%s (pull request #%d)", pr.title, pr.number)
	case BitbucketDataCenter:
		return bitbucketDCMergeSubject(pr)
	default:
		// GitLab's default squash commit template is the merge request title.
		return pr.title
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
	// A merge or close may have settled the pull request while this call waited for the lock; its preview merge
	// commit and base must then stay as the merge left them.
	s.store.mu.Lock()
	state = pr.state
	s.store.mu.Unlock()
	if state != prOpen {
		return nil
	}

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
	if err := s.git.setRef(ctx, r.dir, pullHeadRef(r.provider, number), head); err != nil {
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
	if pr.state != prOpen {
		// Closed without the git lock (source branch deleted, admin close) while the preview was computed.
		return nil
	}
	if pr.headSHA != "" && pr.headSHA != head {
		// A rescope: new commits on the source branch change the pull request.
		pr.version++
	}
	if pr.headSHA != head || pr.baseSHA != base || pr.mergeableAt.IsZero() {
		pr.mergeableAt = s.now().Add(behavior.MergeabilityDelay.Duration)
		// Azure DevOps re-runs the merge when either branch moves, clearing a previous completion failure.
		if !pr.completionQueued {
			pr.completionFailure = ""
		}
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
	// message is the merge commit message.
	message string
	// squashMessage is the squash commit message; it defaults to message.
	squashMessage string
	author        signature
	// squash squashes the pull request into a single commit with the target branch as its only parent. Every provider
	// but GitLab lands that commit on the target branch. GitLab's default merge method (merge commit) squashes first
	// and then merges the squash commit with a merge commit (app/services/merge_requests/merge_strategies/
	// from_source_branch.rb), so the target branch tip is the merge commit and squash_commit_sha is its second parent.
	squash bool
}

// marksMergedByPush reports whether provider p marks an open pull request merged when its head commit reaches the
// target branch by a push (GitHub; GitLab's RefreshService#post_merge_manually_merged; Gitea's
// manuallyMergedByPushedCommit; Bitbucket Cloud; Bitbucket Data Center's remote merge detection). Azure DevOps leaves
// such a pull request active.
func marksMergedByPush(p Provider) bool {
	return p != AzureDevOps
}

// closesOnSourceDeletion reports whether provider p closes an open pull request when its source branch is deleted
// (GitHub; GitLab's RefreshService#close_upon_missing_source_branch_ref; Gitea's CloseBranchPulls; Bitbucket Data
// Center declines it). Azure DevOps keeps the pull request active, and Bitbucket Cloud's behavior is not reproduced.
func closesOnSourceDeletion(p Provider) bool {
	switch p {
	case GitHub, GitLab, Gitea, Forgejo, BitbucketDataCenter:
		return true
	default:
		return false
	}
}

// landMerge merges a pull request into its target branch with a merge commit, or squashes it when req.squash is set,
// marks it merged and notifies subscribers. It returns the SHA of the commit that became the target branch tip.
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
	// Another merge of the same pull request may have landed while this one waited for the lock.
	s.store.mu.Lock()
	state = pr.state
	s.store.mu.Unlock()
	if state != prOpen {
		r.gitMu.Unlock()
		return "", errNotOpen
	}
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
	if r.provider == BitbucketDataCenter {
		// Bitbucket Data Center commits merges as the user who merges.
		committer = author
	}
	committer.when = now
	squashMessage := req.squashMessage
	if squashMessage == "" {
		squashMessage = req.message
	}
	var mergeSHA, squashSHA string
	switch {
	case req.squash && r.provider == GitLab:
		squashSHA, err = s.git.commitTree(ctx, r.dir, tree, []string{base}, squashMessage, author, committer)
		if err == nil {
			mergeSHA, err = s.git.commitTree(ctx, r.dir, tree, []string{base, squashSHA}, req.message, author, committer)
		}
	case req.squash:
		mergeSHA, err = s.git.commitTree(ctx, r.dir, tree, []string{base}, squashMessage, author, committer)
		squashSHA = mergeSHA
	default:
		mergeSHA, err = s.git.commitTree(ctx, r.dir, tree, []string{base, head}, req.message, author, committer)
	}
	if err != nil {
		r.gitMu.Unlock()
		return "", err
	}
	if err := s.git.updateRef(ctx, r.dir, "refs/heads/"+target, mergeSHA, base); err != nil {
		r.gitMu.Unlock()
		return "", err
	}
	_ = s.git.deleteRef(ctx, r.dir, pullRefPrefix(r.provider, pr.number)+"/merge")

	// Mark the pull request merged before releasing gitMu, so a concurrent merge sees it is no longer open.
	s.store.mu.Lock()
	pr.state = prMerged
	pr.headSHA = head
	pr.baseSHA = base
	pr.mergeCommitSHA = mergeSHA
	pr.squashCommitSHA = squashSHA
	pr.squashed = req.squash
	pr.mergedAt = now
	pr.closedAt = now
	pr.updatedAt = now
	pr.version++
	pr.completionQueued = false
	pr.asyncMergePending = false
	s.store.mu.Unlock()
	r.gitMu.Unlock()

	s.webhooks.notifyPullRequest(ctx, s, pr, prActionMerged)
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
	deleted := map[string]bool{}
	for _, c := range changes {
		if branch, ok := strings.CutPrefix(c.ref, "refs/heads/"); ok {
			changed[branch] = true
			if c.after == zeroSHA {
				deleted[branch] = true
			}
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
		settled, action, err := s.settlePROutOfBand(ctx, pr, changed, deleted)
		if err != nil {
			s.log.Error("settle pull request after push", "repo", r.fullName(), "number", pr.number, "error", err)
		}
		if action != "" {
			s.webhooks.notifyPullRequest(ctx, s, pr, action)
		}
		if settled {
			continue
		}
		if err := s.refreshPR(ctx, pr); err != nil {
			s.log.Error("refresh pull request after push", "repo", r.fullName(), "number", pr.number, "error", err)
		}
	}
}

// settlePROutOfBand closes or merges an open pull request the way its provider does when a push, rather than the
// pull request API, settles it: the source branch was deleted, or the head commit reached the target branch. It
// returns true when the pull request is no longer open, and the pull request event this call caused, if any.
func (s *Server) settlePROutOfBand(ctx context.Context, pr *pullRequest, changed, deleted map[string]bool) (bool, prAction, error) {
	s.store.mu.Lock()
	r := pr.repo
	state, source, target := pr.state, pr.source, pr.target
	s.store.mu.Unlock()
	if state != prOpen {
		return true, "", nil
	}
	now := s.now()
	if deleted[source] && closesOnSourceDeletion(r.provider) {
		s.store.mu.Lock()
		defer s.store.mu.Unlock()
		if pr.state != prOpen {
			return true, "", nil
		}
		pr.state = prClosed
		pr.closedAt = now
		pr.updatedAt = now
		pr.version++
		pr.completionQueued = false
		pr.asyncMergePending = false
		return true, prActionClosed, nil
	}
	if !changed[target] || !marksMergedByPush(r.provider) {
		return false, "", nil
	}

	r.gitMu.Lock()
	defer r.gitMu.Unlock()
	head, err := s.git.branchSHA(ctx, r.dir, source)
	if err != nil {
		return false, "", nil //nolint:nilerr // a missing source branch is handled by refreshPR
	}
	tip, err := s.git.branchSHA(ctx, r.dir, target)
	if err != nil {
		return false, "", nil //nolint:nilerr // a missing target branch is handled by refreshPR
	}
	contained, err := s.git.isAncestor(ctx, r.dir, head, tip)
	if err != nil || !contained {
		return false, "", err
	}
	mergeSHA, err := s.git.mergeCommitOf(ctx, r.dir, head, tip)
	if err != nil {
		return false, "", err
	}
	_ = s.git.deleteRef(ctx, r.dir, pullRefPrefix(r.provider, pr.number)+"/merge")

	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if pr.state != prOpen {
		return true, "", nil
	}
	pr.state = prMerged
	pr.headSHA = head
	pr.mergeCommitSHA = mergeSHA
	pr.squashCommitSHA = ""
	pr.squashed = false
	pr.conflicts = false
	pr.previewMergeSHA = ""
	pr.mergedAt = now
	pr.closedAt = now
	pr.updatedAt = now
	pr.version++
	pr.completionQueued = false
	pr.asyncMergePending = false
	return true, prActionMerged, nil
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
