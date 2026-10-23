/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	corev1 "k8s.io/api/core/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/argoproj-labs/gitops-promoter/test/scmserver"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	envSCMProvider = "PROMOTER_TEST_SCM_PROVIDER"
	envSCMAdminURL = "PROMOTER_TEST_SCM_ADMIN_URL"
	envSCMCAFile   = "PROMOTER_TEST_SCM_CA_FILE"
	envSCMToken    = "PROMOTER_TEST_SCM_TOKEN"

	defaultSCMAdminURL = "http://127.0.0.1:9000"
	defaultSCMCAFile   = "bin/scm-server-ca.pem"
	defaultSCMToken    = "dev-token"

	fakeSCMOnlyLabel = "fake-scm-only"

	// githubAppIDBase is the starting App ID for per-repo GitHub App registrations.
	// Each repo gets its own App so the provider's installation cache cannot poison later fixtures.
	githubAppIDBase int64 = 20000
	// githubAppIDsPerProcess partitions App IDs by Ginkgo process. Each process signs with its own
	// key and the server overwrites an App on re-registration, so shared IDs break JWT verification.
	githubAppIDsPerProcess int64 = 1_000_000
)

// scmTestTarget routes controller integration tests at either the in-process fake SCM + gitkit
// server (default) or an externally running test/scmserver provider selected via env vars.
type scmTestTarget struct {
	provider     scmserver.Provider // empty when using the fake provider
	admin        *scmserver.AdminClient
	domain       string
	token        string
	caFile       string
	githubKeyPEM []byte
	githubAppSeq atomic.Int64
	repoSeq      atomic.Int64
	// runID keeps repo names unique across runs against a long-lived scmserver, which may still
	// hold repos from an earlier run whose cleanup did not complete.
	runID string
}

// testRepo is the SCM-side identity of a repository created for a single test.
type testRepo struct {
	Owner       string
	Name        string
	Project     string // Azure DevOps project; empty for other providers
	ID          int64  // server-side repo / GitLab project ID; 0 in fake mode
	GitHubAppID int64  // non-zero only for GitHub server mode
	CloneURL    string // unauthenticated HTTPS clone URL from the server; empty in fake mode
}

var scmTarget *scmTestTarget

func (t *scmTestTarget) isFake() bool {
	return t == nil || t.provider == ""
}

func (t *scmTestTarget) sendsOwnWebhooks() bool {
	return !t.isFake()
}

// repoOwnerName returns the owner and name used for pull-request naming, matching
// changetransferpolicy_controller.go's switch over GitRepositorySpec provider fields.
func (t *scmTestTarget) repoOwnerName(gitRepo *promoterv1alpha1.GitRepository) (owner, name string) {
	GinkgoHelper()
	switch {
	case gitRepo.Spec.GitHub != nil:
		return gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name
	case gitRepo.Spec.GitLab != nil:
		return gitRepo.Spec.GitLab.Namespace, gitRepo.Spec.GitLab.Name
	case gitRepo.Spec.Forgejo != nil:
		return gitRepo.Spec.Forgejo.Owner, gitRepo.Spec.Forgejo.Name
	case gitRepo.Spec.Gitea != nil:
		return gitRepo.Spec.Gitea.Owner, gitRepo.Spec.Gitea.Name
	case gitRepo.Spec.Fake != nil:
		return gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name
	case gitRepo.Spec.BitbucketCloud != nil:
		return gitRepo.Spec.BitbucketCloud.Owner, gitRepo.Spec.BitbucketCloud.Name
	case gitRepo.Spec.BitbucketDataCenter != nil:
		return gitRepo.Spec.BitbucketDataCenter.Project, gitRepo.Spec.BitbucketDataCenter.Name
	case gitRepo.Spec.AzureDevOps != nil:
		return gitRepo.Spec.AzureDevOps.Project, gitRepo.Spec.AzureDevOps.Name
	default:
		Fail("GitRepository has no provider-specific repo fields set")
		return "", ""
	}
}

