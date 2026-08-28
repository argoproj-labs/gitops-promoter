package git

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// blobObject holds the contents of one blob returned by git cat-file --batch.
type blobObject struct {
	Data    []byte
	Missing bool
}

// commitObject holds the fields of one commit, as formatted by git itself.
type commitObject struct {
	trailers       map[string][]string
	Message        string
	State          v1alpha1.CommitShaState
	trailersCached bool
}

// git log is asked to format the commit fields directly rather than handing back the raw commit
// object for us to decode: %s applies git's own subject folding, %cI is RFC 3339 carrying the
// commit's UTC offset, and %b is git's own subject/body split.
//
// Fields and records are both separated by NUL, which git refuses to store in a commit message and
// so cannot appear inside %s, %b or %B; any printable delimiter can. The output is therefore a flat
// run of fields rather than delimited records, read commitLogFieldCount at a time. With
// --pretty=format: (as opposed to tformat:) git writes the -z NUL between records and not after the
// last one, so the field count is an exact multiple of the record width.
const (
	commitFieldSep      = "\x00"
	commitLogFormat     = "%H%x00%an%x00%cI%x00%s%x00%b%x00%B"
	commitLogFieldCount = 6
)

// fullObjectID matches a complete SHA-1 or SHA-256 object ID. SHAs reach the batch readers from
// commit trailers, which anyone able to push to the repo controls, and git log --stdin reads
// option-like input such as --all as a revision selector rather than a revision. Requiring a full
// object ID keeps a crafted trailer from widening the batch, and rules out embedded newlines, which
// would otherwise add requests to the batch.
var fullObjectID = regexp.MustCompile(`^(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)

// LoadCommits prefetches commit metadata for the given SHAs into this instance's per-reconcile
// cache, so that later per-SHA reads are served from memory instead of spawning a git process each.
func (g *EnvironmentOperations) LoadCommits(ctx context.Context, shas ...string) error {
	if g.ClonePath() == "" {
		return fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}
	g.prefetchCommits(ctx, shas...)
	return nil
}

// LoadCommitAndMetadataBlobs prefetches each commit and its activePath hydrator.metadata blob.
func (g *EnvironmentOperations) LoadCommitAndMetadataBlobs(ctx context.Context, activePath string, shas ...string) error {
	if g.ClonePath() == "" {
		return fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	metaPath := buildHydratorMetadataPath(activePath)
	blobRequests := make([]string, 0, len(shas))
	for _, sha := range shas {
		if sha == "" {
			continue
		}
		blobRequests = append(blobRequests, sha+":"+metaPath)
	}

	// Unlike the commit batch, cat-file reports a missing object per request and still exits zero,
	// so an error here is a genuine failure rather than an absent object, and is worth surfacing.
	toFetch := requestsNotInCache(g.blobs, blobRequests)
	if len(toFetch) > 0 {
		results, err := g.catFileBatch(ctx, toFetch...)
		if err != nil {
			return err
		}
		if g.blobs == nil {
			g.blobs = make(map[string]blobObject, len(results))
		}
		for k, v := range results {
			g.blobs[k] = v
		}
	}

	g.prefetchCommits(ctx, shas...)
	return nil
}

// prefetchCommits fills the commit cache in a single git log.
//
// Prefetching is only an optimization, so a failure is logged and swallowed: git log is fatal for
// the whole batch when any one SHA is absent from the clone, and callers must still be able to
// resolve the SHAs that are present. Those are then resolved individually by getCommit, which
// reports the per-SHA error. SHAs read out of commit trailers routinely point at commits that were
// garbage collected or never fetched into this clone, which is why this cannot be fatal.
func (g *EnvironmentOperations) prefetchCommits(ctx context.Context, shas ...string) {
	// Lowercase first so the cache check and the dedup agree with the keys git returns, which are
	// always lowercase. Blob requests need no equivalent: parseCatFileBatch keys them by the request
	// string rather than by anything git echoes back, and their path is case-sensitive.
	keys := make([]string, 0, len(shas))
	for _, sha := range shas {
		keys = append(keys, strings.ToLower(sha))
	}

	toFetch := requestsNotInCache(g.commits, keys)
	if len(toFetch) == 0 {
		return
	}

	results, err := g.commitLogBatch(ctx, toFetch...)
	if err != nil {
		log.FromContext(ctx).V(4).Info("failed to prefetch commits, falling back to individual lookups", "error", err)
		return
	}
	if g.commits == nil {
		g.commits = make(map[string]commitObject, len(results))
	}
	for k, v := range results {
		g.commits[k] = v
	}
}

// requestsNotInCache returns requests that are neither already cached nor duplicated, preserving order.
func requestsNotInCache[T any](cache map[string]T, requests []string) []string {
	var toFetch []string
	seen := make(map[string]struct{}, len(requests))
	for _, req := range requests {
		if req == "" {
			continue
		}
		if _, ok := cache[req]; ok {
			continue
		}
		if _, ok := seen[req]; ok {
			continue
		}
		seen[req] = struct{}{}
		toFetch = append(toFetch, req)
	}
	return toFetch
}

func (g *EnvironmentOperations) getCommit(ctx context.Context, sha string) (commitObject, error) {
	// git resolves an uppercase revision but always emits %H in lowercase, so the batch results,
	// and with them the cache, are keyed lowercase however the caller wrote the SHA.
	key := strings.ToLower(sha)

	if commit, ok := g.commits[key]; ok {
		return commit, nil
	}

	if !fullObjectID.MatchString(key) {
		return commitObject{}, fmt.Errorf("refusing to look up commit %q: not a full git object ID", sha)
	}

	results, err := g.commitLogBatch(ctx, key)
	if err != nil {
		return commitObject{}, err
	}
	commit, ok := results[key]
	if !ok {
		return commitObject{}, fmt.Errorf("git log did not return a record for commit %q", sha)
	}
	if g.commits == nil {
		g.commits = make(map[string]commitObject, 1)
	}
	g.commits[key] = commit
	return commit, nil
}

// getTrailers returns git trailers for a commit, parsing at most once per cached commitObject.
func (g *EnvironmentOperations) getTrailers(ctx context.Context, sha string) (map[string][]string, error) {
	key := strings.ToLower(sha)

	commit, err := g.getCommit(ctx, sha)
	if err != nil {
		return nil, err
	}
	if commit.trailersCached {
		return commit.trailers, nil
	}

	trailers, err := ParseTrailersFromMessage(ctx, commit.Message)
	if err != nil {
		return nil, err
	}

	commit.trailers = trailers
	commit.trailersCached = true
	if g.commits == nil {
		g.commits = make(map[string]commitObject, 1)
	}
	g.commits[key] = commit

	return trailers, nil
}

func (g *EnvironmentOperations) getBlob(ctx context.Context, request string) (blobObject, error) {
	if blob, ok := g.blobs[request]; ok {
		return blob, nil
	}

	results, err := g.catFileBatch(ctx, request)
	if err != nil {
		return blobObject{}, err
	}
	blob, ok := results[request]
	if !ok {
		return blobObject{}, fmt.Errorf("cat-file did not return result for %q", request)
	}
	if g.blobs == nil {
		g.blobs = make(map[string]blobObject, 1)
	}
	g.blobs[request] = blob
	return blob, nil
}

func (g *EnvironmentOperations) commitLogBatch(ctx context.Context, shas ...string) (map[string]commitObject, error) {
	if len(shas) == 0 {
		return map[string]commitObject{}, nil
	}

	gitPath := g.ClonePath()
	if gitPath == "" {
		return nil, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	// Dropping rather than rejecting keeps one bad SHA from costing the whole batch its prefetch;
	// the dropped SHA has no record in the results, and getCommit reports it per-SHA.
	revisions := make([]string, 0, len(shas))
	for _, sha := range shas {
		if fullObjectID.MatchString(sha) {
			revisions = append(revisions, sha)
		}
	}
	if len(revisions) == 0 {
		return map[string]commitObject{}, nil
	}

	stdin := strings.NewReader(strings.Join(revisions, "\n") + "\n")
	stdout, _, err := runCmdWithEnvAndStdin(ctx, g.gap, gitPath, nil, stdin,
		"log", "--no-walk=unsorted", "--stdin", "-z", "--pretty=format:"+commitLogFormat)
	if err != nil {
		return nil, fmt.Errorf("git log failed: %w", err)
	}
	return parseCommitLogOutput(stdout)
}

func parseCommitLogOutput(stdout string) (map[string]commitObject, error) {
	if stdout == "" {
		return map[string]commitObject{}, nil
	}

	// Empty fields are meaningful — %b is empty for a subject-only commit, and all three message
	// fields are empty for a commit with no message — so they are kept rather than skipped.
	fields := strings.Split(stdout, commitFieldSep)
	if len(fields)%commitLogFieldCount != 0 {
		return nil, fmt.Errorf("expected a multiple of %d fields in git log output, got %d", commitLogFieldCount, len(fields))
	}

	results := make(map[string]commitObject, len(fields)/commitLogFieldCount)
	for i := 0; i < len(fields); i += commitLogFieldCount {
		sha, author, commitTime := fields[i], fields[i+1], fields[i+2]
		subject, body, message := fields[i+3], fields[i+4], fields[i+5]

		parsedTime, err := time.Parse(time.RFC3339, commitTime)
		if err != nil {
			return nil, fmt.Errorf("parse committer time %q for commit %q: %w", commitTime, sha, err)
		}

		results[sha] = commitObject{
			State: v1alpha1.CommitShaState{
				Sha:        sha,
				CommitTime: v1.Time{Time: parsedTime},
				Author:     author,
				Subject:    subject,
				Body:       strings.TrimSpace(body),
			},
			Message: message,
		}
	}

	return results, nil
}

func (g *EnvironmentOperations) catFileBatch(ctx context.Context, requests ...string) (map[string]blobObject, error) {
	if len(requests) == 0 {
		return map[string]blobObject{}, nil
	}

	gitPath := g.ClonePath()
	if gitPath == "" {
		return nil, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	stdin := strings.NewReader(strings.Join(requests, "\n") + "\n")
	stdout, _, err := runCmdWithEnvAndStdin(ctx, g.gap, gitPath, nil, stdin, "cat-file", "--batch")
	if err != nil {
		return nil, fmt.Errorf("git cat-file --batch failed: %w", err)
	}
	return parseCatFileBatch(strings.NewReader(stdout), requests)
}

// parseCatFileBatch reads the length-prefixed --batch stream: one header line per request, in
// request order, each followed by exactly the advertised number of content bytes and a newline.
func parseCatFileBatch(r io.Reader, requests []string) (map[string]blobObject, error) {
	reader := bufio.NewReader(r)
	results := make(map[string]blobObject, len(requests))

	for i, req := range requests {
		header, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("unexpected end of cat-file output after %d of %d objects: %w", i, len(requests), err)
		}
		header = strings.TrimSuffix(header, "\n")

		if strings.HasSuffix(header, " missing") {
			results[req] = blobObject{Missing: true}
			continue
		}

		size, err := parseCatFileObjectSize(header)
		if err != nil {
			return nil, err
		}

		data := make([]byte, size)
		if _, err := io.ReadFull(reader, data); err != nil {
			return nil, fmt.Errorf("truncated cat-file object %q: expected %d bytes: %w", req, size, err)
		}
		if _, err := reader.Discard(1); err != nil {
			return nil, fmt.Errorf("missing terminator after cat-file object %q: %w", req, err)
		}

		results[req] = blobObject{Data: data}
	}

	return results, nil
}

// parseCatFileObjectSize extracts the content length from a "<oid> <type> <size>" batch header.
func parseCatFileObjectSize(header string) (int, error) {
	fields := strings.Fields(header)
	if len(fields) != 3 {
		return 0, fmt.Errorf("invalid cat-file header %q", header)
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil {
		return 0, fmt.Errorf("invalid cat-file size %q in header %q: %w", fields[2], header, err)
	}
	return size, nil
}
