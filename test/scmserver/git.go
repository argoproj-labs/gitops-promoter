package scmserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const zeroSHA = "0000000000000000000000000000000000000000"

// minGitVersion is the oldest git that supports `git merge-tree --write-tree`, which merges rely on.
var minGitVersion = [2]int{2, 38}

// signature is a git author or committer identity.
type signature struct {
	when  time.Time
	name  string
	email string
}

// gitCLI runs git commands against bare repositories under root.
type gitCLI struct {
	binary string
	root   string
}

func newGitCLI(ctx context.Context, root string) (*gitCLI, error) {
	binary, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("git is required on PATH: %w", err)
	}
	g := &gitCLI{binary: binary, root: root}
	out, err := g.run(ctx, "", nil, nil, "version")
	if err != nil {
		return nil, err
	}
	if err := checkGitVersion(out); err != nil {
		return nil, err
	}
	return g, nil
}

var gitVersionRe = regexp.MustCompile(`git version (\d+)\.(\d+)`)

func checkGitVersion(versionOutput string) error {
	m := gitVersionRe.FindStringSubmatch(versionOutput)
	if m == nil {
		return fmt.Errorf("cannot parse %q", strings.TrimSpace(versionOutput))
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	if major < minGitVersion[0] || (major == minGitVersion[0] && minor < minGitVersion[1]) {
		return fmt.Errorf("git %d.%d or newer is required, found %s.%s", minGitVersion[0], minGitVersion[1], m[1], m[2])
	}
	return nil
}

// baseEnv isolates git from the user's and system configuration.
func (g *gitCLI) baseEnv() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + g.root,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
	}
}

// run executes git in dir and returns trimmed stdout.
func (g *gitCLI) run(ctx context.Context, dir string, env []string, stdin io.Reader, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, g.binary, args...)
	cmd.Dir = dir
	cmd.Env = append(g.baseEnv(), env...)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), &gitError{args: args, stderr: strings.TrimSpace(stderr.String()), err: err}
	}
	return strings.TrimSpace(stdout.String()), nil
}

type gitError struct {
	err    error
	stderr string
	args   []string
}

func (e *gitError) Error() string {
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.args, " "), e.err, e.stderr)
}

func (e *gitError) Unwrap() error { return e.err }

// exitCode returns the git exit code of err, or -1 when err is not a git exit error.
func exitCode(err error) int {
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode()
	}
	return -1
}

func identityEnv(author, committer signature) []string {
	return []string{
		"GIT_AUTHOR_NAME=" + author.name,
		"GIT_AUTHOR_EMAIL=" + author.email,
		"GIT_AUTHOR_DATE=" + author.when.UTC().Format(time.RFC3339),
		"GIT_COMMITTER_NAME=" + committer.name,
		"GIT_COMMITTER_EMAIL=" + committer.email,
		"GIT_COMMITTER_DATE=" + committer.when.UTC().Format(time.RFC3339),
	}
}

