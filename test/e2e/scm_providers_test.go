package e2e

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
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
	// requeueInterval is the work queue requeue interval the suite configures on the deployed controller. It is
	// longer than any spec, so no requeue fires during a scenario: the server's push webhooks and the controllers'
	// watches have to carry every promotion on their own.
	requeueInterval = time.Hour
	// pollingRequeue is the requeue interval for the one scenario that needs the controller to poll the provider:
	// a pull request closed on the provider sends no push, and the webhook receiver handles push events only, so the
	// controller learns of it only by re-reading the pull request. The server does send the provider's pull request
	// event; the receiver does not consume it yet.
	pollingRequeue = 5 * time.Second
	// retryDelay is the PullRequest rate limiter's slow delay, which retries a merge the provider refused (branch
	// protection). It defaults to 5 minutes.
	retryDelay = 5 * time.Second
	// holdTime is how long a spec watches a pull request it expects the controller to leave open. With requeues off,
	// the controller acts on a pull request within seconds of the webhook that announces it, so this long a hold
	// proves the controller declined to merge.
	holdTime = 8 * time.Second
)

// scmServerPorts are the Service ports of each provider listener of the test SCM server.
var scmServerPorts = map[scmserver.Provider]int{
	scmserver.GitHub:              9001,
	scmserver.GitLab:              9002,
	scmserver.AzureDevOps:         9003,
	scmserver.BitbucketCloud:      9004,
	scmserver.Gitea:               9005,
	scmserver.Forgejo:             9006,
	scmserver.BitbucketDataCenter: 9007,
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

		By("disabling requeues so only webhooks and watches drive the promotions, and shortening merge retries")
		// The Dashboard specs run after these in the same Ordered suite, so the controller configuration goes back to
		// what it was once this context is done. The CA trust patched into the Deployment below is inert for the
		// other specs and stays, since reverting it would cost another controller rollout.
		var configuration struct {
			Spec json.RawMessage `json:"spec"`
		}
		configurationJSON, err := utils.Run(exec.CommandContext(ctx, "kubectl", "get", "controllerconfiguration",
			"promoter-controller-configuration", "-n", namespace, "-o", "json"))
		Expect(err).NotTo(HaveOccurred(), string(configurationJSON))
		Expect(json.Unmarshal(configurationJSON, &configuration)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) {
			run(ctx, "", "kubectl", "patch", "controllerconfiguration", "promoter-controller-configuration",
				"-n", namespace, "--type=json", "-p", `[{"op":"replace","path":"/spec","value":`+string(configuration.Spec)+`}]`)
		})
		configureRequeue(ctx, requeueInterval)

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

	// newFlow returns a promotionFlow for p that applies and lists resources in the e2e namespace with kubectl.
	newFlow := func(p scmserver.Provider) promotionFlow {
		return promotionFlow{
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
			list: func(ctx context.Context, resource string, into any) error {
				out, err := utils.Run(exec.CommandContext(ctx, "kubectl", "get", resource, "-n", scmE2ENamespace,
					"-o", "json"))
				if err != nil {
					return fmt.Errorf("listing %s: %w", resource, err)
				}
				return json.Unmarshal(out, into)
			},
		}
	}

	// cleanupPromotions deletes, after the spec, the promotion resources every provider's flow created under
	// name(p) and suffixes. It runs on the spec's goroutine, before the providers fan out.
	cleanupPromotions := func(kinds string, name func(p scmserver.Provider) string, suffixes ...string) {
		for _, p := range scmserver.AllProviders {
			args := make([]any, 0, 7+len(suffixes))
			args = append(args, kubectlIgnoringErrors, "delete", "-n", scmE2ENamespace, kinds, name(p))
			for _, suffix := range suffixes {
				args = append(args, name(p)+suffix)
			}
			DeferCleanup(append(args, "--ignore-not-found")...)
		}
	}

	// Each scenario runs for every provider at once. Every provider has its own repositories on the server and its
	// own promotion resources, and the controller reconciles them independently, so the scenario takes as long as its
	// slowest provider rather than the sum.
	It("promotes through the provider's API with the real provider implementation", func(ctx SpecContext) {
		cleanupPromotions("promotionstrategy", func(p scmserver.Provider) string { return string(p) })
		forEachProvider(ctx, func(ctx context.Context, p scmserver.Provider) {
			newFlow(p).promote(ctx, p, string(p))
		})
	}, NodeTimeout(12*time.Minute))

	It("holds a promotion on a proposed commit status and follows the proposal when it moves", func(ctx SpecContext) {
		name := func(p scmserver.Provider) string { return string(p) + "-gated" }
		cleanupPromotions("promotionstrategy,commitstatus", name, "-gate")
		forEachProvider(ctx, func(ctx context.Context, p scmserver.Provider) {
			newFlow(p).promoteThroughGate(ctx, p, name(p))
		})
	}, NodeTimeout(12*time.Minute))

	It("promotes pull requests a person merges on the provider with a merge or a squash commit", func(ctx SpecContext) {
		name := func(p scmserver.Provider) string { return string(p) + "-by-hand" }
		cleanupPromotions("promotionstrategy", name)
		forEachProvider(ctx, func(ctx context.Context, p scmserver.Provider) {
			newFlow(p).promoteByHand(ctx, p, name(p))
		})
	}, NodeTimeout(12*time.Minute))

	// Closing a pull request on the provider pushes nothing, so no push webhook announces it, and the receiver ignores
	// the pull request event the server sends; the controller only notices when it re-reads the pull request on a
	// requeue. This scenario alone runs with a short requeue.
	It("opens a new pull request when a person closes one on the provider", func(ctx SpecContext) {
		configureRequeue(ctx, pollingRequeue)
		DeferCleanup(func(ctx SpecContext) { configureRequeue(ctx, requeueInterval) })
		name := func(p scmserver.Provider) string { return string(p) + "-reopened" }
		cleanupPromotions("promotionstrategy", name)
		forEachProvider(ctx, func(ctx context.Context, p scmserver.Provider) {
			newFlow(p).promoteAfterClose(ctx, p, name(p))
		})
	}, NodeTimeout(12*time.Minute))

	// The test SCM server refuses an Azure DevOps completion request synchronously while the required status has not
	// passed, so the provider's Merge returns an error and the controller retries, as for the other providers. Azure
	// DevOps does not document that response; Behavior.AzureAsyncCompletionFailures models the alternative, an
	// accepted completion that fails later, which the provider treats as merged and waits for indefinitely.
	It("waits for a merge the provider blocks until a required status passes", func(ctx SpecContext) {
		name := func(p scmserver.Provider) string { return string(p) + "-protected" }
		cleanupPromotions("promotionstrategy,commitstatus", name, "-required")
		forEachProvider(ctx, func(ctx context.Context, p scmserver.Provider) {
			if p == scmserver.BitbucketDataCenter {
				// The provider reports commit statuses through the deprecated /rest/build-status resource, which
				// Bitbucket Data Center's Required Builds merge check does not read, so the merge is never allowed.
				return
			}
			newFlow(p).promoteThroughProtection(ctx, p, name(p))
		})
	}, NodeTimeout(12*time.Minute))
}

