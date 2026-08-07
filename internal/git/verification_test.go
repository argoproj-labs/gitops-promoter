package git_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
)

var _ = Describe("VerifyCommitRange", func() {
	var (
		remoteDir  string
		signerHome string
	)

	BeforeEach(func() {
		if _, err := exec.LookPath("gpg"); err != nil {
			Skip("gpg not installed")
		}
		var err error
		remoteDir, err = os.MkdirTemp("", "git-verify-remote-*")
		Expect(err).NotTo(HaveOccurred())
		signerHome, err = os.MkdirTemp("", "git-verify-gpg-*")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Chmod(signerHome, 0o700)).To(Succeed())
	})

	AfterEach(func() {
		_ = exec.CommandContext(context.Background(), "gpgconf", "--homedir", signerHome, "--kill", "all").Run()
		Expect(os.RemoveAll(remoteDir)).To(Succeed())
		Expect(os.RemoveAll(signerHome)).To(Succeed())
	})

	It("verifies a commit signed by a trusted key", func() {
		signer, pub := generateGPGKey(signerHome, "Trusted Signer", "trusted@example.com")
		sha := commitToRepo(remoteDir, signerHome, signer)

		sigs := verifyRange(remoteDir, "", sha, newKeyring(pub))
		Expect(sigs).To(HaveLen(1))
		Expect(sigs[0].SHA).To(Equal(sha))
		Expect(sigs[0].Verified).To(BeTrue())
		Expect(sigs[0].Type).To(Equal("gpg"))
		Expect(sigs[0].Signer).To(ContainSubstring("Trusted Signer"))
		Expect(sigs[0].KeyID).NotTo(BeEmpty())
	})

	It("does not verify when the trusted key does not match the signer", func() {
		signer, _ := generateGPGKey(signerHome, "Real Signer", "real@example.com")
		sha := commitToRepo(remoteDir, signerHome, signer)

		otherHome, err := os.MkdirTemp("", "git-verify-other-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = os.RemoveAll(otherHome) })
		Expect(os.Chmod(otherHome, 0o700)).To(Succeed())
		_, otherPub := generateGPGKey(otherHome, "Other Signer", "other@example.com")

		sigs := verifyRange(remoteDir, "", sha, newKeyring(otherPub))
		Expect(sigs).To(HaveLen(1))
		Expect(sigs[0].SignatureVerification).To(Equal(git.SignatureVerification{}),
			"an unverified signature must not report the key ID it claims")
	})

	It("reports an unsigned commit as unverified", func() {
		_, pub := generateGPGKey(signerHome, "Trusted Signer", "trusted@example.com")
		sha := commitToRepo(remoteDir, "", "")

		sigs := verifyRange(remoteDir, "", sha, newKeyring(pub))
		Expect(sigs).To(HaveLen(1))
		Expect(sigs[0].SignatureVerification).To(Equal(git.SignatureVerification{}))
	})

	It("reports unverified when no trusted keys are provided", func() {
		signer, _ := generateGPGKey(signerHome, "Trusted Signer", "trusted@example.com")
		sha := commitToRepo(remoteDir, signerHome, signer)

		sigs := verifyRange(remoteDir, "", sha, newKeyring())
		Expect(sigs).To(HaveLen(1))
		Expect(sigs[0].SignatureVerification).To(Equal(git.SignatureVerification{}))
	})

	It("verifies every commit the range adds, not just its tip", func() {
		signer, pub := generateGPGKey(signerHome, "Trusted Signer", "trusted@example.com")

		workDir := initWorkRepo(remoteDir)
		base := addCommit(workDir, signerHome, signer, "base")
		middle := addCommit(workDir, signerHome, signer, "middle")
		tip := addCommit(workDir, signerHome, signer, "tip")
		pushWorkRepo(workDir)

		sigs := verifyRange(remoteDir, base, tip, newKeyring(pub))
		Expect(sigs).To(HaveLen(2), "the range excludes its lower bound")
		Expect(shasOf(sigs)).To(Equal([]string{tip, middle}), "results are newest first")
		for _, sig := range sigs {
			Expect(sig.Verified).To(BeTrue())
		}
	})

	It("catches an unsigned commit hidden under a signed tip", func() {
		signer, pub := generateGPGKey(signerHome, "Trusted Signer", "trusted@example.com")

		workDir := initWorkRepo(remoteDir)
		base := addCommit(workDir, signerHome, signer, "base")
		sneaked := addCommit(workDir, "", "", "sneaked in unsigned")
		tip := addCommit(workDir, signerHome, signer, "signed tip")
		pushWorkRepo(workDir)

		keyring := newKeyring(pub)

		By("confirming tip-only verification would have passed")
		tipOnly := verifyRange(remoteDir, sneaked, tip, keyring)
		Expect(tipOnly).To(HaveLen(1))
		Expect(tipOnly[0].Verified).To(BeTrue())

		By("verifying the whole promotion range instead")
		sigs := verifyRange(remoteDir, base, tip, keyring)
		Expect(sigs).To(HaveLen(2))
		bySha := map[string]git.CommitSignature{}
		for _, sig := range sigs {
			bySha[sig.SHA] = sig
		}
		Expect(bySha[tip].Verified).To(BeTrue())
		Expect(bySha[sneaked].SignatureVerification).To(Equal(git.SignatureVerification{}),
			"the commit under the signed tip must be reported unverified")
	})

	It("refuses a range with no end commit rather than falling back to HEAD", func() {
		sha := commitToRepo(remoteDir, "", "")

		g := newVerifyOps(remoteDir)
		Expect(g.CloneRepo(GinkgoT().Context())).To(Succeed())
		_, err := g.VerifyCommitRange(GinkgoT().Context(), sha, "", newKeyring())
		Expect(err).To(MatchError(ContainSubstring("without an end commit")))
	})

	It("returns no results when the range adds nothing", func() {
		signer, pub := generateGPGKey(signerHome, "Trusted Signer", "trusted@example.com")
		sha := commitToRepo(remoteDir, signerHome, signer)

		Expect(verifyRange(remoteDir, sha, sha, newKeyring(pub))).To(BeEmpty())
	})
})

