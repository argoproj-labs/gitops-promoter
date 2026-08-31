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
	trailers map[string][]string // nil until parsed by getTrailers
	Message  string
	State    v1alpha1.CommitShaState
}

// git log batch format: six NUL-separated fields per commit, in this order:
// %H (sha), %an (author), %cI (commit time), %s (subject), %b (body), %B (full message).
//
// NUL cannot appear inside those fields, so the output is a flat field stream.
const (
	commitFieldSep      = "\x00"
	commitLogFormat     = "%H%x00%an%x00%cI%x00%s%x00%b%x00%B"
	commitLogFieldCount = 6
)

// fullObjectID matches a complete SHA-1 or SHA-256 object ID. Batch inputs must be full SHAs
// so crafted trailer values cannot inject extra revisions or newlines into git log --stdin.
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
		if sha != "" {
			blobRequests = append(blobRequests, sha+":"+metaPath)
		}
	}

	// cat-file reports missing objects per request and still exits zero; errors here are real failures.
	if err := g.prefetchBlobs(ctx, blobRequests...); err != nil {
		return err
	}
	g.prefetchCommits(ctx, shas...)
	return nil
}

func (g *EnvironmentOperations) getCommit(ctx context.Context, sha string) (commitObject, error) {
	// git resolves uppercase revisions but emits %H lowercase, so cache keys are always lowercase.
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
	if commit.trailers != nil {
		return commit.trailers, nil
	}

	trailers, err := ParseTrailersFromMessage(ctx, commit.Message)
	if err != nil {
		return nil, err
	}

	commit.trailers = trailers
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
	g.blobs[request] = blob
	return blob, nil
}

// prefetchCommits fills the commit cache in a single git log.
//
// Prefetch is best-effort: git log fails the whole batch when any SHA is missing, and callers
// still resolve present SHAs individually via getCommit. Trailer SHAs often point at commits
// that were never fetched into this clone.
func (g *EnvironmentOperations) prefetchCommits(ctx context.Context, shas ...string) {
	pending := make(map[string]struct{}, len(shas))
	for _, sha := range shas {
		key := strings.ToLower(sha)
		if key == "" {
			continue
		}
		if _, ok := g.commits[key]; ok {
			continue
		}
		pending[key] = struct{}{}
	}
	if len(pending) == 0 {
		return
	}

	uncached := make([]string, 0, len(pending))
	for key := range pending {
		uncached = append(uncached, key)
	}

	results, err := g.commitLogBatch(ctx, uncached...)
	if err != nil {
		log.FromContext(ctx).V(4).Info("failed to prefetch commits, falling back to individual lookups", "error", err)
		return
	}
	for key, commit := range results {
		g.commits[key] = commit
	}
}

func (g *EnvironmentOperations) prefetchBlobs(ctx context.Context, requests ...string) error {
	pending := make(map[string]struct{}, len(requests))
	for _, req := range requests {
		if req == "" {
			continue
		}
		if _, ok := g.blobs[req]; ok {
			continue
		}
		pending[req] = struct{}{}
	}
	if len(pending) == 0 {
		return nil
	}

	uncached := make([]string, 0, len(pending))
	for req := range pending {
		uncached = append(uncached, req)
	}

	results, err := g.catFileBatch(ctx, uncached...)
	if err != nil {
		return err
	}
	for key, blob := range results {
		g.blobs[key] = blob
	}
	return nil
}

func (g *EnvironmentOperations) commitLogBatch(ctx context.Context, shas ...string) (map[string]commitObject, error) {
	if len(shas) == 0 {
		return map[string]commitObject{}, nil
	}

	gitPath := g.ClonePath()
	if gitPath == "" {
		return nil, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	// Drop invalid SHAs instead of failing the batch; getCommit reports them individually.
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

func parseCommitLogOutput(stdout string) (map[string]commitObject, error) {
	if stdout == "" {
		return map[string]commitObject{}, nil
	}

	// Empty fields are meaningful (%b empty for subject-only commits, etc.), so keep them all.
	fields := strings.Split(stdout, commitFieldSep)
	if len(fields)%commitLogFieldCount != 0 {
		return nil, fmt.Errorf("expected a multiple of %d fields in git log output, got %d", commitLogFieldCount, len(fields))
	}

	recordCount := len(fields) / commitLogFieldCount
	results := make(map[string]commitObject, recordCount)
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

		blob, err := parseCatFileBlobFromHeader(reader, req, strings.TrimSuffix(header, "\n"))
		if err != nil {
			return nil, err
		}
		results[req] = blob
	}

	return results, nil
}

func parseCatFileBlobFromHeader(reader *bufio.Reader, req, header string) (blobObject, error) {
	if strings.HasSuffix(header, " missing") {
		return blobObject{Missing: true}, nil
	}

	size, err := parseCatFileObjectSize(header)
	if err != nil {
		return blobObject{}, err
	}

	data := make([]byte, size)
	if _, err := io.ReadFull(reader, data); err != nil {
		return blobObject{}, fmt.Errorf("truncated cat-file object %q: expected %d bytes: %w", req, size, err)
	}
	if _, err := reader.Discard(1); err != nil {
		return blobObject{}, fmt.Errorf("missing terminator after cat-file object %q: %w", req, err)
	}

	return blobObject{Data: data}, nil
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
