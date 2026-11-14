package git

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
)

// commitsOnlyFilter keeps trees and blobs out of a fetch. Ordering environments needs the shape of the
// dry history (which commit contains which), never its contents, and trees are most of a repository.
const commitsOnlyFilter = "--filter=tree:0"

// noLazyFetchEnv keeps a local probe local. The clone is a promisor, so git would otherwise try to
// download a missing object from the remote, with no negotiation, and then report it as present.
var noLazyFetchEnv = []string{"GIT_NO_LAZY_FETCH=1"}

func tagRef(tag string) string {
	return "refs/tags/" + tag
}

// TagState is a tag as it was last read from the remote.
type TagState struct {
	// Raw is what the tag ref holds: a commit for a lightweight tag, a tag object for an annotated one.
	// Empty when the tag does not exist. A leased push compares against this value.
	Raw string
	// Commit is the commit the tag points at. Empty when the tag does not exist.
	Commit string
}

// DryRefs is the state of the refs that order a PromotionStrategy's environments.
type DryRefs struct {
	// Tags maps every requested tag name to its state. A tag that does not exist maps to a zero TagState.
	Tags map[string]TagState
	// Head is the commit at the head of the dry branch.
	Head string
	// NotesTip is the tip of PromoterValidationsNotesRef, empty when the ref does not exist.
	NotesTip string
}

// DryBranchNotFoundError is returned by SyncDryRefs when the dry branch does not exist on the remote.
type DryBranchNotFoundError struct {
	Branch string
}

func (e *DryBranchNotFoundError) Error() string {
	return fmt.Sprintf("dry branch %q does not exist on the remote", e.Branch)
}

// TagPushError is returned by MoveTag when the remote did not take the push.
type TagPushError struct {
	// Tag is the tag that was pushed.
	Tag string
	// Reason is what git reported for the tag, for example "[rejected] (stale info)".
	Reason string
}

func (e *TagPushError) Error() string {
	return fmt.Sprintf("push of tag %q was not accepted: %s", e.Tag, e.Reason)
}

