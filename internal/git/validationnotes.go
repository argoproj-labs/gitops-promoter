package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/validations"
)

// PromoterValidationsNotesRef is the git notes reference that holds validations: records, on a dry
// commit, that the commit may deploy to an environment. Git is the record; the ValidatedCommitStatus and
// CommitValidation resources only mirror it in status. The ref is its own commit history, which gives
// the order validations were written in.
const PromoterValidationsNotesRef = "refs/notes/promoter.validations"

// appendValidationsMaxAttempts bounds the fetch/append/push retry loop in AppendValidations. It is higher
// than the promotion-history limit because every gate and every CommitValidation of a repository writes
// to this one ref, and so do people.
const appendValidationsMaxAttempts = 6

// ValidationEntry is one validation to write: Line goes into the note on the commit Sha.
type ValidationEntry struct {
	Sha  string
	Line validations.Line
}

// ValidationEvent is one validation read back from PromoterValidationsNotesRef.
type ValidationEvent struct {
	// Time is the author date of the notes commit that added the line.
	Time time.Time
	// Sha is the dry commit that was validated.
	Sha string
	// NotesCommit is the commit of the notes ref that added the line.
	NotesCommit string
	// Author is the author email of that notes commit.
	Author string
	// Line is the validation as written.
	Line validations.Line
}

// By names who or what made the validation: what the line says, or else whoever wrote the note.
func (e ValidationEvent) By() string {
	if e.Line.By != "" {
		return e.Line.By
	}
	return e.Author
}

// At is when the validation was made: what the line says, or else when the note was written.
func (e ValidationEvent) At() time.Time {
	if !e.Line.At.IsZero() {
		return e.Line.At
	}
	return e.Time
}

// syncValidationNotes brings the local notes ref in line with the remote tip that was just listed and
// returns the resulting local tip, "" when the ref does not exist.
func (g *EnvironmentOperations) syncValidationNotes(ctx context.Context, remoteTip, localTip string) (string, error) {
	switch remoteTip {
	case "":
		if localTip == "" {
			return "", nil
		}
		return "", g.deleteLocalNotesRef(ctx, PromoterValidationsNotesRef)
	case localTip:
		return localTip, nil
	default:
		return g.FetchValidationNotes(ctx)
	}
}

// SyncValidationNotes brings the local PromoterValidationsNotesRef up to date with the remote and returns
// its tip, "" when the remote has no such ref. It asks the remote where the ref is and fetches only when
// it moved, so a caller that reads validations on every reconcile pays one round trip when nothing changed.
//
// Read-only with respect to the clone's index/worktree/HEAD.
func (g *EnvironmentOperations) SyncValidationNotes(ctx context.Context) (string, error) {
	if g.ClonePath() == "" {
		return "", fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}
	remote, err := lsRemoteRefs(ctx, g.gap, g.gitRepo, PromoterValidationsNotesRef)
	if err != nil {
		return "", fmt.Errorf("failed to list the validation notes: %w", err)
	}
	g.forgetLocalRef(PromoterValidationsNotesRef)
	local, err := g.localRefShas(ctx, PromoterValidationsNotesRef)
	if err != nil {
		return "", err
	}
	return g.syncValidationNotes(ctx, remote[PromoterValidationsNotesRef], local[PromoterValidationsNotesRef])
}

// FetchValidationNotes makes the local PromoterValidationsNotesRef equal to the remote one, discarding
// anything local, and returns its tip ("" when the remote has no such ref). The notes are fetched whole,
// blobs included, because their contents are read.
//
// Read-only with respect to the clone's index/worktree/HEAD.
func (g *EnvironmentOperations) FetchValidationNotes(ctx context.Context) (string, error) {
	gitPath := g.ClonePath()
	if gitPath == "" {
		return "", fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	start := time.Now()
	ref := PromoterValidationsNotesRef
	_, stderr, err := g.runCmd(ctx, gitPath, "fetch", "--no-tags", "--no-filter", "origin", "+"+ref+":"+ref)
	g.forgetLocalRef(ref)
	if err != nil {
		if strings.Contains(stderr, "couldn't find remote ref") {
			metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationFetchNotes, metrics.GitOperationResultSuccess, time.Since(start))
			return "", g.deleteLocalNotesRef(ctx, ref)
		}
		metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationFetchNotes, metrics.GitOperationResultFailure, time.Since(start))
		return "", fmt.Errorf("failed to fetch validation notes: %w (stderr: %s)", err, strings.TrimSpace(stderr))
	}
	metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationFetchNotes, metrics.GitOperationResultSuccess, time.Since(start))
	return g.validationNotesTip(ctx)
}

