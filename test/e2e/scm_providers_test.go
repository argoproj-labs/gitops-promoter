package e2e

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/test/scmserver"
	"github.com/argoproj-labs/gitops-promoter/test/utils"
)

const (
	scmServerNamespace = "scm-server"
	scmServerHost      = "scm-server.scm-server.svc"
	scmServerImage     = "scm-server:e2e"
	scmE2ENamespace    = "scm-e2e"
	scmAdminLocalPort  = "19000"
	scmToken           = "e2e-token"
	scmGitHubAppID     = int64(424242)
	webhookReceiverURL = "http://promoter-webhook-receiver.promoter-system.svc:3333/"
	promotionTimeout   = 5 * time.Minute
)

// scmServerPorts are the Service ports of each provider listener of the test SCM server.
var scmServerPorts = map[scmserver.Provider]int{
	scmserver.GitHub:         9001,
	scmserver.GitLab:         9002,
	scmserver.AzureDevOps:    9003,
	scmserver.BitbucketCloud: 9004,
	scmserver.Gitea:          9005,
	scmserver.Forgejo:        9006,
}

// run runs a command from the repository root and fails the spec on error.
func run(ctx context.Context, stdin string, name string, args ...string) {
	GinkgoHelper()
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred(), string(out))
}

// kubectlIgnoringErrors runs kubectl for best-effort cleanup.
func kubectlIgnoringErrors(ctx context.Context, args ...string) {
	_, _ = utils.Run(exec.CommandContext(ctx, "kubectl", args...))
}