// configureRequeue sets every controller's work queue requeue interval to interval on the deployed
// ControllerConfiguration, and the PullRequest rate limiter's slow delay to retryDelay.
func configureRequeue(ctx context.Context, interval time.Duration) {
	GinkgoHelper()
	d := interval.String()
	retry := retryDelay.String()
	requeue := `{"spec":{` +
		`"promotionStrategy":{"workQueue":{"requeueDuration":"` + d + `"}},` +
		`"changeTransferPolicy":{"workQueue":{"requeueDuration":"` + d + `"}},` +
		`"changeTransferPolicyHistory":{"workQueue":{"requeueDuration":"` + d + `"}},` +
		`"pullRequest":{"workQueue":{"requeueDuration":"` + d + `","rateLimiter":{"maxOf":[` +
		`{"bucket":{"qps":10,"bucket":100}},` +
		`{"fastSlow":{"fastDelay":"1s","slowDelay":"` + retry + `","maxFastAttempts":3}}]}}},` +
		`"commitStatus":{"workQueue":{"requeueDuration":"` + d + `"}},` +
		`"dependentsSuccessfulCommitStatus":{"workQueue":{"requeueDuration":"` + d + `"}}}}`
	run(ctx, "", "kubectl", "patch", "controllerconfiguration", "promoter-controller-configuration",
		"-n", namespace, "--type=merge", "-p", requeue)
}

// forEachProvider runs body for every provider of the test SCM server concurrently and waits for all of them. A
// failure in one provider fails the spec, and the other providers still run to completion so the diagnostics printed
// afterwards show every provider's final state.
func forEachProvider(ctx context.Context, body func(ctx context.Context, p scmserver.Provider)) {
	GinkgoHelper()
	var wg sync.WaitGroup
	for _, p := range scmserver.AllProviders {
		wg.Go(func() {
			defer GinkgoRecover()
			body(ctx, p)
		})
	}
	wg.Wait()
}

// promotionFlow promotes a hydrated change through one provider of the test SCM server and verifies the result on
// the server. apply applies the provider's manifests, pullRequestRepos lists the repository of every remaining
// PullRequest resource, and list decodes the JSON list of a resource kind in the test namespace into into.
type promotionFlow struct {
	admin            *scmserver.AdminClient
	apply            func(ctx context.Context, manifests string)
	pullRequestRepos func(ctx context.Context) ([]string, error)
	list             func(ctx context.Context, resource string, into any) error
	domain           string
	webhook          string
	githubKey        []byte
}