// initBare creates a bare repository at dir with the given default branch.
func (g *gitCLI) initBare(ctx context.Context, dir, defaultBranch string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
		return fmt.Errorf("create repository parent directory: %w", err)
	}
	if _, err := g.run(ctx, "", nil, nil, "init", "--bare", "--initial-branch="+defaultBranch, dir); err != nil {
		return err
	}
	for _, kv := range [][2]string{
		{"http.receivepack", "true"},
		{"uploadpack.allowReachableSHA1InWant", "true"},
		{"uploadpack.allowFilter", "true"},
		{"receive.advertisePushOptions", "true"},
		{"core.logAllRefUpdates", "false"},
	} {
		if _, err := g.run(ctx, dir, nil, nil, "config", kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

// refs returns every ref in the repository mapped to the object it points to.
func (g *gitCLI) refs(ctx context.Context, dir string) (map[string]string, error) {
	out, err := g.run(ctx, dir, nil, nil, "for-each-ref", "--format=%(objectname) %(refname)")
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	for line := range strings.SplitSeq(out, "\n") {
		sha, ref, ok := strings.Cut(line, " ")
		if ok {
			refs[ref] = sha
		}
	}
	return refs, nil
}

// resolve returns the full SHA of rev, or errNotFound when it does not exist.
func (g *gitCLI) resolve(ctx context.Context, dir, rev string) (string, error) {
	out, err := g.run(ctx, dir, nil, nil, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		if exitCode(err) == 1 {
			return "", errNotFound
		}
		return "", err
	}
	return out, nil
}

// branchSHA returns the commit at refs/heads/branch, or errNotFound.
func (g *gitCLI) branchSHA(ctx context.Context, dir, branch string) (string, error) {
	return g.resolve(ctx, dir, "refs/heads/"+branch)
}

// updateRef moves ref to newSHA if it currently points at oldSHA ("" means the ref must not exist).
func (g *gitCLI) updateRef(ctx context.Context, dir, ref, newSHA, oldSHA string) error {
	if oldSHA == "" {
		oldSHA = zeroSHA
	}
	_, err := g.run(ctx, dir, nil, nil, "update-ref", ref, newSHA, oldSHA)
	return err
}

// isAncestor reports whether ancestor is reachable from descendant.
func (g *gitCLI) isAncestor(ctx context.Context, dir, ancestor, descendant string) (bool, error) {
	_, err := g.run(ctx, dir, nil, nil, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	if exitCode(err) == 1 {
		return false, nil
	}
	return false, err
}

// commitInfo describes a commit for webhook payloads and API responses.
type commitInfo struct {
	when        time.Time
	sha         string
	message     string
	authorName  string
	authorEmail string
}

// log returns up to limit commits reachable from to and not from from (from may be empty), newest first.
func (g *gitCLI) log(ctx context.Context, dir, from, to string, limit int) ([]commitInfo, error) {
	rangeSpec := to
	if from != "" && from != zeroSHA {
		rangeSpec = from + ".." + to
	}
	out, err := g.run(ctx, dir, nil, nil, "log", "-n", strconv.Itoa(limit), "--format=%H%x00%an%x00%ae%x00%aI%x00%B%x1e", rangeSpec)
	if err != nil {
		return nil, err
	}
	var commits []commitInfo
	for record := range strings.SplitSeq(out, "\x1e") {
		fields := strings.SplitN(strings.TrimLeft(record, "\n"), "\x00", 5)
		if len(fields) != 5 {
			continue
		}
		when, _ := time.Parse(time.RFC3339, fields[3])
		commits = append(commits, commitInfo{
			sha: fields[0], authorName: fields[1], authorEmail: fields[2], when: when, message: strings.TrimSpace(fields[4]),
		})
	}
	return commits, nil
}

// commitFiles describes a commit written by the admin API.
type commitFiles struct {
	author  signature
	files   map[string]string
	message string
	parent  string
	delete  []string
	orphan  bool
}

// writeCommit creates a commit on top of parent (or with no parent for an orphan) with the given file changes and
// returns its SHA. It does not move any ref.
func (g *gitCLI) writeCommit(ctx context.Context, dir string, c commitFiles) (string, error) {
	tmp, err := os.MkdirTemp("", "scmserver-index-*")
	if err != nil {
		return "", fmt.Errorf("create temp index dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}

	if c.orphan || c.parent == "" {
		if _, err := g.run(ctx, dir, env, nil, "read-tree", "--empty"); err != nil {
			return "", err
		}
	} else if _, err := g.run(ctx, dir, env, nil, "read-tree", c.parent); err != nil {
		return "", err
	}

	paths := make([]string, 0, len(c.files))
	for p := range c.files {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		blob, err := g.run(ctx, dir, nil, strings.NewReader(c.files[p]), "hash-object", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		if _, err := g.run(ctx, dir, env, nil, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+p); err != nil {
			return "", err
		}
	}
	for _, p := range c.delete {
		if _, err := g.run(ctx, dir, env, nil, "update-index", "--force-remove", p); err != nil {
			return "", err
		}
	}
	tree, err := g.run(ctx, dir, env, nil, "write-tree")
	if err != nil {
		return "", err
	}
	var parents []string
	if !c.orphan && c.parent != "" {
		parents = []string{c.parent}
	}
	return g.commitTree(ctx, dir, tree, parents, c.message, c.author, c.author)
}

// commitTree creates a commit object for tree with the given parents and returns its SHA.
func (g *gitCLI) commitTree(ctx context.Context, dir, tree string, parents []string, message string, author, committer signature) (string, error) {
	args := make([]string, 0, 2+2*len(parents))
	args = append(args, "commit-tree", tree)
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	return g.run(ctx, dir, identityEnv(author, committer), strings.NewReader(message), args...)
}

// addNote attaches a git note under refs/notes/<ref> to sha, replacing any existing note.
func (g *gitCLI) addNote(ctx context.Context, dir, ref, sha, content string, who signature) error {
	_, err := g.run(ctx, dir, identityEnv(who, who), nil, "notes", "--ref", ref, "add", "-f", "-m", content, sha)
	return err
}

// serveSmartHTTP serves a git smart HTTP request for the bare repository at relDir (relative to the git root) using
// git-http-backend. rest is the part of the URL after the repository path, such as "/info/refs".
func (g *gitCLI) serveSmartHTTP(w http.ResponseWriter, r *http.Request, relDir, rest, remoteUser string) {
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/" + filepath.ToSlash(relDir) + rest
	r2.URL.RawPath = ""
	handler := &cgi.Handler{
		Path: g.binary,
		Root: "/",
		Args: []string{"http-backend"},
		Env: append(g.baseEnv(),
			"GIT_PROJECT_ROOT="+g.root,
			"GIT_HTTP_EXPORT_ALL=1",
			"REMOTE_USER="+remoteUser,
		),
		Stderr: io.Discard,
	}
	handler.ServeHTTP(w, r2)
}

// gitRequest is a parsed git smart HTTP request path.
type gitRequest struct {
	// repoPath is the path of the repository as it appears in the URL, without a leading slash or a ".git" suffix.
	repoPath string
	// rest is the smart HTTP suffix, such as "/info/refs" or "/git-upload-pack".
	rest string
}

var gitSuffixes = []string{"/info/refs", "/git-upload-pack", "/git-receive-pack", "/HEAD"}

// parseGitRequest recognizes git smart HTTP URLs.
func parseGitRequest(urlPath string) (gitRequest, bool) {
	for _, suffix := range gitSuffixes {
		if prefix, ok := strings.CutSuffix(urlPath, suffix); ok && prefix != "" {
			repoPath := strings.TrimSuffix(strings.Trim(prefix, "/"), ".git")
			return gitRequest{repoPath: repoPath, rest: suffix}, true
		}
	}
	return gitRequest{}, false
}

// refChange is a ref update observed around a git write.
type refChange struct {
	ref    string
	before string
	after  string
}

// diffRefs returns the branch and tag updates between two ref snapshots, sorted by ref name.
func diffRefs(before, after map[string]string) []refChange {
	var changes []refChange
	seen := map[string]bool{}
	for ref, newSHA := range after {
		seen[ref] = true
		if old := before[ref]; old != newSHA {
			changes = append(changes, refChange{ref: ref, before: orZero(old), after: newSHA})
		}
	}
	for ref, old := range before {
		if !seen[ref] {
			changes = append(changes, refChange{ref: ref, before: old, after: zeroSHA})
		}
	}
	changes = slices.DeleteFunc(changes, func(c refChange) bool {
		return !strings.HasPrefix(c.ref, "refs/heads/") && !strings.HasPrefix(c.ref, "refs/tags/")
	})
	slices.SortFunc(changes, func(a, b refChange) int { return strings.Compare(a.ref, b.ref) })
	return changes
}

func orZero(sha string) string {
	if sha == "" {
		return zeroSHA
	}
	return sha
}