// scmProviderSpecs promotes through every provider of the test SCM server, using the unmodified provider
// implementations of the deployed controller. It runs inside the Ordered "controller" container, after the
// controller has been deployed.
func scmProviderSpecs() {
	var (
		admin     *scmserver.AdminClient
		githubKey []byte
	)

	BeforeAll(func(ctx SpecContext) {
		By("building the test SCM server image and loading it into Kind")
		run(ctx, "", "make", "docker-build-scm-server", "SCM_SERVER_IMG="+scmServerImage)
		Expect(utils.LoadImageToKindClusterWithName(scmServerImage)).To(Succeed())

		By("issuing the server's TLS certificate")
		caPEM, caKey, err := scmserver.GenerateCA("gitops-promoter e2e SCM CA")
		Expect(err).NotTo(HaveOccurred())
		certPEM, keyPEM, err := scmserver.GenerateServingCert(caPEM, caKey,
			[]string{scmServerHost, scmServerHost + ".cluster.local", "localhost", "127.0.0.1"})
		Expect(err).NotTo(HaveOccurred())
		dir := GinkgoT().TempDir()
		for name, content := range map[string][]byte{"ca.crt": caPEM, "tls.crt": certPEM, "tls.key": keyPEM} {
			Expect(os.WriteFile(filepath.Join(dir, name), content, 0o600)).To(Succeed())
		}

		By("deploying the test SCM server")
		run(ctx, "", "kubectl", "apply", "-k", "test/scmserver/deploy")
		DeferCleanup(kubectlIgnoringErrors, "delete", "-k", "test/scmserver/deploy", "--ignore-not-found")
		run(ctx, "", "kubectl", "create", "secret", "generic", "scm-server-tls", "-n", scmServerNamespace,
			"--from-file=ca.crt="+filepath.Join(dir, "ca.crt"),
			"--from-file=tls.crt="+filepath.Join(dir, "tls.crt"),
			"--from-file=tls.key="+filepath.Join(dir, "tls.key"))
		run(ctx, "", "kubectl", "wait", "deployment/scm-server", "-n", scmServerNamespace,
			"--for=condition=Available", "--timeout=3m")

		By("shortening requeue intervals so polling paths finish within the spec timeouts")
		requeue := `{"spec":{` +
			`"promotionStrategy":{"workQueue":{"requeueDuration":"15s"}},` +
			`"changeTransferPolicy":{"workQueue":{"requeueDuration":"15s"}},` +
			`"pullRequest":{"workQueue":{"requeueDuration":"15s"}},` +
			`"commitStatus":{"workQueue":{"requeueDuration":"15s"}},` +
			`"dependentsSuccessfulCommitStatus":{"workQueue":{"requeueDuration":"15s"}}}}`
		run(ctx, "", "kubectl", "patch", "controllerconfiguration", "promoter-controller-configuration",
			"-n", namespace, "--type=merge", "-p", requeue)

		By("making the controller trust the server's CA for API calls and git")
		run(ctx, "", "kubectl", "create", "secret", "generic", "scm-server-ca", "-n", namespace,
			"--from-file=ca.crt="+filepath.Join(dir, "ca.crt"))
		patch := `{"spec":{"template":{"spec":{
			"volumes":[{"name":"scm-server-ca","secret":{"secretName":"scm-server-ca"}}],
			"containers":[{"name":"manager",
				"volumeMounts":[{"name":"scm-server-ca","mountPath":"/etc/scm-server-ca","readOnly":true}],
				"env":[{"name":"SSL_CERT_FILE","value":"/etc/scm-server-ca/ca.crt"},
				       {"name":"GIT_SSL_CAINFO","value":"/etc/scm-server-ca/ca.crt"}]}]}}}}`
		run(ctx, "", "kubectl", "patch", "deployment", "promoter-controller-manager",
			"-n", namespace, "--type=strategic", "-p", patch)
		run(ctx, "", "kubectl", "rollout", "status", "deployment/promoter-controller-manager",
			"-n", namespace, "--timeout=3m")

		By("forwarding the admin API")
		forwardCtx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		forward := exec.CommandContext(forwardCtx, "kubectl", "port-forward", "-n", scmServerNamespace,
			"svc/scm-server", scmAdminLocalPort+":9000")
		forward.Stdout, forward.Stderr = GinkgoWriter, GinkgoWriter
		Expect(forward.Start()).To(Succeed())
		admin = scmserver.NewAdminClient("http://127.0.0.1:" + scmAdminLocalPort)
		Eventually(func(g Gomega) {
			_, err := admin.Info(ctx)
			g.Expect(err).NotTo(HaveOccurred())
		}).WithContext(ctx).WithTimeout(time.Minute).WithPolling(time.Second).Should(Succeed())

		By("registering credentials")
		for _, p := range scmserver.AllProviders {
			Expect(admin.AddCredential(ctx, scmserver.Credential{Provider: p, Token: scmToken})).To(Succeed())
		}
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		Expect(err).NotTo(HaveOccurred())
		githubKey = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
		Expect(admin.RegisterGitHubApp(ctx, scmserver.RegisterGitHubAppRequest{
			AppID: scmGitHubAppID, PublicKeyPEM: string(githubKey),
			Installations: []scmserver.GitHubInstallation{{ID: scmGitHubAppID * 10, Account: scmOwner(scmserver.GitHub)}},
		})).To(Succeed())

		run(ctx, "", "kubectl", "create", "ns", scmE2ENamespace)
		DeferCleanup(kubectlIgnoringErrors, "delete", "ns", scmE2ENamespace, "--ignore-not-found", "--wait=false")
	}, NodeTimeout(15*time.Minute))

	// AfterEach rather than DeferCleanup: the enclosing container's AfterAll removes the controller before
	// cleanups run, and its logs are the most useful evidence of why a promotion stalled.
	AfterEach(func(ctx SpecContext) {
		if CurrentSpecReport().Failed() {
			printSCMDiagnostics(ctx, admin)
		}
	}, NodeTimeout(2*time.Minute))

	DescribeTable("promotes through the provider's API with the real provider implementation",
		func(ctx SpecContext, p scmserver.Provider) {
			DeferCleanup(kubectlIgnoringErrors, "delete", "-n", scmE2ENamespace, "promotionstrategy", string(p),
				"--ignore-not-found")
			flow := promotionFlow{
				admin:     admin,
				githubKey: githubKey,
				domain:    fmt.Sprintf("%s:%d", scmServerHost, scmServerPorts[p]),
				webhook:   webhookReceiverURL,
				apply: func(ctx context.Context, manifests string) {
					run(ctx, manifests, "kubectl", "apply", "-n", scmE2ENamespace, "-f", "-")
				},
				pullRequestRepos: func(ctx context.Context) ([]string, error) {
					out, err := utils.Run(exec.CommandContext(ctx, "kubectl", "get", "pullrequests", "-n", scmE2ENamespace,
						"-o", "jsonpath={range .items[*]}{.spec.gitRepositoryRef.name}{\"\\n\"}{end}"))
					return utils.GetNonEmptyLines(string(out)), err
				},
			}
			flow.promote(ctx, p)
		},
		Entry("GitHub", scmserver.GitHub, NodeTimeout(12*time.Minute)),
		Entry("GitLab", scmserver.GitLab, NodeTimeout(12*time.Minute)),
		Entry("Azure DevOps", scmserver.AzureDevOps, NodeTimeout(12*time.Minute)),
		Entry("Bitbucket Cloud", scmserver.BitbucketCloud, NodeTimeout(12*time.Minute)),
		Entry("Gitea", scmserver.Gitea, NodeTimeout(12*time.Minute)),
		Entry("Forgejo", scmserver.Forgejo, NodeTimeout(12*time.Minute)),
	)
}

