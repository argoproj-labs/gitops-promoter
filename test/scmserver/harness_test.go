package scmserver_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/gitauth"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/azuredevops"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/bitbucket_cloud"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/bitbucket_datacenter"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/forgejo"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitea"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/github"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitlab"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/argoproj-labs/gitops-promoter/test/scmserver"
)

const (
	testNamespace   = "default"
	testToken       = "contract-test-token"
	githubAppIDBase = int64(4200)
	sourceBranch    = "environment/development-next"
	targetBranch    = "environment/development"
)

var (
	srv          *scmserver.Server
	admin        *scmserver.AdminClient
	caFile       string
	githubKeyPEM []byte
	fixtureSeq   atomic.Int64
	installSeq   atomic.Int64
)

var _ = BeforeSuite(func() {
	ctx := context.Background()
	var err error
	srv, err = scmserver.New(ctx, scmserver.Options{})
	Expect(err).NotTo(HaveOccurred())

	// Trust the server the way the controller does: SSL_CERT_FILE feeds http.DefaultTransport and the system pool used
	// by clients with their own transport (GitLab), and git subprocesses read GIT_SSL_CAINFO.
	caFile = filepath.Join(GinkgoT().TempDir(), "ca.pem")
	Expect(os.WriteFile(caFile, srv.CAPEM(), 0o600)).To(Succeed())
	Expect(os.Setenv("SSL_CERT_FILE", caFile)).To(Succeed())
	DeferCleanup(func() { Expect(os.Unsetenv("SSL_CERT_FILE")).To(Succeed()) })
	Expect(utils.ConfigureDefaultHTTPTransport()).To(Succeed())

	Expect(srv.Start(ctx)).To(Succeed())
	DeferCleanup(func(ctx SpecContext) { Expect(srv.Shutdown(ctx)).To(Succeed()) }, NodeTimeout(30*time.Second))
	admin = scmserver.NewAdminClient(srv.AdminURL())

	for _, p := range scmserver.AllProviders {
		Expect(srv.AddCredential(scmserver.Credential{Provider: p, Token: testToken})).To(Succeed())
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	Expect(err).NotTo(HaveOccurred())
	githubKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
})

// fixture is a hosted repository with a pull-request-ready branch pair and the promoter's real provider
// implementations pointed at it.
type fixture struct {
	k8s         client.Client
	prs         scms.PullRequestProvider
	statuses    scms.CommitStatusProvider
	gitAuth     scms.GitOperationsProvider
	scmProvider *v1alpha1.ScmProvider
	secret      *corev1.Secret
	gitRepo     *v1alpha1.GitRepository
	provider    scmserver.Provider
	repo        scmserver.RepoInfo
	baseSHA     string
	headSHA     string
}

// newFixture creates a repository on provider p whose default branch, target branch and source branch are ready
// for a pull request, plus the Kubernetes objects and provider implementations the controller would build.
func newFixture(ctx context.Context, p scmserver.Provider) *fixture {
	GinkgoHelper()
	seq := fixtureSeq.Add(1)
	name := fmt.Sprintf("repo-%d", seq)
	owner := fmt.Sprintf("org-%d", seq)
	if p == scmserver.BitbucketDataCenter {
		// Project keys allow only letters, digits and underscores.
		owner = fmt.Sprintf("ORG_%d", seq)
	}
	req := scmserver.CreateRepoRequest{Provider: p, Owner: owner, Name: name, Init: true}
	if p == scmserver.AzureDevOps {
		req.Project = "project-" + strconv.FormatInt(seq, 10)
	}
	repo, err := srv.CreateRepo(ctx, req)
	Expect(err).NotTo(HaveOccurred())

	base, err := srv.Commit(ctx, repo.ID, scmserver.CommitRequest{
		Branch: targetBranch, Parent: repo.DefaultBranch, Files: map[string]string{"manifest.yaml": "replicas: 1\n"},
		Message: "hydrate development",
	})
	Expect(err).NotTo(HaveOccurred())
	head, err := srv.Commit(ctx, repo.ID, scmserver.CommitRequest{
		Branch: sourceBranch, Parent: base.SHA, Files: map[string]string{"manifest.yaml": "replicas: 2\n"},
		Message: "hydrate development-next",
	})
	Expect(err).NotTo(HaveOccurred())

	f := &fixture{provider: p, repo: repo, baseSHA: base.SHA, headSHA: head.SHA}
	f.buildObjects(ctx)
	return f
}

func (f *fixture) buildObjects(ctx context.Context) {
	GinkgoHelper()
	p, repo := f.provider, f.repo
	name := "scm-" + repo.Name
	f.secret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Data:       map[string][]byte{"token": []byte(testToken)},
	}
	f.scmProvider = &v1alpha1.ScmProvider{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec:       v1alpha1.ScmProviderSpec{SecretRef: &corev1.LocalObjectReference{Name: name}},
	}
	f.gitRepo = &v1alpha1.GitRepository{
		ObjectMeta: metav1.ObjectMeta{Name: repo.Name, Namespace: testNamespace},
		Spec: v1alpha1.GitRepositorySpec{
			ScmProviderRef: v1alpha1.ScmProviderObjectReference{Kind: v1alpha1.ScmProviderKind, Name: name},
		},
	}
	domain := srv.Domain(p)
	switch p {
	case scmserver.GitHub:
		// The provider caches installation lookups per App, including misses, so every fixture gets its own App.
		appID := githubAppIDBase + installSeq.Add(1)
		Expect(srv.RegisterGitHubApp(scmserver.RegisterGitHubAppRequest{
			AppID: appID, PublicKeyPEM: string(githubKeyPEM),
			Installations: []scmserver.GitHubInstallation{{ID: appID * 10, Account: repo.Owner}},
		})).To(Succeed())
		f.secret.Data = map[string][]byte{"githubAppPrivateKey": githubKeyPEM}
		f.scmProvider.Spec.GitHub = &v1alpha1.GitHub{Domain: domain, AppID: appID}
		f.gitRepo.Spec.GitHub = &v1alpha1.GitHubRepo{Owner: repo.Owner, Name: repo.Name}
	case scmserver.GitLab:
		f.scmProvider.Spec.GitLab = &v1alpha1.GitLab{Domain: domain}
		f.gitRepo.Spec.GitLab = &v1alpha1.GitLabRepo{Namespace: repo.Owner, Name: repo.Name, ProjectID: int(repo.ID)}
	case scmserver.AzureDevOps:
		f.scmProvider.Spec.AzureDevOps = &v1alpha1.AzureDevOps{Organization: repo.Owner, Domain: domain}
		f.gitRepo.Spec.AzureDevOps = &v1alpha1.AzureDevOpsRepo{Project: repo.Project, Name: repo.Name}
	case scmserver.BitbucketCloud:
		f.scmProvider.Spec.BitbucketCloud = &v1alpha1.BitbucketCloud{Domain: domain}
		f.gitRepo.Spec.BitbucketCloud = &v1alpha1.BitbucketCloudRepo{Owner: repo.Owner, Name: repo.Name}
	case scmserver.Gitea:
		f.scmProvider.Spec.Gitea = &v1alpha1.Gitea{Domain: domain}
		f.gitRepo.Spec.Gitea = &v1alpha1.GiteaRepo{Owner: repo.Owner, Name: repo.Name}
	case scmserver.Forgejo:
		f.scmProvider.Spec.Forgejo = &v1alpha1.Forgejo{Domain: domain}
		f.gitRepo.Spec.Forgejo = &v1alpha1.ForgejoRepo{Owner: repo.Owner, Name: repo.Name}
	case scmserver.BitbucketDataCenter:
		f.scmProvider.Spec.BitbucketDataCenter = &v1alpha1.BitbucketDataCenter{Domain: domain}
		f.gitRepo.Spec.BitbucketDataCenter = &v1alpha1.BitbucketDataCenterRepo{Project: repo.Owner, Name: repo.Name}
	default:
		Fail("unknown provider " + string(p))
	}

	f.k8s = fake.NewClientBuilder().WithScheme(utils.GetScheme()).
		WithObjects(f.secret, f.scmProvider, f.gitRepo).Build()

	f.prs, f.statuses = providersFor(ctx, f.k8s, f.scmProvider, *f.secret, f.gitRepo)
	var err error
	f.gitAuth, err = gitauth.CreateGitOperationsProvider(ctx, f.k8s, f.scmProvider, f.secret, client.ObjectKeyFromObject(f.gitRepo))
	Expect(err).NotTo(HaveOccurred())
}

