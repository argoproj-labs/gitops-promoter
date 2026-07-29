package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SignatureVerification is the result of checking a commit's signature against a trusted keyring.
// Every field is empty unless Verified: gpg reports the issuer a signature claims even when it holds
// no key to check that claim against, so an unverified signature names an attacker-chosen key ID.
type SignatureVerification struct {
	Type     string
	KeyID    string
	Signer   string
	Verified bool
}

// CommitSignature pairs a commit with the result of checking its signature.
type CommitSignature struct {
	SHA string
	SignatureVerification
}

// VerifyCommitRange verifies every commit reachable from toSha but not from fromSha — the commits a
// promotion would add — and returns one result per commit, newest first.
//
// An empty fromSha verifies toSha alone. That is the bootstrap case: an environment that has never
// been promoted has no previously-verified boundary, and walking to the repository root instead
// would verify the entire history on first use.
func (g *EnvironmentOperations) VerifyCommitRange(ctx context.Context, fromSha, toSha string, keyring *GPGKeyring) ([]CommitSignature, error) {
	gitPath := g.ClonePath()
	if gitPath == "" {
		return nil, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}
	if keyring == nil {
		return nil, fmt.Errorf("cannot verify range %q..%q without a keyring", fromSha, toSha)
	}

	// -z terminates each commit with NUL and %x00 separates the fields within one, so the output is a
	// flat NUL-terminated stream of 4 fields per commit. Parsing cannot be desynchronised by a signer
	// identity containing a newline, which a %n-separated format would allow.
	args := []string{"log", "-z", "--format=%H%x00%G?%x00%GK%x00%GS"}
	if fromSha == "" {
		args = append(args, toSha, "-1")
	} else {
		args = append(args, fromSha+".."+toSha)
	}
	args = append(args, "--")

	stdout, stderr, err := g.runCmdWithEnv(ctx, gitPath, []string{"GNUPGHOME=" + keyring.home}, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to read signatures for range %q..%q: %w (stderr: %s)", fromSha, toSha, err, stderr)
	}

	return parseCommitRangeSignatures(stdout)
}

// parseCommitRangeSignatures parses the NUL-delimited stream produced by VerifyCommitRange's
// `git log -z` format into one result per commit.
func parseCommitRangeSignatures(out string) ([]CommitSignature, error) {
	if out == "" {
		return nil, nil
	}

	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	if len(fields)%4 != 0 {
		return nil, fmt.Errorf("malformed signature listing: got %d fields, want a multiple of 4", len(fields))
	}

	signatures := make([]CommitSignature, 0, len(fields)/4)
	for i := 0; i < len(fields); i += 4 {
		signatures = append(signatures, CommitSignature{
			SHA:                   fields[i],
			SignatureVerification: parseSignatureFields(fields[i+1], fields[i+2], fields[i+3]),
		})
	}
	return signatures, nil
}

// GPGKeyring is an ephemeral GNUPGHOME holding the public keys that signatures are checked against.
// Build one per reconcile and reuse it across commits; callers must Close it.
//
// It is not safe for concurrent use, and it is not a clone: the home lives outside every worktree so
// it can never surface as an untracked file.
type GPGKeyring struct {
	home string
}

// git spawns gpg itself, so options guarding that invocation cannot be passed as flags and have to
// live in the keyring's config. Offline is the point: only the keys imported here may verify a
// signature, and a fetched key would report as "unknown validity", which counts as verified.
const gpgKeyringConfig = `no-autostart
no-auto-key-retrieve
auto-key-locate clear
`

// NewGPGKeyring creates an ephemeral GNUPGHOME and imports armoredKeys into it.
func NewGPGKeyring(ctx context.Context, armoredKeys []string) (*GPGKeyring, error) {
	home, err := os.MkdirTemp("", "promoter-gpg-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp GNUPGHOME: %w", err)
	}
	keyring := &GPGKeyring{home: home}

	if err := os.WriteFile(filepath.Join(home, "gpg.conf"), []byte(gpgKeyringConfig), 0o600); err != nil {
		_ = keyring.Close()
		return nil, fmt.Errorf("failed to write gpg.conf: %w", err)
	}

	for i, armored := range armoredKeys {
		cmd := exec.CommandContext(ctx, "gpg", "--homedir", home, "--batch", "--no-tty", "--no-autostart", "--import")
		cmd.Env = []string{"GNUPGHOME=" + home, "PATH=" + os.Getenv("PATH")}
		cmd.Stdin = strings.NewReader(armored)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			_ = keyring.Close()
			return nil, fmt.Errorf("failed to import key %d: %w (stderr: %s)", i, err, stderr.String())
		}
	}

	return keyring, nil
}

// Close removes the keyring from disk.
func (k *GPGKeyring) Close() error {
	if k == nil {
		return nil
	}
	if err := os.RemoveAll(k.home); err != nil {
		return fmt.Errorf("failed to remove temp GNUPGHOME %q: %w", k.home, err)
	}
	return nil
}

// parseSignatureFields builds a SignatureVerification from one commit's %G?, %GK and %GS values.
//
// %G? is 'U' (good, unknown validity) rather than 'G' whenever the signing key is imported without
// ownertrust, which is exactly our case: we trust a key by importing it, not by setting ownertrust.
// So both 'G' and 'U' count as verified; expired/revoked/bad/absent do not.
func parseSignatureFields(status, keyID, signer string) SignatureVerification {
	if status = strings.TrimSpace(status); status != "G" && status != "U" {
		return SignatureVerification{}
	}

	return SignatureVerification{
		Verified: true,
		Type:     "gpg",
		KeyID:    strings.TrimSpace(keyID),
		Signer:   strings.TrimSpace(signer),
	}
}
