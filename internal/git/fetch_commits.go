package git

import (
	"context"
	"fmt"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
)

// fetchCommitsBatchSize limits how many SHAs are passed per git fetch invocation.
const fetchCommitsBatchSize = 64

// FetchCommitsFromOrigin batch-fetches commit objects from origin.
// Callers use this to avoid per-SHA lazy promisor resolution during history rebuild.
//
// Do not probe missing SHAs with cat-file -e first: on blob-less clones that triggers one
// promisor fetch per SHA. This reconcile's in-memory commit cache is the only skip signal.
//
// Read-only with respect to the clone's index/worktree/HEAD; updates refs/objects only.
func (g *EnvironmentOperations) FetchCommitsFromOrigin(ctx context.Context, shas ...string) error {
	gitPath := g.ClonePath()
	if gitPath == "" {
		return fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	need := make([]string, 0, len(shas))
	seen := make(map[string]struct{}, len(shas))
	for _, sha := range shas {
		key := strings.ToLower(strings.TrimSpace(sha))
		if key == "" || !fullObjectID.MatchString(key) {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if _, ok := g.commits[key]; ok {
			continue
		}
		need = append(need, key)
	}
	if len(need) == 0 {
		return nil
	}

	logger := log.FromContext(ctx)
	for start := 0; start < len(need); start += fetchCommitsBatchSize {
		end := start + fetchCommitsBatchSize
		if end > len(need) {
			end = len(need)
		}
		batch := need[start:end]
		fetchArgs := make([]string, 0, 2+len(batch))
		fetchArgs = append(fetchArgs, "fetch", "origin")
		for _, sha := range batch {
			// Explicit refspecs fetch loose commits on shallow/partial clones; bare SHAs alone can be no-ops.
			fetchArgs = append(fetchArgs, "+"+sha+":refs/promoter/history-prefetch/"+sha)
		}

		fetchStart := time.Now()
		_, stderr, err := g.runCmd(ctx, gitPath, fetchArgs...)
		metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationFetch, metrics.GitOperationResultFromError(err), time.Since(fetchStart))
		if err != nil {
			logger.V(4).Info("git fetch for commit objects failed", "count", len(batch), "stderr", stderr, "error", err)
			return fmt.Errorf("git fetch commit objects failed: %w", err)
		}
		logger.V(4).Info("Fetched commit objects from origin", "count", len(batch))
	}
	return nil
}
