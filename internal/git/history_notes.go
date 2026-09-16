package git

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

// historyNoteEntry is a cached promotion-history note for one commit SHA.
// A key is absent from historyNotes until LoadHistoryNotes or GetHistoryNote populates it.
type historyNoteEntry struct {
	trailers map[string][]string
	missing  bool
}

// LoadHistoryNotes prefetches promotion-history notes for the given commit SHAs into this
// instance's cache so GetHistoryNote serves them without one git subprocess per SHA.
//
// Read-only: never mutates the clone's index/worktree/HEAD. Requires FetchNotes to have run.
func (g *EnvironmentOperations) LoadHistoryNotes(ctx context.Context, shas ...string) error {
	if g.ClonePath() == "" {
		return fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	want := make(map[string]struct{})
	for _, sha := range shas {
		key := strings.ToLower(sha)
		if key == "" || !fullObjectID.MatchString(key) {
			continue
		}
		if _, ok := g.historyNotes[key]; ok {
			continue
		}
		want[key] = struct{}{}
	}
	if len(want) == 0 {
		return nil
	}

	noteBlobByCommit, err := g.listPromotionHistoryNoteBlobs(ctx)
	if err != nil {
		return err
	}

	blobSHAs := make([]string, 0, len(want))
	blobToCommit := make(map[string]string, len(want))
	for commitSHA := range want {
		noteBlob, ok := noteBlobByCommit[commitSHA]
		if !ok {
			g.historyNotes[commitSHA] = historyNoteEntry{missing: true}
			continue
		}
		blobSHAs = append(blobSHAs, noteBlob)
		blobToCommit[noteBlob] = commitSHA
	}

	if len(blobSHAs) > 0 {
		if err := g.fetchBlobs(ctx, blobSHAs...); err != nil {
			return err
		}
		for blobSHA, commitSHA := range blobToCommit {
			entry := historyNoteEntry{missing: true}
			blob, ok := g.blobs[blobSHA]
			if ok && !blob.Missing && len(blob.Data) > 0 {
				var trailers map[string][]string
				if err := json.Unmarshal(blob.Data, &trailers); err != nil {
					log.FromContext(ctx).V(4).Info("Failed to parse history note as JSON, ignoring", "sha", commitSHA, "error", err)
				} else {
					entry.trailers = trailers
					entry.missing = false
				}
			}
			g.historyNotes[commitSHA] = entry
		}
	}

	return nil
}

func (g *EnvironmentOperations) listPromotionHistoryNoteBlobs(ctx context.Context) (map[string]string, error) {
	gitPath := g.ClonePath()
	stdout, stderr, err := g.runCmd(ctx, gitPath, "notes", "--ref="+PromoterHistoryNotesRef, "list")
	if err != nil {
		if notesRefMissing(stderr) {
			log.FromContext(ctx).V(4).Info("Promotion history notes ref is not present locally", "stderr", stderr)
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("git notes list failed: %w", err)
	}

	out := make(map[string]string)
	for line := range strings.SplitSeq(strings.TrimSpace(stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		noteBlob, commitSHA, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		out[strings.ToLower(commitSHA)] = noteBlob
	}
	return out, nil
}

func notesRefMissing(stderr string) bool {
	lower := strings.ToLower(stderr)
	return strings.Contains(lower, "unknown ref") ||
		strings.Contains(lower, "not a valid ref") ||
		strings.Contains(lower, "no ref") ||
		strings.Contains(lower, "couldn't find")
}