// SyncDryRefs brings the dry branch, the given tags and the validation notes up to date in the clone and
// returns where each one is. It asks the remote once which refs moved and fetches only those. A tag
// that was moved, including backward, is read at its new position; a tag that was deleted is deleted
// locally too.
//
// The dry branch and the tags are fetched without trees or blobs: callers only ask which commit contains
// which. The notes are fetched whole, because their contents are read.
//
// Read-only with respect to the clone's index/worktree/HEAD.
func (g *EnvironmentOperations) SyncDryRefs(ctx context.Context, dryBranch string, tags []string) (DryRefs, error) {
	gitPath := g.ClonePath()
	if gitPath == "" {
		return DryRefs{}, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	refs := make([]string, 0, len(tags)+2)
	refs = append(refs, branchRef(dryBranch), PromoterValidationsNotesRef)
	for _, tag := range tags {
		refs = append(refs, tagRef(tag))
	}
	remote, err := lsRemoteRefs(ctx, g.gap, g.gitRepo, refs...)
	if err != nil {
		return DryRefs{}, fmt.Errorf("failed to list the dry branch, tags and validation notes: %w", err)
	}
	if remote[branchRef(dryBranch)] == "" {
		return DryRefs{}, &DryBranchNotFoundError{Branch: dryBranch}
	}

	localRefs := make([]string, 0, len(refs))
	localRefs = append(localRefs, trackingRef(dryBranch), PromoterValidationsNotesRef)
	for _, tag := range tags {
		localRefs = append(localRefs, tagRef(tag))
	}
	local, err := g.localRefShas(ctx, localRefs...)
	if err != nil {
		return DryRefs{}, err
	}

	if local[trackingRef(dryBranch)] != remote[branchRef(dryBranch)] {
		if err := g.fetchCommitsOnly(ctx, "+"+branchRef(dryBranch)+":"+trackingRef(dryBranch)); err != nil {
			return DryRefs{}, fmt.Errorf("failed to fetch dry branch %q: %w", dryBranch, err)
		}
		g.forgetLocalRef(trackingRef(dryBranch))
	}
	// The tags are fetched after the branch, and on their own: nearly always they point into history
	// that is already here, and a failure can then be put down to a tag.
	tagSpecs, err := g.staleTagRefspecs(ctx, tags, remote, local)
	if err != nil {
		return DryRefs{}, err
	}
	if err := g.fetchCommitsOnly(ctx, tagSpecs...); err != nil {
		return DryRefs{}, fmt.Errorf("failed to fetch the environment tags (each must point at a commit): %w", err)
	}

	notesTip, err := g.syncValidationNotes(ctx, remote[PromoterValidationsNotesRef], local[PromoterValidationsNotesRef])
	if err != nil {
		return DryRefs{}, err
	}

	// Report what the clone now holds rather than what ls-remote said: a ref that moved between the
	// two reads is then described by the objects that were actually fetched.
	head, err := g.revParse(ctx, trackingRef(dryBranch)+"^{commit}")
	if err != nil {
		return DryRefs{}, fmt.Errorf("failed to resolve dry branch %q: %w", dryBranch, err)
	}
	states, err := g.localTagStates(ctx, tags)
	if err != nil {
		return DryRefs{}, err
	}
	return DryRefs{Head: head, Tags: states, NotesTip: notesTip}, nil
}

// ReadTag reads one tag from the remote, as SyncDryRefs does for many.
//
// Read-only with respect to the clone's index/worktree/HEAD.
func (g *EnvironmentOperations) ReadTag(ctx context.Context, tag string) (TagState, error) {
	if g.ClonePath() == "" {
		return TagState{}, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}
	remote, err := lsRemoteRefs(ctx, g.gap, g.gitRepo, tagRef(tag))
	if err != nil {
		return TagState{}, fmt.Errorf("failed to list tag %q: %w", tag, err)
	}
	g.forgetLocalRef(tagRef(tag))
	local, err := g.localRefShas(ctx, tagRef(tag))
	if err != nil {
		return TagState{}, err
	}
	refspecs, err := g.staleTagRefspecs(ctx, []string{tag}, remote, local)
	if err != nil {
		return TagState{}, err
	}
	if err := g.fetchCommitsOnly(ctx, refspecs...); err != nil {
		return TagState{}, fmt.Errorf("failed to fetch tag %q: %w", tag, err)
	}
	states, err := g.localTagStates(ctx, []string{tag})
	if err != nil {
		return TagState{}, err
	}
	return states[tag], nil
}

// staleTagRefspecs returns the refspecs to fetch for the tags whose local copy differs from the remote,
// and deletes the local copy of tags the remote no longer has.
func (g *EnvironmentOperations) staleTagRefspecs(ctx context.Context, tags []string, remote, local map[string]string) ([]string, error) {
	var refspecs []string
	for _, tag := range tags {
		ref := tagRef(tag)
		switch {
		case remote[ref] == "" && local[ref] != "":
			_, stderr, err := g.runCmd(ctx, g.ClonePath(), "update-ref", "-d", ref)
			g.forgetLocalRef(ref)
			if err != nil {
				return nil, fmt.Errorf("failed to delete local tag %q: %w (stderr: %s)", tag, err, stderr)
			}
		case remote[ref] != "" && remote[ref] != local[ref]:
			refspecs = append(refspecs, "+"+ref+":"+ref)
			g.forgetLocalRef(ref)
		default:
			// The local copy already matches the remote.
		}
	}
	return refspecs, nil
}

// fetchCommitsOnly fetches the given refspecs from origin without trees or blobs. It is a no-op without
// refspecs. --no-tags keeps the fetch from also pulling every tag that points into the fetched history.
func (g *EnvironmentOperations) fetchCommitsOnly(ctx context.Context, refspecs ...string) error {
	if len(refspecs) == 0 {
		return nil
	}
	args := make([]string, 0, 4+len(refspecs))
	args = append(args, "fetch", "--no-tags", commitsOnlyFilter, "origin")
	args = append(args, refspecs...)

	start := time.Now()
	_, stderr, err := g.runCmd(ctx, g.ClonePath(), args...)
	metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationFetch, metrics.GitOperationResultFromError(err), time.Since(start))
	if err != nil {
		return fmt.Errorf("git fetch failed: %w (stderr: %s)", err, strings.TrimSpace(stderr))
	}
	return nil
}

