package git

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"maps"
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

// fullObjectID matches a complete lowercase SHA-1 or SHA-256 object ID. Batch inputs must be full
// SHAs so crafted trailer values cannot inject extra revisions or newlines into git log --stdin.
// Callers normalize revisions with strings.ToLower before matching; git emits %H lowercase.
var fullObjectID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

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
	if err := g.fetchBlobs(ctx, blobRequests...); err != nil {
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

	if err := g.fetchCommits(ctx, key); err != nil {
		return commitObject{}, err
	}
	commit, ok := g.commits[key]
	if !ok {
		return commitObject{}, fmt.Errorf("git log did not return a record for commit %q", sha)
	}
	return commit, nil
}

// getTrailers returns git trailers for a commit, parsing at most once per cached commitObject.
func (g *EnvironmentOperations) getTrailers(ctx context.Context, sha string) (map[string][]string, error) {
	key := strings.ToLower(sha)

	commit, err := g.getCommit(ctx, key)
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

	if err := g.fetchBlobs(ctx, request); err != nil {
		return blobObject{}, err
	}
	blob, ok := g.blobs[request]
	if !ok {
		return blobObject{}, fmt.Errorf("cat-file did not return result for %q", request)
	}
	return blob, nil
}

// prefetchCommits fills the commit cache in a single git log.
//
// Prefetch is best-effort: git log fails the whole batch when any SHA is missing, and callers
// still resolve present SHAs individually via getCommit. Trailer SHAs often point at commits
// that were never fetched into this clone.
func (g *EnvironmentOperations) prefetchCommits(ctx context.Context, shas ...string) {
	uncached := make([]string, 0, len(shas))
	seen := make(map[string]struct{}, len(shas))
	for _, sha := range shas {
		key := strings.ToLower(sha)
		if key == "" {
			continue
		}
		if _, ok := g.commits[key]; ok {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		uncached = append(uncached, key)
	}
	if len(uncached) == 0 {
		return
	}
	if err := g.fetchCommits(ctx, uncached...); err != nil {
		log.FromContext(ctx).V(4).Info("failed to prefetch commits, falling back to individual lookups", "error", err)
	}
}

// fetchCommits runs git log for the given SHAs and stores any records returned in g.commits.
// Invalid SHAs are skipped; absent SHAs simply produce no cache entry.
func (g *EnvironmentOperations) fetchCommits(ctx context.Context, shas ...string) error {
	gitPath := g.ClonePath()
	if gitPath == "" {
		return fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	revisions := make([]string, 0, len(shas))
	for _, sha := range shas {
		if fullObjectID.MatchString(sha) {
			revisions = append(revisions, sha)
		}
	}
	if len(revisions) == 0 {
		return nil
	}

	stdin := strings.NewReader(strings.Join(revisions, "\n") + "\n")
	stdout, _, err := runCmdWithEnvAndStdin(ctx, g.gap, gitPath, nil, stdin,
		"log", "--no-walk=unsorted", "--stdin", "-z", "--pretty=format:"+commitLogFormat)
	if err != nil {
		return fmt.Errorf("git log failed: %w", err)
	}

	results, err := parseCommitLogOutput(stdout)
	if err != nil {
		return err
	}
	maps.Copy(g.commits, results)
	return nil
}

// fetchBlobs runs git cat-file --batch for the given requests and stores results in g.blobs.
func (g *EnvironmentOperations) fetchBlobs(ctx context.Context, requests ...string) error {
	uncached := make([]string, 0, len(requests))
	seen := make(map[string]struct{}, len(requests))
	for _, req := range requests {
		if req == "" {
			continue
		}
		if _, ok := g.blobs[req]; ok {
			continue
		}
		if _, ok := seen[req]; ok {
			continue
		}
		seen[req] = struct{}{}
		uncached = append(uncached, req)
	}
	if len(uncached) == 0 {
		return nil
	}

	gitPath := g.ClonePath()
	if gitPath == "" {
		return fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	stdin := strings.NewReader(strings.Join(uncached, "\n") + "\n")
	stdout, _, err := runCmdWithEnvAndStdin(ctx, g.gap, gitPath, nil, stdin, "cat-file", "--batch")
	if err != nil {
		return fmt.Errorf("git cat-file --batch failed: %w", err)
	}

	results, err := parseCatFileBatch(strings.NewReader(stdout), uncached)
	if err != nil {
		return err
	}
	maps.Copy(g.blobs, results)
	return nil
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
		header = strings.TrimSuffix(header, "\n")

		if strings.HasSuffix(header, " missing") {
			results[req] = blobObject{Missing: true}
			continue
		}

		fields := strings.Fields(header)
		if len(fields) != 3 {
			return nil, fmt.Errorf("invalid cat-file header %q", header)
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("invalid cat-file size %q in header %q: %w", fields[2], header, err)
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