// promote promotes a hydrated change through development and staging on provider p, with the controller merging the
// pull requests. name names the promotion's resources and the repository.
func (f promotionFlow) promote(ctx context.Context, p scmserver.Provider, name string) {
	GinkgoHelper()
	repo := f.newRepo(ctx, p, name)

	environments := []string{"environment/development", "environment/staging"}
	seedEnvironments(ctx, f.admin, repo, environments)
	f.apply(ctx, promotionManifests(p, name, repo, f.domain, f.githubKey, true, ""))

	By("hydrating a new dry commit to every environment")
	drySHA := hydrate(ctx, f.admin, repo, environments)

	By("delivering the hydration pushes to the promoter's webhook receiver")
	f.expectWebhooksDelivered(ctx, repo, environments[0]+"-next", environments[1]+"-next")

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

	By("delivering the merge pushes to the promoter's webhook receiver")
	f.expectWebhooksDelivered(ctx, repo, environments...)

	By("reporting the ordering gate as a commit status on the provider")
	statuses, err := f.admin.CommitStatuses(ctx, repo.ID, "")
	Expect(err).NotTo(HaveOccurred())
	Expect(statuses).To(ContainElement(HaveField("Context", "dependents-successful")))

	By("finalizing every PullRequest resource")
	Eventually(func(g Gomega) {
		repos, err := f.pullRequestRepos(ctx)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(repos).NotTo(ContainElement(name))
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

// promoteByHand promotes three hydrated changes through one provider of the test SCM server with auto-merge off,
// merging every pull request the controller opens as a person would in the provider's UI: first with a merge commit,
// then with a squash commit, then with a merge commit again. The last round follows a squash, whose single-parent
// commit leaves the environment branch without the proposed branch's history, so its pull request conflicts until the
// controller merges the environment branch back into the proposed branch. name names the promotion's resources.
func (f promotionFlow) promoteByHand(ctx context.Context, p scmserver.Provider, name string) {
	GinkgoHelper()
	req := scmserver.CreateRepoRequest{Provider: p, Owner: scmOwner(p), Name: "promotion-by-hand"}
	if p == scmserver.AzureDevOps {
		req.Project = "e2e"
	}
	repo, err := f.admin.CreateRepo(ctx, req)
	Expect(err).NotTo(HaveOccurred())
	_, err = f.admin.AddWebhook(ctx, scmserver.WebhookSubscription{Provider: p, Repo: repo.FullName, URL: f.webhook})
	Expect(err).NotTo(HaveOccurred())

	environments := []string{"environment/development", "environment/staging"}
	seedEnvironments(ctx, f.admin, repo, environments)
	f.apply(ctx, promotionManifests(p, name, repo, f.domain, f.githubKey, false, ""))

	for _, squash := range []bool{false, true, false} {
		style := "a merge commit"
		if squash {
			style = "a squash commit"
		}
		By("hydrating a new dry commit to every environment")
		drySHA := hydrate(ctx, f.admin, repo, environments)
		for _, env := range environments {
			By(fmt.Sprintf("merging the pull request into %s with %s on the provider", env, style))
			merged := f.mergeByHand(ctx, repo, name, env, squash)

			By("finalizing the PullRequest resource of " + env)
			Eventually(func(g Gomega) {
				var prs v1alpha1.PullRequestList
				g.Expect(f.list(ctx, "pullrequests", &prs)).To(Succeed())
				for _, pr := range prs.Items {
					g.Expect(pr.Spec.RepositoryReference.Name == name && pr.Spec.TargetBranch == env).To(BeFalse(),
						"PullRequest %s into %s still exists: %+v", pr.Name, env, pr.Status)
				}
			}).WithContext(ctx).WithTimeout(promotionTimeout).WithPolling(2 * time.Second).Should(Succeed())

			By("recording the merged pull request in the history of " + env)
			Eventually(func(g Gomega) {
				var histories v1alpha1.ChangeTransferPolicyHistoryList
				g.Expect(f.list(ctx, "changetransferpolicyhistories", &histories)).To(Succeed())
				var history *v1alpha1.ChangeTransferPolicyHistory
				for i := range histories.Items {
					spec := histories.Items[i].Spec
					if spec.RepositoryReference.Name == name && spec.ActiveBranch == env {
						history = &histories.Items[i]
					}
				}
				g.Expect(history).NotTo(BeNil(), "no ChangeTransferPolicyHistory for %s", env)
				g.Expect(history.Status.History).NotTo(BeEmpty())
				entry := history.Status.History[0]
				g.Expect(entry.Active.Hydrated.Sha).To(Equal(merged.MergeCommitSHA))
				g.Expect(entry.Active.Dry.Sha).To(Equal(drySHA))
				g.Expect(entry.PullRequest).NotTo(BeNil())
				g.Expect(entry.PullRequest.ID).To(Equal(strconv.Itoa(merged.Number)))
				g.Expect(entry.PullRequest.MergedTargetSha).To(Equal(merged.MergeCommitSHA))
			}).WithContext(ctx).WithTimeout(promotionTimeout).WithPolling(2 * time.Second).Should(Succeed())
		}

		By("reporting the dry commit as active in every environment")
		Eventually(func(g Gomega) {
			var strategies v1alpha1.PromotionStrategyList
			g.Expect(f.list(ctx, "promotionstrategies", &strategies)).To(Succeed())
			var strategy *v1alpha1.PromotionStrategy
			for i := range strategies.Items {
				if strategies.Items[i].Name == name {
					strategy = &strategies.Items[i]
				}
			}
			g.Expect(strategy).NotTo(BeNil())
			g.Expect(strategy.Status.Environments).To(HaveLen(len(environments)))
			for _, env := range strategy.Status.Environments {
				g.Expect(env.Active.Dry.Sha).To(Equal(drySHA), "active dry commit of %s", env.Branch)
			}
		}).WithContext(ctx).WithTimeout(promotionTimeout).WithPolling(2 * time.Second).Should(Succeed())
		GinkgoWriter.Printf("promoted dry commit %s through %s, merging with %s\n", drySHA, p, style)
	}
}

// development and staging are the environment branches of the e2e promotions.
const (
	development = "environment/development"
	staging     = "environment/staging"
)

// gateKey is the proposed commit status that gates promotion into development in promoteThroughGate.
const gateKey = "e2e-gate"

// promoteThroughGate promotes through one provider of the test SCM server with a proposed commit status gating
// development, reported on the provider through the controller's real commit status implementation. It checks that the
// controller:
//   - holds the pull request while the gate is pending on the proposed commit;
//   - keeps the same pull request when a newer hydrated commit moves the proposed branch, so it targets the new head;
//   - keeps holding when the gate succeeds only on the stale proposed commit;
//   - merges the newer commit, and only it, once the gate succeeds on it, and then promotes it to staging.
//
// name names the promotion's resources.
func (f promotionFlow) promoteThroughGate(ctx context.Context, p scmserver.Provider, name string) {
	GinkgoHelper()
	req := scmserver.CreateRepoRequest{Provider: p, Owner: scmOwner(p), Name: "gated-promotion"}
	if p == scmserver.AzureDevOps {
		req.Project = "e2e"
	}
	repo, err := f.admin.CreateRepo(ctx, req)
	Expect(err).NotTo(HaveOccurred())
	_, err = f.admin.AddWebhook(ctx, scmserver.WebhookSubscription{Provider: p, Repo: repo.FullName, URL: f.webhook})
	Expect(err).NotTo(HaveOccurred())
	environments := []string{development, staging}
	seedEnvironments(ctx, f.admin, repo, environments)
	f.apply(ctx, promotionManifests(p, name, repo, f.domain, f.githubKey, true, gateKey))

	By("hydrating a first dry commit")
	hydrate(ctx, f.admin, repo, environments)
	first := f.developmentTip(ctx, repo)
	opened := f.openDevelopmentPullRequest(ctx, repo, first)

	By("reporting the gate as pending on the first proposed commit")
	f.setGate(ctx, name, first, v1alpha1.CommitPhasePending)
	f.expectGate(ctx, repo, first, false)
	f.expectHeld(ctx, repo, development, opened.Number)

	By("hydrating a second dry commit while the pull request is open")
	secondDry := hydrate(ctx, f.admin, repo, environments)
	second := f.developmentTip(ctx, repo)
	Expect(second).NotTo(Equal(first))
	Expect(f.openDevelopmentPullRequest(ctx, repo, second).Number).To(Equal(opened.Number),
		"the controller keeps the open pull request instead of opening another")

	By("passing the gate on the stale first commit only")
	f.setGate(ctx, name, first, v1alpha1.CommitPhaseSuccess)
	f.expectGate(ctx, repo, first, true)
	f.expectHeld(ctx, repo, development, opened.Number)

	By("passing the gate on the second commit")
	f.setGate(ctx, name, second, v1alpha1.CommitPhaseSuccess)
	f.expectGate(ctx, repo, second, true)
	for _, env := range environments {
		By("merging the second dry commit into " + env)
		Eventually(func(g Gomega) {
			prs, err := f.admin.PullRequests(ctx, repo.ID, "")
			g.Expect(err).NotTo(HaveOccurred())
			var merged []scmserver.PullRequestInfo
			for _, pr := range prs {
				if pr.Target == env {
					g.Expect(pr.State).To(Equal("merged"), "pull request %d into %s", pr.Number, env)
					merged = append(merged, pr)
				}
			}
			g.Expect(merged).To(HaveLen(1), "pull requests into %s", env)
			if env == development {
				g.Expect(merged[0].Number).To(Equal(opened.Number))
				g.Expect(merged[0].HeadSHA).To(Equal(second), "the merge took the newer proposed commit")
			}
			info, err := f.admin.Repo(ctx, repo.ID)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(info.Branches[env]).To(Equal(merged[0].MergeCommitSHA))
		}).WithContext(ctx).WithTimeout(promotionTimeout).WithPolling(2 * time.Second).Should(Succeed())
	}

	By("reporting the second dry commit as active in every environment")
	Eventually(func(g Gomega) {
		var strategies v1alpha1.PromotionStrategyList
		g.Expect(f.list(ctx, "promotionstrategies", &strategies)).To(Succeed())
		var strategy *v1alpha1.PromotionStrategy
		for i := range strategies.Items {
			if strategies.Items[i].Name == name {
				strategy = &strategies.Items[i]
			}
		}
		g.Expect(strategy).NotTo(BeNil())
		g.Expect(strategy.Status.Environments).To(HaveLen(len(environments)))
		for _, env := range strategy.Status.Environments {
			g.Expect(env.Active.Dry.Sha).To(Equal(secondDry), "active dry commit of %s", env.Branch)
		}
	}).WithContext(ctx).WithTimeout(promotionTimeout).WithPolling(2 * time.Second).Should(Succeed())
	GinkgoWriter.Printf("promoted dry commit %s through %s past the %s gate\n", secondDry, p, gateKey)
}

// developmentTip returns the commit at the tip of development's proposed branch.
func (f promotionFlow) developmentTip(ctx context.Context, repo scmserver.RepoInfo) string {
	GinkgoHelper()
	info, err := f.admin.Repo(ctx, repo.ID)
	Expect(err).NotTo(HaveOccurred())
	return info.Branches[development+"-next"]
}

// openDevelopmentPullRequest waits for exactly one open pull request into development, with head as its head commit,
// and returns it.
func (f promotionFlow) openDevelopmentPullRequest(ctx context.Context, repo scmserver.RepoInfo, head string,
) scmserver.PullRequestInfo {
	GinkgoHelper()
	var open []scmserver.PullRequestInfo
	Eventually(func(g Gomega) {
		prs, err := f.admin.PullRequests(ctx, repo.ID, "")
		g.Expect(err).NotTo(HaveOccurred())
		open = nil
		for _, pr := range prs {
			if pr.Target != development {
				continue
			}
			g.Expect(pr.State).To(Equal("open"), "pull request %d into %s", pr.Number, development)
			open = append(open, pr)
		}
		g.Expect(open).To(HaveLen(1), "pull requests into %s", development)
		g.Expect(open[0].HeadSHA).To(Equal(head))
	}).WithContext(ctx).WithTimeout(promotionTimeout).WithPolling(2 * time.Second).Should(Succeed())
	return open[0]
}

// setGate points the gate's CommitStatus at sha with phase. The controller reports it on the provider.
func (f promotionFlow) setGate(ctx context.Context, name, sha string, phase v1alpha1.CommitStatusPhase) {
	GinkgoHelper()
	f.apply(ctx, fmt.Sprintf(`apiVersion: promoter.argoproj.io/v1alpha1
kind: CommitStatus
metadata:
  name: %[1]s-gate
  labels:
    promoter.argoproj.io/commit-status: %[2]s
spec:
  gitRepositoryRef:
    name: %[1]s
  sha: %[3]s
  name: %[2]s
  description: e2e gate
  phase: %[4]s
`, name, gateKey, sha, phase))
}

// expectGate waits until the provider reports the gate on sha, as successful or as not yet successful.
func (f promotionFlow) expectGate(ctx context.Context, repo scmserver.RepoInfo, sha string, successful bool) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		statuses, err := f.admin.CommitStatuses(ctx, repo.ID, sha)
		g.Expect(err).NotTo(HaveOccurred())
		var gate []scmserver.CommitStatusInfo
		for _, cs := range statuses {
			if cs.Context == gateKey {
				gate = append(gate, cs)
			}
		}
		g.Expect(gate).NotTo(BeEmpty(), "no %s status on %s", gateKey, sha)
		latest := slices.MaxFunc(gate, func(a, b scmserver.CommitStatusInfo) int { return a.UpdatedAt.Compare(b.UpdatedAt) })
		g.Expect(statusSucceeded(latest)).To(Equal(successful), "%s status on %s: %+v", gateKey, sha, latest)
	}).WithContext(ctx).WithTimeout(promotionTimeout).WithPolling(2 * time.Second).Should(Succeed())
}