// localTagStates reads the given tags from the clone. A tag that is absent maps to a zero TagState. A
// tag that points at something other than a commit is an error: nothing can be hydrated from it.
func (g *EnvironmentOperations) localTagStates(ctx context.Context, tags []string) (map[string]TagState, error) {
	states := make(map[string]TagState, len(tags))
	for _, tag := range tags {
		states[tag] = TagState{}
	}
	if len(tags) == 0 {
		return states, nil
	}

	const fieldSep = "\x00"
	args := make([]string, 0, 2+len(tags))
	args = append(args, "for-each-ref", "--format=%(refname)%00%(objectname)%00%(objecttype)")
	for _, tag := range tags {
		args = append(args, tagRef(tag))
	}
	stdout, stderr, err := g.runCmd(ctx, g.ClonePath(), args...)
	if err != nil {
		return nil, fmt.Errorf("failed to read local tags: %w (stderr: %s)", err, stderr)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(stdout), "\n") {
		fields := strings.Split(line, fieldSep)
		if len(fields) != 3 {
			continue
		}
		// for-each-ref patterns also match refs nested below them, so keep exact matches only.
		tag, isTag := strings.CutPrefix(fields[0], "refs/tags/")
		if _, requested := states[tag]; !isTag || !requested {
			continue
		}
		raw, objectType := fields[1], fields[2]
		commit := raw
		if objectType != "commit" {
			// An annotated tag, possibly of another tag. Peel all the way down.
			commit, err = g.revParse(ctx, fields[0]+"^{commit}")
			if err != nil {
				return nil, fmt.Errorf("tag %q does not point at a commit: %w", tag, err)
			}
		}
		states[tag] = TagState{Raw: raw, Commit: commit}
	}
	return states, nil
}

// MoveTag points tag at sha on the remote as a lightweight tag, creating it when it does not exist. The
// push is a compare-and-swap: it only goes through while the tag still holds expectedRaw on the remote
// (the TagState.Raw that was last read), and an empty expectedRaw means the tag must not exist. A tag
// that someone moved, created or deleted since it was read is therefore never overwritten.
//
// One tag is pushed per call. GitHub sends no push event when more than three tags are pushed at once,
// and one rejected tag must not hold back the others.
//
// A push the remote did not take is returned as a *TagPushError. The caller should read the tag again
// to learn whether it lost to another writer or was refused.
func (g *EnvironmentOperations) MoveTag(ctx context.Context, tag, sha, expectedRaw string) error {
	gitPath := g.ClonePath()
	if gitPath == "" {
		return fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}
	ref := tagRef(tag)

	start := time.Now()
	stdout, stderr, err := g.runCmd(ctx, gitPath, "push", "--porcelain", "--force-with-lease="+ref+":"+expectedRaw, "origin", sha+":"+ref)
	metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationPush, metrics.GitOperationResultFromError(err), time.Since(start))
	g.forgetLocalRef(ref)
	g.forgetRemoteRef(ref)
	if err != nil {
		if reason := porcelainRejection(stdout, ref); reason != "" {
			return &TagPushError{Tag: tag, Reason: reason}
		}
		return fmt.Errorf("failed to push tag %q: %w (stderr: %s)", tag, err, strings.TrimSpace(stderr))
	}

	if _, stderr, err := g.runCmd(ctx, gitPath, "update-ref", ref, sha); err != nil {
		return fmt.Errorf("pushed tag %q but failed to update it locally: %w (stderr: %s)", tag, err, stderr)
	}
	return nil
}

// porcelainRejection returns why git push --porcelain reports ref as rejected, or "" when its output has
// no rejection for ref. A status line is "<flag>\t<from>:<to>\t<summary>", and the flag is "!" for a
// ref the remote did not take. This reads git's own verdict for the ref instead of guessing from stderr,
// where server hooks may print anything.
func porcelainRejection(stdout, ref string) string {
	for line := range strings.SplitSeq(stdout, "\n") {
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) != 3 || fields[0] != "!" {
			continue
		}
		if _, to, found := strings.Cut(fields[1], ":"); found && to == ref {
			return strings.TrimSpace(fields[2])
		}
	}
	return ""
}