// prName returns the pull-request name the controller would assign for the given branches.
func (t *scmTestTarget) prName(gitRepo *promoterv1alpha1.GitRepository, proposed, active string) string {
	owner, name := t.repoOwnerName(gitRepo)
	return utils.GetPullRequestName(owner, name, proposed, active)
}

// newRepo creates (or, in fake mode, synthesizes) a repository for the given stem.
// In server mode the repository is deleted via DeferCleanup when the current Ginkgo node finishes.
func (t *scmTestTarget) newRepo(ctx context.Context, stem string) testRepo {
	GinkgoHelper()
	if t.isFake() {
		return testRepo{Owner: stem, Name: stem}
	}

	seq := t.repoSeq.Add(1)
	// Keep owner/name short: GitHub owners are capped at 39 chars and Azure orgs at 50.
	owner := fmt.Sprintf("o%s-%d-%d", t.runID, GinkgoParallelProcess(), seq)
	if t.provider == scmserver.BitbucketDataCenter {
		// Bitbucket Data Center project keys allow only letters, digits and underscores.
		owner = fmt.Sprintf("O%s_%d_%d", t.runID, GinkgoParallelProcess(), seq)
	}
	name := fmt.Sprintf("r%s-%d-%d", t.runID, GinkgoParallelProcess(), seq)
	req := scmserver.CreateRepoRequest{
		Provider: t.provider,
		Owner:    owner,
		Name:     name,
		// Leave Init false: setup helpers push the first commit themselves.
	}
	if t.provider == scmserver.AzureDevOps {
		req.Project = fmt.Sprintf("p%s-%d-%d", t.runID, GinkgoParallelProcess(), seq)
	}
	info, err := t.admin.CreateRepo(ctx, req)
	Expect(err).NotTo(HaveOccurred(), "create repo on scmserver (is make run-scm-server running?)")
	// Best-effort cleanup. Controllers may still touch the repo briefly after the Spec ends;
	// a missing repo then only produces reconciler ERROR logs, which tests already tolerate.
	DeferCleanup(func(cleanupCtx SpecContext) { //nolint:contextcheck // DeferCleanup supplies its own SpecContext for teardown.
		_ = t.admin.DeleteRepo(cleanupCtx, info.ID)
	})

	_, err = t.admin.AddWebhook(ctx, scmserver.WebhookSubscription{
		Provider: t.provider,
		Repo:     info.FullName,
		URL:      fmt.Sprintf("http://127.0.0.1:%d/", webhookReceiverPort),
	})
	Expect(err).NotTo(HaveOccurred())

	repo := testRepo{
		Owner:    info.Owner,
		Name:     info.Name,
		Project:  info.Project,
		ID:       info.ID,
		CloneURL: info.CloneURL,
	}
	if t.provider == scmserver.AzureDevOps {
		azureOrgs.Store(info.Project+"/"+info.Name, info.Owner)
	}

	if t.provider == scmserver.GitHub {
		appID := githubAppIDBase + int64(GinkgoParallelProcess())*githubAppIDsPerProcess + t.githubAppSeq.Add(1)
		Expect(t.admin.RegisterGitHubApp(ctx, scmserver.RegisterGitHubAppRequest{
			AppID:        appID,
			PublicKeyPEM: string(t.githubKeyPEM),
			Installations: []scmserver.GitHubInstallation{
				{ID: appID * 10, Account: info.Owner},
			},
		})).To(Succeed())
		repo.GitHubAppID = appID
	}
	return repo
}