// expectHeld checks that pull request number into env stays open and that no pull request merges into env.
func (f promotionFlow) expectHeld(ctx context.Context, repo scmserver.RepoInfo, env string, number int) {
	GinkgoHelper()
	Consistently(func(g Gomega) {
		prs, err := f.admin.PullRequests(ctx, repo.ID, "")
		g.Expect(err).NotTo(HaveOccurred())
		for _, pr := range prs {
			if pr.Target == env {
				g.Expect(pr.Number).To(Equal(number))
				g.Expect(pr.State).To(Equal("open"), "pull request %d into %s", pr.Number, env)
			}
		}
	}).WithContext(ctx).WithTimeout(holdTime).WithPolling(2 * time.Second).Should(Succeed())
}

// statusSucceeded reports whether a provider's commit status, build status or check run is successful.
func statusSucceeded(cs scmserver.CommitStatusInfo) bool {
	switch strings.ToLower(cs.State) {
	case "success", "successful", "succeeded":
		return true
	case "completed":
		return cs.Conclusion == "success"
	default:
		return false
	}
}

// promoteAfterClose checks, for one provider of the test SCM server, that the controller opens a new pull request
// when a person closes the controller's pull request on the provider, and that the promotion completes through it.
// Auto-merge is off so the pull request stays open until it is closed. name names the promotion's resources.
func (f promotionFlow) promoteAfterClose(ctx context.Context, p scmserver.Provider, name string) {
	GinkgoHelper()
	repo := f.newRepo(ctx, p, "closed-promotion")
	environments := []string{development, "environment/staging"}
	seedEnvironments(ctx, f.admin, repo, environments)
	f.apply(ctx, promotionManifests(p, name, repo, f.domain, f.githubKey, false, ""))

	By("hydrating a new dry commit")
	drySHA := hydrate(ctx, f.admin, repo, environments)
	head := f.developmentTip(ctx, repo)
	closed := f.openDevelopmentPullRequest(ctx, repo, head)

	By("closing the pull request on the provider")
	_, err := f.admin.PullRequestAction(ctx, repo.ID, closed.Number, scmserver.PullRequestAction{Action: "close"})
	Expect(err).NotTo(HaveOccurred())

	By("delivering the pull request's closed event to the promoter's webhook receiver, which accepts and ignores it")
	f.expectPullRequestWebhookDelivered(ctx, repo, closed.Number, "closed")

	By("opening a new pull request for the same proposed commit")
	var reopened scmserver.PullRequestInfo
	Eventually(func(g Gomega) {
		prs, err := f.admin.PullRequests(ctx, repo.ID, "")
		g.Expect(err).NotTo(HaveOccurred())
		var open []scmserver.PullRequestInfo
		for _, pr := range prs {
			switch {
			case pr.Target != development:
			case pr.Number == closed.Number:
				g.Expect(pr.State).To(Equal("closed"), "the closed pull request stays closed")
			default:
				g.Expect(pr.State).To(Equal("open"), "pull request %d into %s", pr.Number, development)
				open = append(open, pr)
			}
		}
		g.Expect(open).To(HaveLen(1), "new pull requests into %s", development)
		g.Expect(open[0].HeadSHA).To(Equal(head))
		reopened = open[0]
	}).WithContext(ctx).WithTimeout(promotionTimeout).WithPolling(2 * time.Second).Should(Succeed())

	By("tracking only the new pull request")
	Eventually(func(g Gomega) {
		var prs v1alpha1.PullRequestList
		g.Expect(f.list(ctx, "pullrequests", &prs)).To(Succeed())
		var ids []string
		for _, pr := range prs.Items {
			if pr.Spec.RepositoryReference.Name == name && pr.Spec.TargetBranch == development {
				ids = append(ids, pr.Status.ID)
			}
		}
		g.Expect(ids).To(Equal([]string{strconv.Itoa(reopened.Number)}))
	}).WithContext(ctx).WithTimeout(promotionTimeout).WithPolling(2 * time.Second).Should(Succeed())

	By("promoting through the new pull request")
	merged := f.mergeByHand(ctx, repo, name, development, false)
	Expect(merged.Number).To(Equal(reopened.Number))
	f.expectActive(ctx, name, map[string]string{development: drySHA})
	GinkgoWriter.Printf("promoted dry commit %s through %s after closing pull request %d\n", drySHA, p, closed.Number)
}