// providersFor builds the pull request and commit status providers exactly as the controllers do.
func providersFor(ctx context.Context, k8s client.Client, scmProvider *v1alpha1.ScmProvider, secret corev1.Secret, gitRepo *v1alpha1.GitRepository) (scms.PullRequestProvider, scms.CommitStatusProvider) {
	GinkgoHelper()
	var (
		prs      scms.PullRequestProvider
		statuses scms.CommitStatusProvider
		prErr    error
		csErr    error
	)
	spec := scmProvider.Spec
	switch {
	case spec.GitHub != nil:
		prs, prErr = github.NewGithubPullRequestProvider(ctx, k8s, scmProvider, secret, gitRepo.Spec.GitHub.Owner)
		statuses, csErr = github.NewGithubCommitStatusProvider(ctx, k8s, scmProvider, secret, gitRepo.Spec.GitHub.Owner)
	case spec.GitLab != nil:
		prs, prErr = gitlab.NewGitlabPullRequestProvider(k8s, secret, spec.GitLab.Domain)
		statuses, csErr = gitlab.NewGitlabCommitStatusProvider(k8s, secret, spec.GitLab.Domain)
	case spec.AzureDevOps != nil:
		prs, prErr = azuredevops.NewAzdoPullRequestProvider(k8s, secret, scmProvider, spec.AzureDevOps.Organization) //nolint:contextcheck // matches the controller's factory, which takes no context
		statuses, csErr = azuredevops.NewAzureDevopsCommitStatusProvider(ctx, k8s, scmProvider, secret, spec.AzureDevOps.Organization)
	case spec.BitbucketCloud != nil:
		prs, prErr = bitbucket_cloud.NewBitbucketCloudPullRequestProvider(k8s, secret, spec.BitbucketCloud.Domain)
		statuses, csErr = bitbucket_cloud.NewBitbucketCloudCommitStatusProvider(k8s, secret, spec.BitbucketCloud.Domain)
	case spec.Gitea != nil:
		prs, prErr = gitea.NewGiteaPullRequestProvider(k8s, secret, spec.Gitea.Domain)
		statuses, csErr = gitea.NewGiteaCommitStatusProvider(k8s, scmProvider, secret)
	case spec.Forgejo != nil:
		prs, prErr = forgejo.NewForgejoPullRequestProvider(k8s, secret, spec.Forgejo.Domain)
		statuses, csErr = forgejo.NewForgejoCommitStatusProvider(k8s, scmProvider, secret)
	case spec.BitbucketDataCenter != nil:
		prs, prErr = bitbucket_datacenter.NewBitbucketDataCenterPullRequestProvider(k8s, scmProvider, secret)
		statuses, csErr = bitbucket_datacenter.NewBitbucketDataCenterCommitStatusProvider(k8s, scmProvider, secret)
	default:
		Fail("unsupported provider " + scmProvider.Name)
	}
	Expect(prErr).NotTo(HaveOccurred())
	Expect(csErr).NotTo(HaveOccurred())
	return prs, statuses
}