// scmProviderSpec builds an ScmProviderSpec for the configured target, including SecretRef.
func (t *scmTestTarget) scmProviderSpec(repo testRepo, secretName string) promoterv1alpha1.ScmProviderSpec {
	GinkgoHelper()
	spec := promoterv1alpha1.ScmProviderSpec{
		SecretRef: &corev1.LocalObjectReference{Name: secretName},
	}
	if t.isFake() {
		spec.Fake = &promoterv1alpha1.Fake{}
		return spec
	}
	switch t.provider {
	case scmserver.GitHub:
		spec.GitHub = &promoterv1alpha1.GitHub{Domain: t.domain, AppID: repo.GitHubAppID}
	case scmserver.GitLab:
		spec.GitLab = &promoterv1alpha1.GitLab{Domain: t.domain}
	case scmserver.AzureDevOps:
		spec.AzureDevOps = &promoterv1alpha1.AzureDevOps{Organization: repo.Owner, Domain: t.domain}
	case scmserver.BitbucketCloud:
		spec.BitbucketCloud = &promoterv1alpha1.BitbucketCloud{Domain: t.domain}
	case scmserver.Gitea:
		spec.Gitea = &promoterv1alpha1.Gitea{Domain: t.domain}
	case scmserver.Forgejo:
		spec.Forgejo = &promoterv1alpha1.Forgejo{Domain: t.domain}
	case scmserver.BitbucketDataCenter:
		spec.BitbucketDataCenter = &promoterv1alpha1.BitbucketDataCenter{Domain: t.domain}
	default:
		Fail("unknown scmserver provider " + string(t.provider))
	}
	return spec
}

// gitRepositorySpec builds a GitRepositorySpec for the configured target.
func (t *scmTestTarget) gitRepositorySpec(repo testRepo, scmProviderRef promoterv1alpha1.ScmProviderObjectReference) promoterv1alpha1.GitRepositorySpec {
	GinkgoHelper()
	spec := promoterv1alpha1.GitRepositorySpec{ScmProviderRef: scmProviderRef}
	if t.isFake() {
		spec.Fake = &promoterv1alpha1.FakeRepo{Owner: repo.Owner, Name: repo.Name}
		return spec
	}
	switch t.provider {
	case scmserver.GitHub:
		spec.GitHub = &promoterv1alpha1.GitHubRepo{Owner: repo.Owner, Name: repo.Name}
	case scmserver.GitLab:
		spec.GitLab = &promoterv1alpha1.GitLabRepo{Namespace: repo.Owner, Name: repo.Name, ProjectID: int(repo.ID)}
	case scmserver.AzureDevOps:
		spec.AzureDevOps = &promoterv1alpha1.AzureDevOpsRepo{Project: repo.Project, Name: repo.Name}
	case scmserver.BitbucketCloud:
		spec.BitbucketCloud = &promoterv1alpha1.BitbucketCloudRepo{Owner: repo.Owner, Name: repo.Name}
	case scmserver.Gitea:
		spec.Gitea = &promoterv1alpha1.GiteaRepo{Owner: repo.Owner, Name: repo.Name}
	case scmserver.Forgejo:
		spec.Forgejo = &promoterv1alpha1.ForgejoRepo{Owner: repo.Owner, Name: repo.Name}
	case scmserver.BitbucketDataCenter:
		spec.BitbucketDataCenter = &promoterv1alpha1.BitbucketDataCenterRepo{Project: repo.Owner, Name: repo.Name}
	default:
		Fail("unknown scmserver provider " + string(t.provider))
	}
	return spec
}

// secretData returns Secret.Data for the configured target. Fake mode returns nil (empty secret).
// repo is accepted for API symmetry with the other helpers (and for GitHub App ID on the repo).
func (t *scmTestTarget) secretData(_ testRepo) map[string][]byte {
	if t.isFake() {
		return nil
	}
	if t.provider == scmserver.GitHub {
		return map[string][]byte{"githubAppPrivateKey": t.githubKeyPEM}
	}
	return map[string][]byte{"token": []byte(t.token)}
}

// cloneURL returns a clone URL suitable for runGitCmd. In server mode credentials are embedded
// so git can authenticate over HTTPS without an askpass helper.
func (t *scmTestTarget) cloneURL(gitRepo *promoterv1alpha1.GitRepository) string {
	GinkgoHelper()
	if t.isFake() {
		owner, name := t.repoOwnerName(gitRepo)
		return fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPort, owner, name)
	}
	base := t.unauthenticatedCloneURL(gitRepo)
	user := t.gitUser()
	u, err := url.Parse(base)
	Expect(err).NotTo(HaveOccurred())
	u.User = url.UserPassword(user, t.token)
	return u.String()
}