// expectWebhooksDelivered waits until the server has delivered a push webhook for every one of branches in repo to
// the promoter's webhook receiver, and the receiver accepted each with 204. The receiver answers 400 when it cannot
// tell which provider sent the payload, so a 204 proves the delivery reached it with headers it recognizes.
func (f promotionFlow) expectWebhooksDelivered(ctx context.Context, repo scmserver.RepoInfo, branches ...string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		deliveries, err := f.admin.WebhookDeliveries(ctx)
		g.Expect(err).NotTo(HaveOccurred())
		for _, branch := range branches {
			ref := "refs/heads/" + branch
			var matched []scmserver.WebhookDelivery
			for _, d := range deliveries {
				if d.Repo == repo.FullName && d.Ref == ref && !d.Dropped {
					matched = append(matched, d)
				}
			}
			g.Expect(matched).NotTo(BeEmpty(), "no webhook delivered for %s of %s", ref, repo.FullName)
			for _, d := range matched {
				g.Expect(d.Error).To(BeEmpty(), "delivery %s for %s", d.ID, ref)
				g.Expect(d.StatusCode).To(Equal(http.StatusNoContent), "delivery %s for %s", d.ID, ref)
			}
		}
	}).WithContext(ctx).WithTimeout(promotionTimeout).WithPolling(2 * time.Second).Should(Succeed())
}