// promotionFlow promotes a hydrated change through one provider of the test SCM server and verifies the result on
// the server. apply applies the provider's manifests and pullRequestRepos lists the repository of every remaining
// PullRequest resource.
type promotionFlow struct {
	admin            *scmserver.AdminClient
	apply            func(ctx context.Context, manifests string)
	pullRequestRepos func(ctx context.Context) ([]string, error)
	domain           string
	webhook          string
	githubKey        []byte
}

func (f promotionFlow) promote(ctx context.Context, p scmserver.Provider) {
	GinkgoHelper()
	req := scmserver.CreateRepoRequest{Provider: p, Owner: scmOwner(p), Name: "promotion"}
	if p == scmserver.AzureDevOps {
		req.Project = "e2e"
	}
	repo, err := f.admin.CreateRepo(ctx, req)
	Expect(err).NotTo(HaveOccurred())
	_, err = f.admin.AddWebhook(ctx, scmserver.WebhookSubscription{Provider: p, Repo: repo.FullName, URL: f.webhook})
	Expect(err).NotTo(HaveOccurred())

	environments := []string{"environment/development", "environment/staging"}
	seedEnvironments(ctx, f.admin, repo, environments)
	f.apply(ctx, promotionManifests(p, repo, f.domain, f.githubKey))

	By("hydrating a new dry commit to every environment")
	drySHA := hydrate(ctx, f.admin, repo, environments)

	for _, env := range environments {
		By("merging the pull request into " + env)
		Eventually(func(g Gomega) {
			prs, err := f.admin.PullRequests(ctx, repo.ID, "merged")
			g.Expect(err).NotTo(HaveOccurred())
			var merged *scmserver.PullRequestInfo
			for i := range prs {
				if prs[i].Target == env {
					merged = &prs[i]
				}
			}
			g.Expect(merged).NotTo(BeNil(), "no merged pull request into %s yet", env)
			info, err := f.admin.Repo(ctx, repo.ID)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(info.Branches[env]).To(Equal(merged.MergeCommitSHA))
		}).WithContext(ctx).WithTimeout(promotionTimeout).WithPolling(2 * time.Second).Should(Succeed())
	}

	By("reporting the ordering gate as a commit status on the provider")
	statuses, err := f.admin.CommitStatuses(ctx, repo.ID, "")
	Expect(err).NotTo(HaveOccurred())
	Expect(statuses).To(ContainElement(HaveField("Context", "dependents-successful")))

	By("finalizing every PullRequest resource")
	Eventually(func(g Gomega) {
		repos, err := f.pullRequestRepos(ctx)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(repos).NotTo(ContainElement(string(p)))
	}).WithContext(ctx).WithTimeout(promotionTimeout).WithPolling(2 * time.Second).Should(Succeed())

	if p == scmserver.AzureDevOps {
		By("waiting out Azure DevOps' asynchronous completion instead of abandoning the pull request")
		prs, err := f.admin.PullRequests(ctx, repo.ID, "")
		Expect(err).NotTo(HaveOccurred())
		for _, pr := range prs {
			Expect(pr.State).To(Equal("merged"), "pull request %d into %s", pr.Number, pr.Target)
			Expect(pr.CompletionRequestedAt).NotTo(BeNil())
			Expect(pr.MergedAt.Sub(*pr.CompletionRequestedAt)).To(BeNumerically(">=", time.Second))
		}
	}
	GinkgoWriter.Printf("promoted dry commit %s through %s\n", drySHA, p)
}