func verifyRange(remote, fromSha, toSha string, keyring *git.GPGKeyring) []git.CommitSignature {
	GinkgoHelper()
	g := newVerifyOps(remote)
	Expect(g.CloneRepo(GinkgoT().Context())).To(Succeed())
	sigs, err := g.VerifyCommitRange(GinkgoT().Context(), fromSha, toSha, keyring)
	Expect(err).NotTo(HaveOccurred())
	return sigs
}

func shasOf(sigs []git.CommitSignature) []string {
	shas := make([]string, 0, len(sigs))
	for _, sig := range sigs {
		shas = append(shas, sig.SHA)
	}
	return shas
}

func newKeyring(armoredKeys ...string) *git.GPGKeyring {
	GinkgoHelper()
	keyring, err := git.NewGPGKeyring(GinkgoT().Context(), armoredKeys)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(keyring.Close()).To(Succeed()) })
	return keyring
}

func newVerifyOps(remote string) *git.EnvironmentOperations {
	repo := &v1alpha1.GitRepository{
		Spec: v1alpha1.GitRepositorySpec{
			GitHub: &v1alpha1.GitHubRepo{Owner: "test-owner", Name: "verifyrepo"},
			ScmProviderRef: v1alpha1.ScmProviderObjectReference{
				Kind: "ScmProvider",
				Name: "testprovider",
			},
		},
		ObjectMeta: metav1.ObjectMeta{Name: "verifyrepo", Namespace: "default"},
	}
	gap := &fakeGitProvider{tempDirPath: remote}
	return git.NewEnvironmentOperations(repo, gap, "default/verifyrepo")
}

// generateGPGKey creates an unprotected signing key in home, returning the identity to sign with
// and its armored public key. gpg resolves a user id anywhere it accepts a fingerprint, so the
// email doubles as the key spec and no key listing has to be parsed.
func generateGPGKey(home, name, email string) (signer, armoredPublic string) {
	GinkgoHelper()
	params := "%no-protection\nKey-Type: eddsa\nKey-Curve: ed25519\nName-Real: " + name +
		"\nName-Email: " + email + "\nExpire-Date: 0\n%commit\n"
	_, stderr, err := gpgRun(home, params, "--gen-key")
	Expect(err).NotTo(HaveOccurred(), stderr)

	armoredPublic, stderr, err = gpgRun(home, "", "--armor", "--export", email)
	Expect(err).NotTo(HaveOccurred(), stderr)
	Expect(armoredPublic).NotTo(BeEmpty())
	return email, armoredPublic
}

func gpgRun(home, stdin string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(context.Background(), "gpg", append([]string{"--homedir", home, "--batch", "--no-tty"}, args...)...)
	cmd.Env = append(os.Environ(), "GNUPGHOME="+home)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	err := cmd.Run()
	return out.String(), errBuf.String(), err
}

// commitToRepo initializes remoteBare, commits a file, and pushes it, returning the commit SHA.
// A non-empty signerHome signs the commit with signer using that home as GNUPGHOME; empty leaves it unsigned.
func commitToRepo(remoteBare, signerHome, signer string) string {
	GinkgoHelper()
	workDir := initWorkRepo(remoteBare)
	sha := addCommit(workDir, signerHome, signer, "commit")
	pushWorkRepo(workDir)
	return sha
}

// initWorkRepo initializes remoteBare and returns a working clone of it, ready for addCommit.
func initWorkRepo(remoteBare string) string {
	GinkgoHelper()
	_, err := runGitCmd(remoteBare, "init", "--bare")
	Expect(err).NotTo(HaveOccurred())

	workDir, err := os.MkdirTemp("", "git-verify-work-*")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(workDir) })

	_, err = runGitCmd(workDir, "clone", remoteBare, ".")
	Expect(err).NotTo(HaveOccurred())
	mustGit(workDir, "config", "user.name", "Test User")
	mustGit(workDir, "config", "user.email", "test@example.com")
	return workDir
}

// addCommit commits a file whose content is msg, returning the commit SHA. A non-empty signerHome
// signs with signer using that home as GNUPGHOME; empty leaves the commit unsigned.
func addCommit(workDir, signerHome, signer, msg string) string {
	GinkgoHelper()
	Expect(os.WriteFile(filepath.Join(workDir, "file.txt"), []byte(msg), 0o644)).To(Succeed())
	mustGit(workDir, "add", "file.txt")

	if signerHome == "" {
		mustGit(workDir, "commit", "--no-gpg-sign", "-m", msg)
	} else {
		mustGit(workDir, "config", "user.signingkey", signer)
		cmd := exec.CommandContext(context.Background(), "git", "commit", "-S", "-m", msg)
		cmd.Dir = workDir
		cmd.Env = append(os.Environ(), "GNUPGHOME="+signerHome, "GIT_TERMINAL_PROMPT=0")
		out, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(out))
	}

	return strings.TrimSpace(mustGit(workDir, "rev-parse", "HEAD"))
}

func pushWorkRepo(workDir string) {
	GinkgoHelper()
	branch := strings.TrimSpace(mustGit(workDir, "rev-parse", "--abbrev-ref", "HEAD"))
	mustGit(workDir, "push", "origin", branch)
}