// expectPullRequestWebhookDelivered waits until the server has delivered the pull request webhook for action (opened,
// closed, reopened or merged) on pull request number of repo to the promoter's webhook receiver, and the receiver
// accepted it with 204. The receiver answers 204 to any event it cannot read a push from, so this checks the
// plumbing, not that the controller acted on the event.
func (f promotionFlow) expectPullRequestWebhookDelivered(
	ctx context.Context, repo scmserver.RepoInfo, number int, action string,
) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		deliveries, err := f.admin.WebhookDeliveries(ctx)
		g.Expect(err).NotTo(HaveOccurred())
		var matched []scmserver.WebhookDelivery
		for _, d := range deliveries {
			if d.Repo == repo.FullName && d.Event == "pull_request" && d.Number == number && d.Action == action && !d.Dropped {
				matched = append(matched, d)
			}
		}
		g.Expect(matched).NotTo(BeEmpty(), "no %s webhook delivered for pull request %d of %s", action, number, repo.FullName)
		for _, d := range matched {
			g.Expect(d.Error).To(BeEmpty(), "delivery %s", d.ID)
			g.Expect(d.StatusCode).To(Equal(http.StatusNoContent), "delivery %s", d.ID)
		}
	}).WithContext(ctx).WithTimeout(promotionTimeout).WithPolling(2 * time.Second).Should(Succeed())
}