// validationNotesTip returns the local tip of PromoterValidationsNotesRef, "" when the ref does not exist.
func (g *EnvironmentOperations) validationNotesTip(ctx context.Context) (string, error) {
	g.forgetLocalRef(PromoterValidationsNotesRef)
	local, err := g.localRefShas(ctx, PromoterValidationsNotesRef)
	if err != nil {
		return "", err
	}
	return local[PromoterValidationsNotesRef], nil
}

// validationNote returns the text of the validation note on a commit, "" when it has none. Requires the
// notes to have been fetched.
func (g *EnvironmentOperations) validationNote(ctx context.Context, sha string) (string, error) {
	stdout, stderr, err := g.runCmdWithEnv(ctx, g.ClonePath(), noLazyFetchEnv, "notes", "--ref="+PromoterValidationsNotesRef, "show", sha)
	if err != nil {
		if strings.Contains(strings.ToLower(stderr), "no note found") {
			return "", nil
		}
		return "", fmt.Errorf("failed to read validation note for %q: %w (stderr: %s)", sha, err, strings.TrimSpace(stderr))
	}
	return stdout, nil
}

// AppendValidations records the given validations and pushes PromoterValidationsNotesRef to origin. Each
// one is appended as a line of its own to the note on its commit, so validations by other writers on the
// same commit are kept. It returns the tip of the notes ref afterwards.
//
// An entry whose Line.ID is already on its commit is skipped, which makes a retry after an uncertain
// push safe. The notes ref is shared by every writer of the repository, so a rejected push is fetched,
// appended again and retried. The validated commits do not need to be in the clone.
//
// Writes only the notes ref and its objects; never mutates the clone's index/worktree/HEAD.
func (g *EnvironmentOperations) AppendValidations(ctx context.Context, entries []ValidationEntry) (string, error) {
	gitPath := g.ClonePath()
	if gitPath == "" {
		return "", fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}
	logger := log.FromContext(ctx)
	ref := PromoterValidationsNotesRef

	texts := make([]string, len(entries))
	for i, entry := range entries {
		if !fullObjectID.MatchString(entry.Sha) {
			return "", fmt.Errorf("refusing to validate %q: not a full git object ID", entry.Sha)
		}
		text, err := entry.Line.Marshal()
		if err != nil {
			return "", fmt.Errorf("failed to write validation for %q: %w", entry.Sha, err)
		}
		texts[i] = text
	}

	var lastErr error
	for attempt := 1; attempt <= appendValidationsMaxAttempts; attempt++ {
		// Force-fetch deliberately discards any local note state, so the appends below go on top of the
		// latest remote notes.
		tip, err := g.FetchValidationNotes(ctx)
		if err != nil {
			return "", err
		}

		appended := 0
		for i, entry := range entries {
			recorded, err := g.validationRecorded(ctx, entry)
			if err != nil {
				return "", err
			}
			if recorded {
				continue
			}
			_, stderr, err := g.runCmdWithEnv(ctx, gitPath, noLazyFetchEnv, "notes", "--ref="+ref, "append", "-m", texts[i], entry.Sha)
			g.forgetLocalRef(ref)
			if err != nil {
				g.dropUnpushedValidations(ctx)
				return "", fmt.Errorf("failed to append validation note for %q: %w (stderr: %s)", entry.Sha, err, strings.TrimSpace(stderr))
			}
			appended++
		}
		if appended == 0 {
			return tip, nil
		}

		start := time.Now()
		stdout, stderr, err := g.runCmd(ctx, gitPath, "push", "--porcelain", "origin", ref+":"+ref)
		metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationPushNotes, metrics.GitOperationResultFromError(err), time.Since(start))
		g.forgetRemoteRef(ref)
		if err == nil {
			return g.validationNotesTip(ctx)
		}

		reason := porcelainRejection(stdout, ref)
		lastErr = fmt.Errorf("failed to push validation notes: %w (%s)", err, strings.TrimSpace(reason+" "+stderr))
		if !isRetryableNotesRejection(reason, stderr) {
			break
		}
		logger.V(4).Info("Validation notes push rejected, retrying", "attempt", attempt, "reason", reason)
		if attempt < appendValidationsMaxAttempts {
			// Jittered so writers that collided once do not collide again in lockstep.
			delay := wait.Jitter(time.Duration(attempt)*setHistoryNoteRetryBaseDelay, 1.0)
			select {
			case <-ctx.Done():
				g.dropUnpushedValidations(ctx)
				return "", fmt.Errorf("failed to push validation notes: %w", ctx.Err())
			case <-time.After(delay):
			}
		}
	}

	g.dropUnpushedValidations(ctx)
	return "", lastErr
}