func (t *scmTestTarget) unauthenticatedCloneURL(gitRepo *promoterv1alpha1.GitRepository) string {
	GinkgoHelper()
	switch t.provider {
	case scmserver.AzureDevOps:
		Expect(gitRepo.Spec.AzureDevOps).NotTo(BeNil())
		key := gitRepo.Spec.AzureDevOps.Project + "/" + gitRepo.Spec.AzureDevOps.Name
		orgVal, ok := azureOrgs.Load(key)
		Expect(ok).To(BeTrue(), "missing Azure DevOps org for %s (was newRepo called?)", key)
		org, _ := orgVal.(string)
		return fmt.Sprintf("https://%s/%s/%s/_git/%s", t.domain, org, gitRepo.Spec.AzureDevOps.Project, gitRepo.Spec.AzureDevOps.Name)
	case scmserver.BitbucketDataCenter:
		owner, name := t.repoOwnerName(gitRepo)
		return fmt.Sprintf("https://%s/scm/%s/%s.git", t.domain, owner, name)
	default:
		owner, name := t.repoOwnerName(gitRepo)
		return fmt.Sprintf("https://%s/%s/%s.git", t.domain, owner, name)
	}
}

// azureOrgs maps "project/name" -> organization for Azure DevOps clone URL reconstruction.
// The GitRepository only stores project+name; the organization lives on the ScmProvider.
var azureOrgs sync.Map

func (t *scmTestTarget) gitUser() string {
	switch t.provider {
	case scmserver.GitHub, scmserver.AzureDevOps:
		return "git"
	case scmserver.BitbucketCloud:
		return "x-token-auth"
	case scmserver.BitbucketDataCenter:
		// Bitbucket Data Center accepts a token over basic auth only with its owner's user name.
		return scmserver.TokenUser(scmserver.BitbucketDataCenter)
	default:
		return "oauth2"
	}
}

// expectPromotionMergeCommit asserts that hydrated is the merge commit of a promotion PR. Real
// providers pick their own merge subject and author (GitHub: "Merge pull request #N from ..." by
// the App's bot user) and put the requested message in the body, so only the fake provider is
// held to the exact subject/author.
func (t *scmTestTarget) expectPromotionMergeCommit(g Gomega, hydrated promoterv1alpha1.CommitShaState) {
	if t.isFake() {
		g.Expect(hydrated.Author).To(Equal("GitOps Promoter"))
		g.Expect(hydrated.Subject).To(ContainSubstring("Promote"))
		g.Expect(hydrated.Body).To(ContainSubstring("This PR promotes changes to"))
		return
	}
	message := hydrated.Subject + "\n\n" + hydrated.Body
	g.Expect(hydrated.Author).NotTo(BeEmpty())
	g.Expect(message).To(ContainSubstring("Promote"))
	g.Expect(message).To(ContainSubstring("This PR promotes changes to"))
}

// mergeReportsTargetSha reports whether the provider's Merge returns the resulting target-branch
// commit. Gitea, Forgejo and Azure DevOps do not, and the controller recovers it with a Get-by-ID lookup.
func (t *scmTestTarget) mergeReportsTargetSha() bool {
	switch t.provider {
	case "", scmserver.GitHub, scmserver.GitLab, scmserver.BitbucketCloud, scmserver.BitbucketDataCenter:
		return true
	default:
		return false
	}
}

// mergeGuardsHeadSha reports whether the provider refuses a merge when the pull request's head no
// longer matches PullRequest.spec.mergeSha. Bitbucket Cloud's merge API takes no expected head SHA:
// its documented body is type, message, close_source_branch and merge_strategy, and the go-bitbucket,
// drone/go-scm and jenkins-x/go-scm clients send none, so the server merges whatever the head is.
// TODO: confirm against real Bitbucket Cloud, with and without a merge queue on the target branch.
func (t *scmTestTarget) mergeGuardsHeadSha() bool {
	return t.provider != scmserver.BitbucketCloud
}