// requiredStatus is the status context promoteThroughProtection's branch protection requires.
const requiredStatus = "e2e-required"

// promoteThroughProtection checks, for one provider of the test SCM server, that the controller keeps trying to merge a
// pull request the provider refuses because branch protection requires a status that has not passed, and merges it
// once the status passes. The promotion strategy itself has no gate, so the controller decides to merge at once.
// name names the promotion's resources.
func (f promotionFlow) promoteThroughProtection(ctx context.Context, p scmserver.Provider, name string) {
	GinkgoHelper()
	repo := f.newRepo(ctx, p, "protected-promotion")
	environments := []string{development, staging}
	seedEnvironments(ctx, f.admin, repo, environments)
	Expect(f.admin.SetProtection(ctx, repo.ID, development,
		scmserver.ProtectionRequest{RequiredStatuses: []string{requiredStatus}})).To(Succeed())
	f.apply(ctx, promotionManifests(p, name, repo, f.domain, f.githubKey, true, ""))

	By("hydrating a new dry commit")
	drySHA := hydrate(ctx, f.admin, repo, environments)
	head := f.developmentTip(ctx, repo)
	opened := f.openDevelopmentPullRequest(ctx, repo, head)

	By("deciding to merge the pull request into " + development)
	Eventually(func(g Gomega) {
		var prs v1alpha1.PullRequestList
		g.Expect(f.list(ctx, "pullrequests", &prs)).To(Succeed())
		var states []v1alpha1.PullRequestState
		for _, pr := range prs.Items {
			if pr.Spec.RepositoryReference.Name == name && pr.Spec.TargetBranch == development {
				states = append(states, pr.Spec.State)
			}
		}
		g.Expect(states).To(Equal([]v1alpha1.PullRequestState{v1alpha1.PullRequestMerged}))
	}).WithContext(ctx).WithTimeout(promotionTimeout).WithPolling(2 * time.Second).Should(Succeed())

	By("leaving the pull request open while the provider refuses the merge")
	f.expectHeld(ctx, repo, development, opened.Number)

	By("passing the required status on the pull request's head")
	f.apply(ctx, fmt.Sprintf(`apiVersion: promoter.argoproj.io/v1alpha1
kind: CommitStatus
metadata:
  name: %[1]s-required
spec:
  gitRepositoryRef:
    name: %[1]s
  sha: %[2]s
  name: %[3]s
  description: e2e required status
  phase: success
`, name, head, requiredStatus))

	for _, env := range environments {
		By("merging the dry commit into " + env)
		Eventually(func(g Gomega) {
			prs, err := f.admin.PullRequests(ctx, repo.ID, "")
			g.Expect(err).NotTo(HaveOccurred())
			var merged []scmserver.PullRequestInfo
			for _, pr := range prs {
				if pr.Target == env {
					g.Expect(pr.State).To(Equal("merged"), "pull request %d into %s", pr.Number, env)
					merged = append(merged, pr)
				}
			}
			g.Expect(merged).To(HaveLen(1), "pull requests into %s", env)
			info, err := f.admin.Repo(ctx, repo.ID)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(info.Branches[env]).To(Equal(merged[0].MergeCommitSHA))
		}).WithContext(ctx).WithTimeout(promotionTimeout).WithPolling(2 * time.Second).Should(Succeed())
	}
	f.expectActive(ctx, name, map[string]string{development: drySHA, staging: drySHA})
	GinkgoWriter.Printf("promoted dry commit %s through %s past branch protection\n", drySHA, p)
}

// newRepo creates a repository called repoName for provider p on the test SCM server, subscribes the promoter's
// webhook receiver to it, and returns it.
func (f promotionFlow) newRepo(ctx context.Context, p scmserver.Provider, repoName string) scmserver.RepoInfo {
	GinkgoHelper()
	req := scmserver.CreateRepoRequest{Provider: p, Owner: scmOwner(p), Name: repoName}
	if p == scmserver.AzureDevOps {
		req.Project = "e2e"
	}
	repo, err := f.admin.CreateRepo(ctx, req)
	Expect(err).NotTo(HaveOccurred())
	_, err = f.admin.AddWebhook(ctx, scmserver.WebhookSubscription{Provider: p, Repo: repo.FullName, URL: f.webhook})
	Expect(err).NotTo(HaveOccurred())
	return repo
}

// expectActive waits until the PromotionStrategy called name reports drySHAs[branch] as the active dry commit of each
// listed environment.
func (f promotionFlow) expectActive(ctx context.Context, name string, drySHAs map[string]string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var strategies v1alpha1.PromotionStrategyList
		g.Expect(f.list(ctx, "promotionstrategies", &strategies)).To(Succeed())
		var strategy *v1alpha1.PromotionStrategy
		for i := range strategies.Items {
			if strategies.Items[i].Name == name {
				strategy = &strategies.Items[i]
			}
		}
		g.Expect(strategy).NotTo(BeNil())
		active := map[string]string{}
		for _, env := range strategy.Status.Environments {
			active[env.Branch] = env.Active.Dry.Sha
		}
		for branch, drySHA := range drySHAs {
			g.Expect(active).To(HaveKeyWithValue(branch, drySHA), "active dry commit of %s", branch)
		}
	}).WithContext(ctx).WithTimeout(promotionTimeout).WithPolling(2 * time.Second).Should(Succeed())
}