// validationRecorded reports whether the entry's ID is already on its commit's note.
func (g *EnvironmentOperations) validationRecorded(ctx context.Context, entry ValidationEntry) (bool, error) {
	if entry.Line.ID == "" {
		return false, nil
	}
	note, err := g.validationNote(ctx, entry.Sha)
	if err != nil {
		return false, err
	}
	for _, line := range validations.ParseNote(note) {
		if line.ID == entry.Line.ID {
			return true, nil
		}
	}
	return false, nil
}

// dropUnpushedValidations resets the local notes ref to the remote after a failed write. A note that
// never reached the remote would otherwise be read back as a validation.
func (g *EnvironmentOperations) dropUnpushedValidations(ctx context.Context) {
	if _, err := g.FetchValidationNotes(context.WithoutCancel(ctx)); err != nil {
		log.FromContext(ctx).V(4).Info("Failed to reset validation notes after a failed write", "error", err)
	}
}

// isRetryableNotesRejection reports whether a rejected notes push lost a race with another writer, as
// opposed to being refused. reason is git's own verdict for the ref when it gave one.
func isRetryableNotesRejection(reason, stderr string) bool {
	if reason == "" {
		return isRetryableHistoryNotePushStderr(stderr)
	}
	if strings.HasPrefix(reason, "[rejected]") {
		// "fetch first" or "non-fast-forward": the remote ref moved.
		return true
	}
	// "[remote rejected]" covers both a ref that moved while it was being updated and a server that
	// refuses the push outright. Only the first is worth trying again.
	return strings.Contains(reason, "lock") || strings.Contains(reason, "failed to update ref")
}

// validationLogFormat is one header line per notes commit: a record separator, then the commit, its
// author email and its author date, separated by NUL. The raw diff entries follow on lines of their own.
const (
	validationLogRecordSep = "\x01"
	validationLogFormat    = "%x01%H%x00%ae%x00%aI"
)

// ListValidations returns the validations written after the notes commit since, oldest first: the order
// they were written in, taken from the history of PromoterValidationsNotesRef. An empty since lists from
// the beginning of that history. A limit greater than zero keeps only the validations of the newest
// limit notes commits.
//
// ok is false when since is not part of the notes history any more (the ref was rewritten, or the
// commit never reached the remote); nothing is listed then. Requires the notes to have been fetched.
//
// Only lines a commit added are validations: a line that was already there, was removed, or moved
// because git re-arranged the notes tree is not. Merge commits are skipped, since each side of a merge
// lists its own lines.
//
// Read-only: never mutates the clone's index/worktree/HEAD.
func (g *EnvironmentOperations) ListValidations(ctx context.Context, since string, limit int) (events []ValidationEvent, ok bool, err error) {
	gitPath := g.ClonePath()
	if gitPath == "" {
		return nil, false, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}
	tip, err := g.validationNotesTip(ctx)
	if err != nil {
		return nil, false, err
	}
	if tip == "" {
		return nil, since == "", nil
	}

	revRange := tip
	if since != "" {
		if !g.HasCommit(ctx, since) {
			return nil, false, nil
		}
		contained, err := g.ContainsCommit(ctx, tip, since)
		if err != nil {
			return nil, false, err
		}
		if !contained {
			return nil, false, nil
		}
		if since == tip {
			return nil, true, nil
		}
		revRange = since + ".." + tip
	}

	args := []string{
		"log", "--no-merges", "--date-order", "--reverse", "--root", "--raw", "--no-abbrev", "--no-renames",
		"--no-color", "--format=" + validationLogFormat,
	}
	if limit > 0 {
		args = append(args, "--max-count="+strconv.Itoa(limit))
	}
	args = append(args, revRange, "--")
	stdout, stderr, err := g.runCmdWithEnv(ctx, gitPath, noLazyFetchEnv, args...)
	if err != nil {
		return nil, false, fmt.Errorf("failed to list validation notes: %w (stderr: %s)", err, strings.TrimSpace(stderr))
	}

	commits, err := parseValidationLog(stdout)
	if err != nil {
		return nil, false, err
	}
	var blobs []string
	for _, commit := range commits {
		for _, change := range commit.changes {
			blobs = append(blobs, change.oldBlob, change.newBlob)
		}
	}
	if err := g.fetchBlobs(ctx, blobs...); err != nil {
		return nil, false, fmt.Errorf("failed to read validation notes: %w", err)
	}

	for _, commit := range commits {
		for _, change := range commit.changes {
			added := addedLines(string(g.blobs[change.oldBlob].Data), string(g.blobs[change.newBlob].Data))
			for _, text := range added {
				line, isValidation := validations.ParseLine(text)
				if !isValidation {
					continue
				}
				events = append(events, ValidationEvent{
					Sha:         change.sha,
					NotesCommit: commit.sha,
					Author:      commit.author,
					Time:        commit.time,
					Line:        line,
				})
			}
		}
	}
	return events, true, nil
}