// HasCommit reports whether the commit is in the clone. It never contacts the remote.
//
// Read-only.
func (g *EnvironmentOperations) HasCommit(ctx context.Context, sha string) bool {
	gitPath := g.ClonePath()
	if gitPath == "" || !fullObjectID.MatchString(sha) {
		return false
	}
	_, _, err := g.runCmdWithEnv(ctx, gitPath, noLazyFetchEnv, "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

// EnsureCommit makes sure the commit and its history are in the clone, fetching them by SHA when they
// are not. It returns false, with no error, when the remote does not have the commit or will not serve
// it by SHA. Any other failure is an error, so that an outage is never read as "no such commit".
//
// Read-only with respect to the clone's index/worktree/HEAD.
func (g *EnvironmentOperations) EnsureCommit(ctx context.Context, sha string) (bool, error) {
	gitPath := g.ClonePath()
	if gitPath == "" {
		return false, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}
	if !fullObjectID.MatchString(sha) {
		return false, nil
	}
	if g.HasCommit(ctx, sha) {
		return true, nil
	}

	start := time.Now()
	_, stderr, err := g.runCmd(ctx, gitPath, "fetch", "--no-tags", commitsOnlyFilter, "origin", sha)
	metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationFetch, metrics.GitOperationResultFromError(err), time.Since(start))
	if err != nil {
		if isMissingObjectFetchError(stderr) {
			log.FromContext(ctx).V(4).Info("Commit is not available from the remote", "sha", sha, "stderr", strings.TrimSpace(stderr))
			return false, nil
		}
		return false, fmt.Errorf("failed to fetch commit %q: %w (stderr: %s)", sha, err, strings.TrimSpace(stderr))
	}
	// The object exists on the remote, but only a commit will do.
	return g.HasCommit(ctx, sha), nil
}

// ContainsCommit reports whether descendant is ancestor or has ancestor in its history. Both commits
// must already be in the clone (see EnsureCommit); a commit that is not there is an error rather than a
// silent download.
//
// Read-only.
func (g *EnvironmentOperations) ContainsCommit(ctx context.Context, descendant, ancestor string) (bool, error) {
	gitPath := g.ClonePath()
	if gitPath == "" {
		return false, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}
	if descendant == "" || ancestor == "" {
		return false, nil
	}
	if descendant == ancestor {
		return true, nil
	}
	_, stderr, err := g.runCmdWithEnv(ctx, gitPath, noLazyFetchEnv, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("failed to check whether %q contains %q: %w (stderr: %s)", descendant, ancestor, err, strings.TrimSpace(stderr))
}

// isMissingObjectFetchError reports whether a fetch by SHA failed because the remote does not have the
// object, or does not serve objects that no ref points at.
func isMissingObjectFetchError(stderr string) bool {
	return isAbsentObjectFetchError(stderr) || isUnadvertisedObjectFetchError(stderr)
}

// isAbsentObjectFetchError reports whether the remote said it does not have the object.
func isAbsentObjectFetchError(stderr string) bool {
	return strings.Contains(stderr, "not our ref") ||
		strings.Contains(stderr, "couldn't find remote ref") ||
		strings.Contains(stderr, "no such remote ref")
}

// isUnadvertisedObjectFetchError reports whether the fetch was refused because no ref points at the
// object and the remote does not serve such objects by SHA. That says nothing about whether it has it.
func isUnadvertisedObjectFetchError(stderr string) bool {
	return strings.Contains(stderr, "unadvertised object")
}

// scratchRepository creates an empty bare repository that knows the remote as origin, for a one-off read
// that should leave nothing behind in a clone. The caller removes it with the returned function.
func scratchRepository(ctx context.Context, gap scms.GitOperationsProvider, gitRepo *v1alpha1.GitRepository) (dir string, remove func() error, err error) {
	dir, err = os.MkdirTemp("", "scratch-*")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create scratch directory: %w", err)
	}
	remove = func() error {
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			return fmt.Errorf("failed to remove scratch directory %q: %w", dir, removeErr)
		}
		return nil
	}

	_, stderr, err := runCmd(ctx, gap, dir, "init", "--bare", "--quiet")
	if err == nil {
		// A filtered fetch needs a named remote; git records it as a promisor on the first such fetch.
		_, stderr, err = runCmd(ctx, gap, dir, "config", "remote.origin.url", gap.GetGitHttpsRepoUrl(*gitRepo))
	}
	if err != nil {
		err = fmt.Errorf("failed to set up scratch repository: %w (stderr: %s)", err, strings.TrimSpace(stderr))
		if removeErr := remove(); removeErr != nil {
			err = fmt.Errorf("%w (%w)", err, removeErr)
		}
		return "", nil, err
	}
	return dir, remove, nil
}