// mergeByHand merges the open pull request into env through the server's admin API, as a person would in the
// provider's UI, once the pull request carries the tip of env's proposed branch and the controller has recorded the
// pull request's ID in its PullRequest resource. The promotion-history note is built from that resource's commit
// message trailers, so a merge before the controller has written them leaves the history without the pull request.
// It retries while the merge conflicts, which it does after a squash until the controller merges env back into the
// proposed branch. name names the promotion's resources. It returns the merged pull request.
func (f promotionFlow) mergeByHand(ctx context.Context, repo scmserver.RepoInfo, name, env string, squash bool,
) scmserver.PullRequestInfo {
	GinkgoHelper()
	var merged scmserver.PullRequestInfo
	Eventually(func(g Gomega) {
		info, err := f.admin.Repo(ctx, repo.ID)
		g.Expect(err).NotTo(HaveOccurred())
		prs, err := f.admin.PullRequests(ctx, repo.ID, "open")
		g.Expect(err).NotTo(HaveOccurred())
		var open []scmserver.PullRequestInfo
		for _, pr := range prs {
			if pr.Target == env {
				open = append(open, pr)
			}
		}
		g.Expect(open).To(HaveLen(1), "open pull requests into %s", env)
		g.Expect(open[0].HeadSHA).To(Equal(info.Branches[env+"-next"]),
			"the pull request does not carry the proposed tip yet")
		var resources v1alpha1.PullRequestList
		g.Expect(f.list(ctx, "pullrequests", &resources)).To(Succeed())
		trailer := fmt.Sprintf("%s: %d\n", constants.TrailerPullRequestID, open[0].Number)
		g.Expect(resources.Items).To(ContainElement(SatisfyAll(
			HaveField("Spec.RepositoryReference.Name", name),
			HaveField("Spec.TargetBranch", env),
			HaveField("Spec.Commit.Message", ContainSubstring(trailer)),
		)), "the controller has not recorded pull request %d into %s yet", open[0].Number, env)
		merged, err = f.admin.PullRequestAction(ctx, repo.ID, open[0].Number,
			scmserver.PullRequestAction{Action: "merge", Squash: squash})
		g.Expect(err).NotTo(HaveOccurred())
	}).WithContext(ctx).WithTimeout(promotionTimeout).WithPolling(2 * time.Second).Should(Succeed())
	Expect(merged.State).To(Equal("merged"))
	Expect(merged.Squashed).To(Equal(squash))
	return merged
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
			subject := d.Ref
			if d.Event == "pull_request" {
				subject = fmt.Sprintf("pull request %d %s", d.Number, d.Action)
			}
			GinkgoWriter.Printf("  %s %-11s %s %s %s -> %d %s\n", d.Time.Format(time.TimeOnly), d.Provider, d.Repo, subject,
				d.URL, d.StatusCode, d.Error)
		}
	}
}

// scmOwner is the owner (organization, namespace, workspace or project key) of the provider's e2e repository.
func scmOwner(p scmserver.Provider) string {
	if p == scmserver.BitbucketDataCenter {
		// Project keys allow only letters, digits and underscores.
		return "E2E_BITBUCKET_DC"
	}
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
		// The message names the repository so that seeds made in the same second in different repositories get
		// different SHAs. The webhook receiver finds the ChangeTransferPolicy to wake by a push's previous SHA and
		// gives up when several match, which would leave a merge made on the provider unnoticed until a requeue.
		next, err := admin.Commit(ctx, repo.ID, scmserver.CommitRequest{
			Branch: env + "-next", Orphan: true,
			Message: fmt.Sprintf("initial empty commit for %s-next in %s", env, repo.FullName),
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
// the controller at the provider's listener on the test SCM server at domain. Every object is called name. autoMerge
// sets whether the controller merges the pull requests itself, and a non-empty developmentGate is the key of a proposed
// commit status that gates promotion into development.
func promotionManifests(p scmserver.Provider, name string, repo scmserver.RepoInfo, domain string, githubKey []byte,
	autoMerge bool, developmentGate string,
) string {
	developmentStatuses := ""
	if developmentGate != "" {
		developmentStatuses = "\n      proposedCommitStatuses:\n        - key: " + developmentGate
	}
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
	case scmserver.BitbucketDataCenter:
		providerSpec = "bitbucketDataCenter:\n    domain: " + domain
		repoSpec = fmt.Sprintf("bitbucketDataCenter:\n    project: %s\n    name: %s", repo.Owner, repo.Name)
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
      autoMerge: %[5]t%[6]s
    - branch: environment/staging
      autoMerge: %[5]t
---
apiVersion: promoter.argoproj.io/v1alpha1
kind: DependentsSuccessfulCommitStatus
metadata:
  name: %[1]s
spec:
  key: dependents-successful
  promotionStrategyRef:
    name: %[1]s
`, name, secretData, providerSpec, repoSpec, autoMerge, developmentStatuses)
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n") + "\n"
}