// validationLogCommit is one commit of the notes ref and the notes it changed.
type validationLogCommit struct {
	time    time.Time
	sha     string
	author  string
	changes []validationNoteChange
}

// validationNoteChange is the note of one annotated commit before and after a notes commit. A blob is
// "" when the note did not exist on that side.
type validationNoteChange struct {
	sha     string
	oldBlob string
	newBlob string
}

// parseValidationLog parses git log --raw output written with validationLogFormat.
func parseValidationLog(stdout string) ([]validationLogCommit, error) {
	var commits []validationLogCommit
	for record := range strings.SplitSeq(stdout, validationLogRecordSep) {
		if strings.TrimSpace(record) == "" {
			continue
		}
		header, body, _ := strings.Cut(record, "\n")
		fields := strings.Split(header, "\x00")
		if len(fields) != 3 {
			return nil, fmt.Errorf("unexpected validation notes log header %q", header)
		}
		authorTime, err := time.Parse(time.RFC3339, fields[2])
		if err != nil {
			return nil, fmt.Errorf("parse author date %q of notes commit %q: %w", fields[2], fields[0], err)
		}
		commit := validationLogCommit{sha: fields[0], author: fields[1], time: authorTime}

		// When git re-arranges the notes tree (it fans notes out into directories as they grow in
		// number), a note shows up as deleted at one path and added at another. Collecting both sides
		// per annotated commit turns that back into "unchanged".
		index := map[string]int{}
		for line := range strings.SplitSeq(body, "\n") {
			change, ok := parseRawNoteEntry(line)
			if !ok {
				continue
			}
			i, seen := index[change.sha]
			if !seen {
				index[change.sha] = len(commit.changes)
				commit.changes = append(commit.changes, change)
				continue
			}
			if change.oldBlob != "" {
				commit.changes[i].oldBlob = change.oldBlob
			}
			if change.newBlob != "" {
				commit.changes[i].newBlob = change.newBlob
			}
		}
		commits = append(commits, commit)
	}
	return commits, nil
}

// parseRawNoteEntry parses one raw diff line of a notes commit,
// ":<old mode> <new mode> <old blob> <new blob> <status>\t<path>". The path is the annotated object's ID,
// possibly split into directories.
func parseRawNoteEntry(line string) (validationNoteChange, bool) {
	if !strings.HasPrefix(line, ":") {
		return validationNoteChange{}, false
	}
	meta, path, found := strings.Cut(line, "\t")
	fields := strings.Fields(meta)
	if !found || len(fields) != 5 {
		return validationNoteChange{}, false
	}
	sha := strings.ReplaceAll(path, "/", "")
	if !fullObjectID.MatchString(sha) {
		return validationNoteChange{}, false
	}
	change := validationNoteChange{sha: sha}
	if strings.Trim(fields[2], "0") != "" {
		change.oldBlob = fields[2]
	}
	if strings.Trim(fields[3], "0") != "" {
		change.newBlob = fields[3]
	}
	return change, true
}

// addedLines returns the lines of updated that are not accounted for by a line of original, in order. A
// line that appears twice in updated and once in original is new once.
func addedLines(original, updated string) []string {
	remaining := map[string]int{}
	for line := range strings.SplitSeq(original, "\n") {
		remaining[line]++
	}
	var added []string
	for line := range strings.SplitSeq(updated, "\n") {
		if remaining[line] > 0 {
			remaining[line]--
			continue
		}
		added = append(added, line)
	}
	return added
}