// skipIfFakeSCMOnly skips the current spec when a real scmserver provider is selected and the
// spec (or any of its containers) is labeled fake-scm-only.
func skipIfFakeSCMOnly() {
	if scmTarget.isFake() {
		return
	}
	for _, label := range CurrentSpecReport().Labels() {
		if label == fakeSCMOnlyLabel {
			Skip("spec requires the in-process fake SCM provider")
		}
	}
}

// initSCMTargetFromEnv builds the suite-level scmTarget from PROMOTER_TEST_SCM_* env vars.
// Call once from BeforeSuite. In server mode it verifies the admin API is reachable, trusts the
// CA, registers the token, and generates a GitHub App key.
func initSCMTargetFromEnv(ctx context.Context) *scmTestTarget {
	GinkgoHelper()
	providerName := strings.TrimSpace(os.Getenv(envSCMProvider))
	if providerName == "" || providerName == "fake" {
		return &scmTestTarget{}
	}

	p := scmserver.Provider(providerName)
	if !p.Valid() {
		Fail(fmt.Sprintf("%s=%q is not a supported provider; use fake or one of %v", envSCMProvider, providerName, scmserver.AllProviders))
	}

	adminURL := envOr(envSCMAdminURL, defaultSCMAdminURL)
	token := envOr(envSCMToken, defaultSCMToken)
	caFile := envOr(envSCMCAFile, defaultSCMCAFile)
	if !filepath.IsAbs(caFile) {
		// Resolve relative to the package dir (internal/controller) up to the repo root.
		caFile = filepath.Clean(filepath.Join("..", "..", caFile))
		abs, err := filepath.Abs(caFile)
		Expect(err).NotTo(HaveOccurred())
		caFile = abs
	}

	admin := scmserver.NewAdminClient(adminURL)
	info, err := admin.Info(ctx)
	Expect(err).NotTo(HaveOccurred(),
		"cannot reach scmserver admin at %s; start it with `make run-scm-server`", adminURL)
	providerInfo, ok := info.Providers[p]
	Expect(ok).To(BeTrue(), "scmserver did not report provider %s", p)

	_, err = os.Stat(caFile)
	Expect(err).NotTo(HaveOccurred(), "CA file %s missing; start scmserver with --ca-out pointing there", caFile)

	Expect(os.Setenv("SSL_CERT_FILE", caFile)).To(Succeed())
	Expect(os.Setenv("GIT_SSL_CAINFO", caFile)).To(Succeed())
	Expect(utils.ConfigureDefaultTransportFromEnv()).To(Succeed())

	// Health check: a GET that fails would have already failed on Info above; also hit /healthz.
	resp, err := http.Get(adminURL + "/healthz") //nolint:gosec,noctx // test-only local admin
	Expect(err).NotTo(HaveOccurred())
	_ = resp.Body.Close()
	Expect(resp.StatusCode).To(Equal(http.StatusOK))

	Expect(admin.AddCredential(ctx, scmserver.Credential{Provider: p, Token: token})).To(Succeed())

	// Keep provider quirks from slowing the envtest suite: zero the async delays. Also turn off rate
	// limiting: every spec shares one token, and the suite exceeds Bitbucket Cloud's 1000 requests per hour.
	b := scmserver.DefaultBehavior(p)
	b.MergeabilityDelay = scmserver.D(0)
	b.CompletionDelay = scmserver.D(0)
	b.RateLimit = 0
	Expect(admin.SetBehavior(ctx, p, b)).To(Succeed())

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	Expect(err).NotTo(HaveOccurred())
	githubKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	By(fmt.Sprintf("using scmserver provider %s at domain %s", p, providerInfo.Domain))
	return &scmTestTarget{
		provider:     p,
		admin:        admin,
		domain:       providerInfo.Domain,
		token:        token,
		caFile:       caFile,
		githubKeyPEM: githubKeyPEM,
		runID:        strings.ToLower(randomString(6)),
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