// printSCMDiagnostics writes the state of the controller, the test SCM server and the promotion resources to the
// spec output, so a failed promotion can be diagnosed from the CI log alone.
func printSCMDiagnostics(ctx context.Context, admin *scmserver.AdminClient) {
	for _, args := range [][]string{
		{"get", "pods", "-A", "-o", "wide"},
		{"logs", "-n", namespace, "deployment/promoter-controller-manager", "-c", "manager", "--tail=500"},
		{"logs", "-n", namespace, "deployment/promoter-controller-manager", "-c", "manager", "--previous", "--tail=100"},
		{"logs", "-n", scmServerNamespace, "deployment/scm-server", "--tail=200"},
		{
			"get", "scmproviders,gitrepositories,promotionstrategies,changetransferpolicies,pullrequests,commitstatuses",
			"-n", scmE2ENamespace, "-o", "yaml",
		},
		{"get", "events", "-n", scmE2ENamespace, "--sort-by=.lastTimestamp"},
		{"get", "events", "-n", namespace, "--sort-by=.lastTimestamp"},
	} {
		out, err := utils.Run(exec.CommandContext(ctx, "kubectl", args...))
		if err != nil {
			GinkgoWriter.Printf("%v\n", err)
			continue
		}
		GinkgoWriter.Printf("%s\n", out)
	}
	if admin == nil {
		return
	}
	if requests, err := admin.Requests(ctx); err != nil {
		GinkgoWriter.Printf("listing SCM server requests: %v\n", err)
	} else {
		GinkgoWriter.Printf("SCM server requests (%d):\n", len(requests))
		for _, r := range requests {
			GinkgoWriter.Printf("  %s %-11s %-6s %s?%s -> %d (%s)\n", r.Time.Format(time.TimeOnly), r.Provider, r.Method,
				r.Path, r.Query, r.StatusCode, r.Principal)
		}
	}
	if deliveries, err := admin.WebhookDeliveries(ctx); err != nil {
		GinkgoWriter.Printf("listing webhook deliveries: %v\n", err)
	} else {
		GinkgoWriter.Printf("webhook deliveries (%d):\n", len(deliveries))
		for _, d := range deliveries {
			GinkgoWriter.Printf("  %s %-11s %s %s %s -> %d %s\n", d.Time.Format(time.TimeOnly), d.Provider, d.Repo, d.Ref,
				d.URL, d.StatusCode, d.Error)
		}
	}
}

// scmOwner is the owner (organization, namespace or workspace) of the provider's e2e repository.
func scmOwner(p scmserver.Provider) string {
	return "e2e-" + string(p)
}