// RemoteCommitExists reports whether the repository has the commit, without needing a clone. It fetches
// that one commit object into a scratch repository, which is removed again.
//
// Some servers do not serve a commit by SHA unless a ref points at it. For those, the answer comes from
// fallbackBranch instead: the branch's commits are fetched and searched. With no fallback branch, such
// a server is an error, because the question cannot be answered.
//
// RemoteCommitExists is concurrency-safe: it uses no shared clone.
func RemoteCommitExists(ctx context.Context, gap scms.GitOperationsProvider, gitRepo *v1alpha1.GitRepository, sha, fallbackBranch string) (exists bool, err error) {
	if !fullObjectID.MatchString(sha) {
		return false, nil
	}

	dir, remove, err := scratchRepository(ctx, gap, gitRepo)
	if err != nil {
		return false, err
	}
	defer func() {
		if removeErr := remove(); removeErr != nil && err == nil {
			err = removeErr
		}
	}()

	probe := func() bool {
		_, _, probeErr := runCmdWithEnv(ctx, gap, dir, noLazyFetchEnv, "cat-file", "-e", sha+"^{commit}")
		return probeErr == nil
	}

	start := time.Now()
	_, stderr, fetchErr := runCmd(ctx, gap, dir, "fetch", "--no-tags", "--depth=1", commitsOnlyFilter, "origin", sha)
	metrics.RecordGitOperation(gitRepo, metrics.GitOperationFetch, metrics.GitOperationResultFromError(fetchErr), time.Since(start))
	switch {
	case fetchErr == nil:
		return probe(), nil
	case isAbsentObjectFetchError(stderr):
		return false, nil
	case !isUnadvertisedObjectFetchError(stderr):
		return false, fmt.Errorf("failed to fetch commit %q: %w (stderr: %s)", sha, fetchErr, strings.TrimSpace(stderr))
	case fallbackBranch == "":
		return false, fmt.Errorf("the remote does not serve commit %q by SHA and there is no branch to look for it on", sha)
	}

	start = time.Now()
	_, stderr, fetchErr = runCmd(ctx, gap, dir, "fetch", "--no-tags", commitsOnlyFilter, "origin", "+"+branchRef(fallbackBranch)+":"+branchRef(fallbackBranch))
	metrics.RecordGitOperation(gitRepo, metrics.GitOperationFetch, metrics.GitOperationResultFromError(fetchErr), time.Since(start))
	if fetchErr != nil {
		return false, fmt.Errorf("failed to fetch branch %q to look for commit %q: %w (stderr: %s)", fallbackBranch, sha, fetchErr, strings.TrimSpace(stderr))
	}
	return probe(), nil
}

// LiveDrySha returns the dry commit that is live in an environment: the dry SHA its active branch was
// last hydrated from, as recorded in hydrator.metadata at the tip of that branch. It returns "" when
// nothing is live: the branch does not exist, its tip has no hydrator.metadata under activePath, or the
// metadata names no commit.
//
// It reads the repository itself rather than a ChangeTransferPolicy's status, which reports nothing until
// the environment's proposed branch exists and can trail the branch by a reconcile. Only the tip commit
// and that one file are downloaded, into a scratch repository that is removed again.
//
// LiveDrySha is concurrency-safe: it uses no shared clone.
func LiveDrySha(ctx context.Context, gap scms.GitOperationsProvider, gitRepo *v1alpha1.GitRepository, activeBranch, activePath string) (drySha string, err error) {
	dir, remove, err := scratchRepository(ctx, gap, gitRepo)
	if err != nil {
		return "", err
	}
	defer func() {
		if removeErr := remove(); removeErr != nil && err == nil {
			err = removeErr
		}
	}()

	ref := branchRef(activeBranch)
	start := time.Now()
	_, stderr, fetchErr := runCmd(ctx, gap, dir, "fetch", "--no-tags", "--depth=1", "--filter=blob:none", "origin", "+"+ref+":"+ref)
	metrics.RecordGitOperation(gitRepo, metrics.GitOperationFetch, metrics.GitOperationResultFromError(fetchErr), time.Since(start))
	if fetchErr != nil {
		if isAbsentObjectFetchError(stderr) {
			return "", nil
		}
		return "", fmt.Errorf("failed to fetch active branch %q: %w (stderr: %s)", activeBranch, fetchErr, strings.TrimSpace(stderr))
	}

	// The tip's trees are here, so whether the file exists is answered locally. Its contents may not be.
	metaPath := buildHydratorMetadataPath(activePath)
	blob, stderr, err := runCmdWithEnv(ctx, gap, dir, noLazyFetchEnv, "rev-parse", "--verify", "--quiet", ref+":"+metaPath)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", nil
		}
		return "", fmt.Errorf("failed to look up %q on active branch %q: %w (stderr: %s)", metaPath, activeBranch, err, strings.TrimSpace(stderr))
	}
	content, stderr, err := runCmd(ctx, gap, dir, "cat-file", "blob", strings.TrimSpace(blob))
	if err != nil {
		return "", fmt.Errorf("failed to read %q on active branch %q: %w (stderr: %s)", metaPath, activeBranch, err, strings.TrimSpace(stderr))
	}

	var metadata HydratorMetadata
	if err := json.Unmarshal([]byte(content), &metadata); err != nil {
		return "", &MalformedHydratorMetadataError{Revision: ref, Path: metaPath, Err: err}
	}
	if !fullObjectID.MatchString(metadata.DrySha) {
		return "", nil
	}
	return metadata.DrySha, nil
}