// pullRequest returns a PullRequest object for the fixture's branch pair.
func (f *fixture) pullRequest(name string) v1alpha1.PullRequest {
	return v1alpha1.PullRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec: v1alpha1.PullRequestSpec{
			RepositoryReference: v1alpha1.ObjectReference{Name: f.gitRepo.Name},
			Title:               "Promote development",
			Description:         "Promotes the hydrated manifests to development.",
			SourceBranch:        sourceBranch,
			TargetBranch:        targetBranch,
			MergeSha:            f.headSHA,
			Commit:              v1alpha1.CommitConfiguration{Message: "Promote " + f.headSHA[:7]},
		},
	}
}

// branchSHA returns the current head of branch on the server.
func (f *fixture) branchSHA(ctx context.Context, branch string) string {
	GinkgoHelper()
	info, err := admin.Repo(ctx, f.repo.ID)
	Expect(err).NotTo(HaveOccurred())
	return info.Branches[branch]
}

// serverPR returns the server-side state of pull request id.
func (f *fixture) serverPR(ctx context.Context, id string) scmserver.PullRequestInfo {
	GinkgoHelper()
	number, err := strconv.Atoi(id)
	Expect(err).NotTo(HaveOccurred())
	info, err := admin.PullRequest(ctx, f.repo.ID, number)
	Expect(err).NotTo(HaveOccurred())
	return info
}

// git runs git with the server CA trusted and returns its combined output.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_SSL_CAINFO="+caFile,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// git runs git like the package-level git, adding the http.extraHeader the promoter configures for providers that
// authenticate git with a header (Bitbucket Data Center's bearer token).
func (f *fixture) git(ctx context.Context, dir string, args ...string) (string, error) {
	GinkgoHelper()
	if headerProvider, ok := f.gitAuth.(scms.GitHTTPHeaderProvider); ok {
		urlPrefix, header, err := headerProvider.GetGitHTTPHeader(ctx)
		Expect(err).NotTo(HaveOccurred())
		if header != "" {
			args = append([]string{"-c", "http." + urlPrefix + ".extraHeader=" + header}, args...)
		}
	}
	return git(ctx, dir, args...)
}

// authenticatedCloneURL returns the provider's clone URL with the credentials the promoter would send.
func (f *fixture) authenticatedCloneURL(ctx context.Context) string {
	GinkgoHelper()
	cloneURL := f.gitAuth.GetGitHttpsRepoUrl(*f.gitRepo)
	Expect(cloneURL).NotTo(BeEmpty())
	user, err := f.gitAuth.GetUser(ctx)
	Expect(err).NotTo(HaveOccurred())
	token, err := f.gitAuth.GetToken(ctx)
	Expect(err).NotTo(HaveOccurred())
	scheme, rest, ok := strings.Cut(cloneURL, "://")
	Expect(ok).To(BeTrue())
	return scheme + "://" + user + ":" + token + "@" + rest
}