// seedEnvironments lays out the repository the way the hydrator does: a default branch carrying hydrator.metadata
// and, for every environment, an empty "-next" branch with the environment branch pointing at it.
func seedEnvironments(ctx context.Context, admin *scmserver.AdminClient, repo scmserver.RepoInfo, environments []string,
) {
	GinkgoHelper()
	first, err := admin.Commit(ctx, repo.ID, scmserver.CommitRequest{
		Branch: repo.DefaultBranch, Files: map[string]string{"hydrator.metadata": `{"drySha": "n/a"}`},
		Message: "init commit dry side n/a dry sha",
	})
	Expect(err).NotTo(HaveOccurred())
	_, err = admin.Commit(ctx, repo.ID, scmserver.CommitRequest{
		Branch: repo.DefaultBranch, Files: map[string]string{"hydrator.metadata": fmt.Sprintf(`{"drySha": %q}`, first.SHA)},
		Message: "second commit with real dry sha",
	})
	Expect(err).NotTo(HaveOccurred())
	for _, env := range environments {
		next, err := admin.Commit(ctx, repo.ID, scmserver.CommitRequest{
			Branch: env + "-next", Orphan: true, Message: "initial empty commit for " + env + "-next",
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = admin.SetBranch(ctx, repo.ID, env, scmserver.BranchRequest{SHA: next.SHA})
		Expect(err).NotTo(HaveOccurred())
	}
}

// hydrate pushes a dry commit to the default branch and the matching hydrated commit to every "-next" branch, as the
// hydrator would, and returns the dry SHA.
func hydrate(ctx context.Context, admin *scmserver.AdminClient, repo scmserver.RepoInfo, environments []string) string {
	GinkgoHelper()
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	dry, err := admin.Commit(ctx, repo.ID, scmserver.CommitRequest{
		Branch: repo.DefaultBranch, Files: map[string]string{"manifests-fake.yaml": fmt.Sprintf(`{"time": %q}`, stamp)},
		Message: "change the application", AuthorName: "developer", AuthorEmail: "developer@example.com",
	})
	Expect(err).NotTo(HaveOccurred())
	metadata, err := json.MarshalIndent(v1alpha1.HydratorMetadata{
		DrySha:  dry.SHA,
		Author:  "developer <developer@example.com>",
		Date:    metav1.Now(),
		Subject: "change the application",
	}, "", "\t")
	Expect(err).NotTo(HaveOccurred())
	for _, env := range environments {
		_, err := admin.Commit(ctx, repo.ID, scmserver.CommitRequest{
			Branch: env + "-next",
			Files: map[string]string{
				"hydrator.metadata":   string(metadata),
				"manifests-fake.yaml": fmt.Sprintf(`{"time": %q, "environment": %q}`, stamp, env),
			},
			Message: "hydrate " + dry.SHA + " to " + env, AuthorName: "hydrator", AuthorEmail: "hydrator@example.com",
		})
		Expect(err).NotTo(HaveOccurred())
	}
	return dry.SHA
}

// promotionManifests returns the Secret, ScmProvider, GitRepository, PromotionStrategy and ordering gate that point
// the controller at the provider's listener on the test SCM server at domain. Every object is named after the
// provider.
func promotionManifests(p scmserver.Provider, repo scmserver.RepoInfo, domain string, githubKey []byte) string {
	name := string(p)
	secretData := fmt.Sprintf("  token: %s\n", scmToken)
	var providerSpec, repoSpec string
	switch p {
	case scmserver.GitHub:
		secretData = "  githubAppPrivateKey: |\n" + indent(string(githubKey), "    ")
		providerSpec = fmt.Sprintf("github:\n    domain: %s\n    appID: %d", domain, scmGitHubAppID)
		repoSpec = fmt.Sprintf("github:\n    owner: %s\n    name: %s", repo.Owner, repo.Name)
	case scmserver.GitLab:
		providerSpec = "gitlab:\n    domain: " + domain
		repoSpec = fmt.Sprintf("gitlab:\n    namespace: %s\n    name: %s\n    projectId: %d", repo.Owner, repo.Name, repo.ID)
	case scmserver.AzureDevOps:
		providerSpec = fmt.Sprintf("azureDevOps:\n    organization: %s\n    domain: %s", repo.Owner, domain)
		repoSpec = fmt.Sprintf("azureDevOps:\n    project: %s\n    name: %s", repo.Project, repo.Name)
	case scmserver.BitbucketCloud:
		providerSpec = "bitbucketCloud:\n    domain: " + domain
		repoSpec = fmt.Sprintf("bitbucketCloud:\n    owner: %s\n    name: %s", repo.Owner, repo.Name)
	case scmserver.Gitea:
		providerSpec = "gitea:\n    domain: " + domain
		repoSpec = fmt.Sprintf("gitea:\n    owner: %s\n    name: %s", repo.Owner, repo.Name)
	case scmserver.Forgejo:
		providerSpec = "forgejo:\n    domain: " + domain
		repoSpec = fmt.Sprintf("forgejo:\n    owner: %s\n    name: %s", repo.Owner, repo.Name)
	default:
		Fail("unknown provider " + name)
	}
	return fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %[1]s
stringData:
%[2]s---
apiVersion: promoter.argoproj.io/v1alpha1
kind: ScmProvider
metadata:
  name: %[1]s
spec:
  secretRef:
    name: %[1]s
  %[3]s
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: GitRepository
metadata:
  name: %[1]s
spec:
  scmProviderRef:
    kind: ScmProvider
    name: %[1]s
  %[4]s
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: PromotionStrategy
metadata:
  name: %[1]s
spec:
  gitRepositoryRef:
    name: %[1]s
  orderCommitStatusRef:
    group: promoter.argoproj.io
    kind: DependentsSuccessfulCommitStatus
    name: %[1]s
  environments:
    - branch: environment/development
      autoMerge: true
    - branch: environment/staging
      autoMerge: true
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: DependentsSuccessfulCommitStatus
metadata:
  name: %[1]s
spec:
  key: dependents-successful
  promotionStrategyRef:
    name: %[1]s
`, name, secretData, providerSpec, repoSpec)
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n") + "\n"
}
