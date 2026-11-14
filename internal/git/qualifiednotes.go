package git

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
)

// QualifiedNote is the content of a PromoterQualifiedNotesRef note on a hydrated commit. One commit can carry
// entries for several environments, because environments that share an active branch share its commits.
type QualifiedNote struct {
	// Environments maps an environment's note key to what that environment qualified on this commit.
	Environments map[string]QualifiedNoteEntry `json:"environments"`
}

// QualifiedNoteEntry records that an environment ran DrySha with every active commit status successful.
type QualifiedNoteEntry struct {
	// QualifiedAt is when the controller observed the environment successful on DrySha.
	QualifiedAt metav1.Time `json:"qualifiedAt"`
	// DrySha is the dry commit that was live and successful.
	DrySha string `json:"drySha"`
	// CommitStatuses are the active commit statuses observed at that moment.
	CommitStatuses []QualifiedNoteCommitStatus `json:"commitStatuses,omitempty"`
}

// QualifiedNoteCommitStatus is one active commit status recorded in a QualifiedNoteEntry.
type QualifiedNoteCommitStatus struct {
	Key   string `json:"key"`
	Phase string `json:"phase"`
}

// FetchQualifiedNotes brings PromoterQualifiedNotesRef up to date with origin.
func (g *EnvironmentOperations) FetchQualifiedNotes(ctx context.Context) error {
	return g.fetchNotesRef(ctx, PromoterQualifiedNotesRef)
}

// GetQualifiedNote reads the qualification note on a hydrated commit. It returns nil, nil when the commit has
// no note or the note cannot be parsed. Requires FetchQualifiedNotes to have run.
//
// Read-only: never mutates the clone's index/worktree/HEAD.
func (g *EnvironmentOperations) GetQualifiedNote(ctx context.Context, sha string) (*QualifiedNote, error) {
	gitPath := g.ClonePath()
	if gitPath == "" {
		return nil, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}
	stdout, stderr, err := g.runCmd(ctx, gitPath, "notes", "--ref="+PromoterQualifiedNotesRef, "show", sha)
	if err != nil {
		if strings.Contains(strings.ToLower(stderr), "no note found") {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read qualification note for sha %q: %w (stderr: %s)", sha, err, stderr)
	}
	var note QualifiedNote
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &note); err != nil {
		log.FromContext(ctx).V(4).Info("Failed to parse qualification note as JSON, ignoring", "sha", sha, "error", err)
		return nil, nil
	}
	return &note, nil
}

// SetQualifiedNoteEntry records entry under key in the qualification note on the given hydrated commit and
// pushes PromoterQualifiedNotesRef to origin. Entries under other keys are kept. The notes ref is shared by
// every writer of the repository, so a rejected push is retried on top of the latest remote notes.
//
// Writes only the notes ref and its objects; never mutates the clone's index/worktree/HEAD.
func (g *EnvironmentOperations) SetQualifiedNoteEntry(ctx context.Context, sha, key string, entry QualifiedNoteEntry) error {
	gitPath := g.ClonePath()
	if gitPath == "" {
		return fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	var lastErr error
	for attempt := 1; attempt <= setQualifiedNoteMaxAttempts; attempt++ {
		// Force-fetch deliberately discards local note state so the merge below starts from the latest
		// remote notes.
		if err := g.fetchNotesRef(ctx, PromoterQualifiedNotesRef); err != nil {
			return err
		}
		note, err := g.GetQualifiedNote(ctx, sha)
		if err != nil {
			return err
		}
		if note == nil {
			note = &QualifiedNote{}
		}
		if note.Environments == nil {
			note.Environments = map[string]QualifiedNoteEntry{}
		}
		note.Environments[key] = entry
		payload, err := json.Marshal(note)
		if err != nil {
			return fmt.Errorf("failed to marshal qualification note: %w", err)
		}

		_, stderr, err := g.runCmd(ctx, gitPath, "notes", "--ref="+PromoterQualifiedNotesRef, "add", "-f", "-m", string(payload), sha)
		g.forgetLocalRef(PromoterQualifiedNotesRef)
		if err != nil {
			return fmt.Errorf("failed to add qualification note for sha %q: %w (stderr: %s)", sha, err, stderr)
		}

		start := time.Now()
		_, stderr, err = g.runCmd(ctx, gitPath, "push", "origin", PromoterQualifiedNotesRef+":"+PromoterQualifiedNotesRef)
		metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationPushNotes, metrics.GitOperationResultFromError(err), time.Since(start))
		g.forgetRemoteRef(PromoterQualifiedNotesRef)
		if err == nil {
			return nil
		}
		lastErr = fmt.Errorf("failed to push qualification note for sha %q: %w (stderr: %s)", sha, err, strings.TrimSpace(stderr))
		if !isRetryableHistoryNotePushStderr(stderr) {
			break
		}
		if attempt < setQualifiedNoteMaxAttempts {
			// Jittered so writers that collided once do not collide again in lockstep.
			delay := wait.Jitter(time.Duration(attempt)*setHistoryNoteRetryBaseDelay, 1.0)
			select {
			case <-ctx.Done():
				return fmt.Errorf("failed to push qualification note for sha %q: %w", sha, ctx.Err())
			case <-time.After(delay):
			}
		}
	}

	// The local ref holds a note that never reached the remote. Drop it so it is not read back as written.
	if err := g.fetchNotesRef(ctx, PromoterQualifiedNotesRef); err != nil {
		log.FromContext(ctx).V(4).Info("Failed to reset qualification notes after a failed push", "error", err)
	}
	return lastErr
}

// setQualifiedNoteMaxAttempts bounds SetQualifiedNoteEntry's retries. It is higher than the promotion-history
// limit because every environment of every gate on a repository writes to the one qualification notes ref.
const setQualifiedNoteMaxAttempts = 6

// FindNewestQualifiedNoteEntry walks at most maxCommits first-parent commits from startRev and returns the
// first qualification entry recorded under key, with the hydrated commit it was found on. It returns nil, "",
// nil when there is none. Requires FetchQualifiedNotes to have run and startRev's commits to be present.
//
// Read-only: never mutates the clone's index/worktree/HEAD.
func (g *EnvironmentOperations) FindNewestQualifiedNoteEntry(ctx context.Context, startRev, key string, maxCommits int) (*QualifiedNoteEntry, string, error) {
	noted, err := g.notedObjects(ctx, PromoterQualifiedNotesRef)
	if err != nil {
		return nil, "", err
	}
	if len(noted) == 0 {
		return nil, "", nil
	}
	commits, err := g.GetRevListFirstParent(ctx, startRev, maxCommits)
	if err != nil {
		return nil, "", err
	}
	for _, commit := range commits {
		if _, ok := noted[commit]; !ok {
			continue
		}
		note, err := g.GetQualifiedNote(ctx, commit)
		if err != nil {
			return nil, "", err
		}
		if note == nil {
			continue
		}
		if entry, ok := note.Environments[key]; ok {
			return &entry, commit, nil
		}
	}
	return nil, "", nil
}
