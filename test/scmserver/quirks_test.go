package scmserver_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/tidwall/gjson"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/github"
	"github.com/argoproj-labs/gitops-promoter/internal/webhookreceiver"
	"github.com/argoproj-labs/gitops-promoter/test/scmserver"
)

// withBehavior changes a provider's behavior for the current spec.
func withBehavior(p scmserver.Provider, change func(*scmserver.Behavior)) {
	GinkgoHelper()
	b := srv.Behavior(p)
	change(&b)
	srv.SetBehavior(p, b)
	DeferCleanup(func() { srv.SetBehavior(p, scmserver.DefaultBehavior(p)) })
}

// pushToSource commits to the fixture's source branch and returns the new head.
func (f *fixture) pushToSource(ctx context.Context, content string) string {
	GinkgoHelper()
	res, err := srv.Commit(ctx, f.repo.ID, scmserver.CommitRequest{
		Branch: sourceBranch, Files: map[string]string{"manifest.yaml": content}, Message: "re-hydrate",
	})
	Expect(err).NotTo(HaveOccurred())
	return res.SHA
}

// requestsMatching returns logged requests of provider p whose method matches and path contains fragment.
func requestsMatching(p scmserver.Provider, method, fragment string) []scmserver.RequestRecord {
	var out []scmserver.RequestRecord
	for _, rec := range srv.Requests() {
		if rec.Provider == p && rec.Method == method && strings.Contains(rec.Path, fragment) {
			out = append(out, rec)
		}
	}
	return out
}

// countRepoLabels reads X-Total-Count from the Gitea or Forgejo labels endpoint of repository fullName.
func countRepoLabels(ctx context.Context, p scmserver.Provider, fullName string) int {
	GinkgoHelper()
	url := "https://" + srv.Domain(p) + "/api/v1/repos/" + fullName + "/labels?limit=1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	Expect(err).NotTo(HaveOccurred())
	req.Header.Set("Authorization", "token "+testToken)
	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	Expect(resp.StatusCode).To(Equal(http.StatusOK))
	Expect(resp.Header.Get("Link")).To(BeEmpty(), "real servers send no Link header on label lists")
	total, err := strconv.Atoi(resp.Header.Get("X-Total-Count"))
	Expect(err).NotTo(HaveOccurred())
	return total
}

// bitbucketDC sends an authenticated request to the fixture repository's Bitbucket Data Center REST resource at
// path (relative to /rest/api/1.0/projects/{key}/repos/{slug}) and returns the status code and body.
func (f *fixture) bitbucketDC(ctx context.Context, method, path string) (int, string) {
	GinkgoHelper()
	return bitbucketDCRequest(ctx, method, f.bitbucketDCPath(path), "")
}

// bitbucketDCPost posts body to the fixture repository's Bitbucket Data Center REST resource at path.
func (f *fixture) bitbucketDCPost(ctx context.Context, path, body string) (int, string) {
	GinkgoHelper()
	return bitbucketDCRequest(ctx, http.MethodPost, f.bitbucketDCPath(path), body)
}

func (f *fixture) bitbucketDCPath(path string) string {
	return fmt.Sprintf("/rest/api/1.0/projects/%s/repos/%s%s", f.repo.Owner, f.repo.Name, path)
}

// bitbucketDCRequest sends an authenticated request to the Bitbucket Data Center listener at path and returns the
// status code and body.
func bitbucketDCRequest(ctx context.Context, method, path, body string) (int, string) {
	GinkgoHelper()
	url := "https://" + srv.Domain(scmserver.BitbucketDataCenter) + path
	req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
	Expect(err).NotTo(HaveOccurred())
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	return resp.StatusCode, string(respBody)
}

// azure sends an authenticated request to the fixture repository's Azure DevOps git resource at path (relative to
// /{org}/{project}/_apis/git/repositories/{repo}) and returns the status code and body.
func (f *fixture) azure(ctx context.Context, method, path, body string) (int, string) {
	GinkgoHelper()
	url := fmt.Sprintf("https://%s/%s/%s/_apis/git/repositories/%s%s?api-version=7.1",
		srv.Domain(scmserver.AzureDevOps), f.repo.Owner, f.repo.Project, f.repo.Name, path)
	req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
	Expect(err).NotTo(HaveOccurred())
	req.SetBasicAuth("", testToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	return resp.StatusCode, string(respBody)
}

func statusCodes(records []scmserver.RequestRecord) []int {
	codes := make([]int, 0, len(records))
	for _, rec := range records {
		codes = append(codes, rec.StatusCode)
	}
	return codes
}

var _ = Describe("provider quirks", func() {
	Context("Azure DevOps", func() {
		It("keeps a pull request active after completion is requested until the merge lands", func(ctx SpecContext) {
			withBehavior(scmserver.AzureDevOps, func(b *scmserver.Behavior) { b.CompletionDelay = scmserver.D(1500 * time.Millisecond) })
			f := newFixture(ctx, scmserver.AzureDevOps)
			prObj := f.createPR(ctx, "async-complete")
			Eventually(func(g Gomega) {
				g.Expect(f.serverPR(ctx, prObj.Status.ID).MergeabilityKnown).To(BeTrue())
			}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())
			preview := f.serverPR(ctx, prObj.Status.ID).PreviewMergeSHA
			Expect(preview).NotTo(BeEmpty())

			result, err := f.prs.Merge(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.CommitSHA).To(BeEmpty(), "Azure DevOps does not report the merge commit when completion is requested")

			By("reporting the pull request as open, and as the open pull request, while completion is queued")
			Consistently(func(g Gomega) {
				got, err := f.prs.Get(ctx, prObj)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(got.State).To(Equal(v1alpha1.PullRequestOpen))
				open, err := f.prs.FindOpen(ctx, prObj)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(open.Found).To(BeTrue())
			}).WithContext(ctx).WithTimeout(time.Second).WithPolling(pollInterval).Should(Succeed())
			Expect(f.serverPR(ctx, prObj.Status.ID).CompletionQueued).To(BeTrue())
			Expect(f.branchSHA(ctx, targetBranch)).To(Equal(f.baseSHA))

			By("completing with a merge commit that differs from the preview merge commit")
			Eventually(func(g Gomega) {
				got, err := f.prs.Get(ctx, prObj)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(got.State).To(Equal(v1alpha1.PullRequestMerged))
				g.Expect(got.MergedTargetSHA).To(Equal(f.branchSHA(ctx, targetBranch)))
			}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())
			Expect(f.branchSHA(ctx, targetBranch)).NotTo(Equal(preview))
		})

		It("refuses to complete a conflicting pull request", func(ctx SpecContext) {
			withBehavior(scmserver.AzureDevOps, func(b *scmserver.Behavior) { b.MergeabilityDelay = scmserver.D(0) })
			f := newFixture(ctx, scmserver.AzureDevOps)
			prObj := f.createPR(ctx, "conflict")
			_, err := srv.Commit(ctx, f.repo.ID, scmserver.CommitRequest{
				Branch: targetBranch, Files: map[string]string{"manifest.yaml": "replicas: 3\n"}, Message: "conflicting change",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(f.serverPR(ctx, prObj.Status.ID).Conflicts).To(BeTrue())

			_, err = f.prs.Merge(ctx, prObj)
			Expect(err).To(MatchError(ContainSubstring("merge conflicts")))
			Expect(statusCodes(requestsMatching(scmserver.AzureDevOps, http.MethodPatch, "/pullRequests/"))).To(ContainElement(http.StatusBadRequest))
			info := f.serverPR(ctx, prObj.Status.ID)
			Expect(info.CompletionQueued).To(BeFalse())
			Expect(info.State).To(Equal("open"))
		})

		It("refuses to complete a pull request whose required statuses have not passed", func(ctx SpecContext) {
			f := newFixture(ctx, scmserver.AzureDevOps)
			Expect(admin.SetProtection(ctx, f.repo.ID, targetBranch, scmserver.ProtectionRequest{RequiredStatuses: []string{"promoter-gate"}})).To(Succeed())
			prObj := f.createPR(ctx, "policy")

			_, err := f.prs.Merge(ctx, prObj)
			Expect(err).To(MatchError(ContainSubstring("required policies are not met")))
			Expect(f.serverPR(ctx, prObj.Status.ID).CompletionQueued).To(BeFalse())

			By("still refusing while the status exists only as a commit status, which status policies do not read")
			status, body := f.azure(ctx, http.MethodPost, "/commits/"+f.headSHA+"/statuses",
				`{"state":"succeeded","context":{"name":"promoter-gate","genre":"promoter"}}`)
			Expect(status).To(Equal(http.StatusCreated), body)
			_, err = f.prs.Merge(ctx, prObj)
			Expect(err).To(MatchError(ContainSubstring("required policies are not met")))

			By("completing once the provider mirrors the status onto the pull request's iteration")
			_, err = f.statuses.Set(ctx, f.commitStatus("promoter-gate", v1alpha1.CommitPhaseSuccess))
			Expect(err).NotTo(HaveOccurred())
			Expect(f.serverPR(ctx, prObj.Status.ID).Statuses).To(ContainElement(scmserver.PullRequestStatusInfo{
				Context: "promoter-gate", Genre: "promoter", State: "succeeded", IterationID: 1,
			}))
			_, err = f.prs.Merge(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
			Expect(f.serverPR(ctx, prObj.Status.ID).CompletionQueued).To(BeTrue())
		})

		It("adds an iteration for every source branch update, and a status for an older iteration no longer meets the policy", func(ctx SpecContext) {
			f := newFixture(ctx, scmserver.AzureDevOps)
			Expect(admin.SetProtection(ctx, f.repo.ID, targetBranch, scmserver.ProtectionRequest{RequiredStatuses: []string{"promoter/promoter-gate"}})).To(Succeed())
			prObj := f.createPR(ctx, "iterations")
			moved := f.pushToSource(ctx, "replicas: 4\n")
			Eventually(func(g Gomega) {
				g.Expect(f.serverPR(ctx, prObj.Status.ID).Iterations).To(Equal(2))
			}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())
			_, body := f.azure(ctx, http.MethodGet, "/pullRequests/"+prObj.Status.ID+"/iterations", "")
			Expect(gjson.Get(body, "value.#.sourceRefCommit.commitId").Value()).To(Equal([]any{f.headSHA, moved}))
			Expect(gjson.Get(body, "value.#.reason").Value()).To(Equal([]any{"create", "push"}))

			By("refusing with a successful status for the first iteration")
			_, err := f.statuses.Set(ctx, f.commitStatus("promoter-gate", v1alpha1.CommitPhaseSuccess))
			Expect(err).NotTo(HaveOccurred())
			Expect(f.serverPR(ctx, prObj.Status.ID).Statuses).To(ConsistOf(HaveField("IterationID", 1)))
			prObj.Spec.MergeSha = moved
			_, err = f.prs.Merge(ctx, prObj)
			Expect(err).To(MatchError(ContainSubstring("required policies are not met")))

			By("completing with a successful status for the latest iteration")
			latest := f.commitStatus("promoter-gate", v1alpha1.CommitPhaseSuccess)
			latest.Spec.Sha = moved
			_, err = f.statuses.Set(ctx, latest)
			Expect(err).NotTo(HaveOccurred())
			_, err = f.prs.Merge(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
			Expect(f.serverPR(ctx, prObj.Status.ID).CompletionQueued).To(BeTrue())
		})

		It("can accept a completion that fails later, when asked to model it that way", func(ctx SpecContext) {
			withBehavior(scmserver.AzureDevOps, func(b *scmserver.Behavior) {
				b.CompletionDelay = scmserver.D(0)
				b.MergeabilityDelay = scmserver.D(0)
				b.AzureAsyncCompletionFailures = true
			})
			f := newFixture(ctx, scmserver.AzureDevOps)
			prObj := f.createPR(ctx, "async-conflict")
			_, err := srv.Commit(ctx, f.repo.ID, scmserver.CommitRequest{
				Branch: targetBranch, Files: map[string]string{"manifest.yaml": "replicas: 3\n"}, Message: "conflicting change",
			})
			Expect(err).NotTo(HaveOccurred())

			_, err = f.prs.Merge(ctx, prObj)
			Expect(err).NotTo(HaveOccurred(), "the completion request is accepted; the conflict surfaces asynchronously")
			Eventually(func(g Gomega) {
				info := f.serverPR(ctx, prObj.Status.ID)
				g.Expect(info.CompletionQueued).To(BeFalse())
				g.Expect(info.CompletionFailure).To(ContainSubstring("merge conflicts"))
			}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())
			got, err := f.prs.Get(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.State).To(Equal(v1alpha1.PullRequestOpen))
		})

		It("rejects completion when the source branch moved past the expected merge SHA", func(ctx SpecContext) {
			f := newFixture(ctx, scmserver.AzureDevOps)
			prObj := f.createPR(ctx, "stale")
			moved := f.pushToSource(ctx, "replicas: 4\n")
			Eventually(func(g Gomega) {
				g.Expect(f.serverPR(ctx, prObj.Status.ID).HeadSHA).To(Equal(moved))
			}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())

			_, err := f.prs.Merge(ctx, prObj)
			Expect(err).To(MatchError(ContainSubstring("source branch has been updated")))
			Expect(statusCodes(requestsMatching(scmserver.AzureDevOps, http.MethodPatch, "/pullRequests/"))).To(ContainElement(http.StatusConflict))
			Expect(f.serverPR(ctx, prObj.Status.ID).CompletionQueued).To(BeFalse())
		})
	})

	Context("GitLab", func() {
		It("merges while detailed_merge_status is checking, as the merge API checks mergeability synchronously", func(ctx SpecContext) {
			withBehavior(scmserver.GitLab, func(b *scmserver.Behavior) { b.MergeabilityDelay = scmserver.D(1500 * time.Millisecond) })
			f := newFixture(ctx, scmserver.GitLab)
			prObj := f.createPR(ctx, "checking")
			Expect(f.serverPR(ctx, prObj.Status.ID).MergeabilityKnown).To(BeFalse())

			_, err := f.prs.Merge(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
			Expect(f.serverPR(ctx, prObj.Status.ID).State).To(Equal("merged"))
		})

		It("answers a stale or empty sha with 409 before any other check", func(ctx SpecContext) {
			f := newFixture(ctx, scmserver.GitLab)
			prObj := f.createPR(ctx, "stale-sha")
			Expect(admin.SetProtection(ctx, f.repo.ID, targetBranch, scmserver.ProtectionRequest{RequiredStatuses: []string{"promoter-gate"}})).To(Succeed())

			prObj.Spec.MergeSha = ""
			_, err := f.prs.Merge(ctx, prObj)
			Expect(err).To(MatchError(ContainSubstring("SHA does not match HEAD of source branch")))
			mergePath := fmt.Sprintf("/projects/%d/merge_requests/%s/merge", f.repo.ID, prObj.Status.ID)
			Expect(statusCodes(requestsMatching(scmserver.GitLab, http.MethodPut, mergePath))).To(Equal([]int{http.StatusConflict}))

			prObj.Spec.MergeSha = f.headSHA
			_, err = f.prs.Merge(ctx, prObj)
			Expect(err).To(MatchError(ContainSubstring("405")), "the required status has not passed")
		})

		It("reports both merge_commit_sha and squash_commit_sha for a squashed merge request", func(ctx SpecContext) {
			f := newFixture(ctx, scmserver.GitLab)
			prObj := f.createPR(ctx, "squashed")
			number, err := strconv.Atoi(prObj.Status.ID)
			Expect(err).NotTo(HaveOccurred())

			info, err := admin.PullRequestAction(ctx, f.repo.ID, number, scmserver.PullRequestAction{Action: "merge", Squash: true})
			Expect(err).NotTo(HaveOccurred())
			Expect(info.SquashCommitSHA).NotTo(BeEmpty())
			Expect(info.MergeCommitSHA).NotTo(Equal(info.SquashCommitSHA), "the merge commit merges the squash commit")
			Expect(f.branchSHA(ctx, targetBranch)).To(Equal(info.MergeCommitSHA))

			got, err := f.prs.Get(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.State).To(Equal(v1alpha1.PullRequestMerged))
			Expect(got.MergedTargetSHA).To(Equal(info.MergeCommitSHA), "the provider must report the target branch tip, not the squash commit")
		})

		It("rejects a repeated pending status with 400, which the provider treats as already done", func(ctx SpecContext) {
			f := newFixture(ctx, scmserver.GitLab)
			status := f.commitStatus("promoter-pending-twice", v1alpha1.CommitPhasePending)
			created, err := f.statuses.Set(ctx, status)
			Expect(err).NotTo(HaveOccurred())
			_, err = f.statuses.Set(ctx, status)
			Expect(err).NotTo(HaveOccurred())

			Expect(statusCodes(requestsMatching(scmserver.GitLab, http.MethodPost, "/statuses/"+f.headSHA))).To(ContainElements(http.StatusCreated, http.StatusBadRequest))
			statuses, err := admin.CommitStatuses(ctx, f.repo.ID, f.headSHA)
			Expect(err).NotTo(HaveOccurred())
			Expect(statuses).To(HaveLen(1))
			Expect(statuses[0].State).To(Equal("pending"))
			Expect(created.Status.Id).To(Equal(strconv.FormatInt(statuses[0].ID, 10)), "the 201 response carries the new status's ID")
		})

		It("is retried transparently by the GitLab client on server errors", func(ctx SpecContext) {
			f := newFixture(ctx, scmserver.GitLab)
			rule, err := srv.AddFault(scmserver.FaultRule{
				Provider: scmserver.GitLab, Method: http.MethodPost, Path: "/merge_requests$", Status: http.StatusBadGateway, Times: 1,
			})
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(srv.ClearFaults)

			prObj := f.createPR(ctx, "retried")
			Expect(prObj.Status.ID).NotTo(BeEmpty())
			Expect(statusCodes(requestsMatching(scmserver.GitLab, http.MethodPost, "/projects/"))).To(ContainElement(http.StatusBadGateway))
			Expect(rule.ID).NotTo(BeZero())
		})
	})

	DescribeTable("Gitea and Forgejo label lists carry no Link header, so the providers must page by page size",
		func(ctx SpecContext, p scmserver.Provider) {
			f := newFixture(ctx, p)
			prObj := f.createPR(ctx, "many-labels")

			// Fill more than one page (50) of repository labels through the provider, which creates missing ones.
			names := make([]string, 0, 55)
			for i := range 55 {
				names = append(names, fmt.Sprintf("label-%02d", i))
			}
			Expect(f.prs.AddLabels(ctx, prObj, names)).To(Succeed())
			Expect(countRepoLabels(ctx, p, f.repo.FullName)).To(Equal(55))

			By("resolving a label on the second page instead of creating a duplicate")
			other := f.pullRequest("more-labels")
			other.Spec.SourceBranch = "environment/development-other"
			_, err := srv.Commit(ctx, f.repo.ID, scmserver.CommitRequest{
				Branch: other.Spec.SourceBranch, Parent: f.baseSHA, Files: map[string]string{"manifest.yaml": "replicas: 6\n"},
			})
			Expect(err).NotTo(HaveOccurred())
			id, err := f.prs.Create(ctx, other.Spec.Title, other.Spec.SourceBranch, other.Spec.TargetBranch, other.Spec.Description, other)
			Expect(err).NotTo(HaveOccurred())
			other.Status.ID = id
			Expect(f.prs.AddLabels(ctx, other, []string{names[len(names)-1]})).To(Succeed())
			Expect(countRepoLabels(ctx, p, f.repo.FullName)).To(Equal(55), "the existing label on page 2 must be reused")
			Expect(f.serverPR(ctx, id).Labels).To(ConsistOf(names[len(names)-1]))
		},
		Entry("Gitea", scmserver.Gitea),
		Entry("Forgejo", scmserver.Forgejo),
	)

	DescribeTable("Gitea and Forgejo SDKs report rejected merges as not merged, which the providers return as errors",
		func(ctx SpecContext, p scmserver.Provider) {
			f := newFixture(ctx, p)
			prObj := f.createPR(ctx, "swallowed")
			moved := f.pushToSource(ctx, "replicas: 5\n")
			Eventually(func(g Gomega) {
				g.Expect(f.serverPR(ctx, prObj.Status.ID).HeadSHA).To(Equal(moved))
			}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())

			// The SDK returns false with no error for the 409; the provider must not report it as merged.
			_, err := f.prs.Merge(ctx, prObj)
			Expect(err).To(MatchError(ContainSubstring("refused to merge pull request")))

			Expect(statusCodes(requestsMatching(p, http.MethodPost, "/merge"))).To(ContainElement(http.StatusConflict))
			got, err := f.prs.Get(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.State).To(Equal(v1alpha1.PullRequestOpen))
			Expect(f.branchSHA(ctx, targetBranch)).To(Equal(f.baseSHA))
		},
		Entry("Gitea", scmserver.Gitea),
		Entry("Forgejo", scmserver.Forgejo),
	)

	Context("Bitbucket Cloud", func() {
		It("answers slow merges with a body-less 202 Accepted and completes them in the background", func(ctx SpecContext) {
			withBehavior(scmserver.BitbucketCloud, func(b *scmserver.Behavior) { b.AsyncMergeDelay = scmserver.D(time.Second) })
			f := newFixture(ctx, scmserver.BitbucketCloud)
			prObj := f.createPR(ctx, "async-merge")

			// go-bitbucket treats 202 as success and decodes the empty body as JSON, so the provider reports an error
			// and the controller retries, as it would against Bitbucket Cloud.
			mergePath := "/repositories/" + f.repo.FullName + "/pullrequests/" + prObj.Status.ID + "/merge"
			_, err := f.prs.Merge(ctx, prObj)
			Expect(err).To(MatchError(ContainSubstring("failed to merge request")))
			Expect(statusCodes(requestsMatching(scmserver.BitbucketCloud, http.MethodPost, mergePath))).To(Equal([]int{http.StatusAccepted}))
			got, err := f.prs.Get(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.State).To(Equal(v1alpha1.PullRequestOpen))

			By("answering a repeated merge of the queued pull request with the same 202")
			_, err = f.prs.Merge(ctx, prObj)
			Expect(err).To(HaveOccurred())
			Expect(statusCodes(requestsMatching(scmserver.BitbucketCloud, http.MethodPost, mergePath))).To(Equal([]int{http.StatusAccepted, http.StatusAccepted}))

			Eventually(func(g Gomega) {
				got, err := f.prs.Get(ctx, prObj)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(got.State).To(Equal(v1alpha1.PullRequestMerged))
				g.Expect(got.MergedTargetSHA).To(Equal(f.branchSHA(ctx, targetBranch)))
			}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())
		})

		It("returns abbreviated merge hashes that the provider resolves to full SHAs", func(ctx SpecContext) {
			f := newFixture(ctx, scmserver.BitbucketCloud)
			prObj := f.createPR(ctx, "short-hash")
			result, err := f.prs.Merge(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.CommitSHA).To(HaveLen(40))
			Expect(result.CommitSHA).To(Equal(f.branchSHA(ctx, targetBranch)))
			Expect(requestsMatching(scmserver.BitbucketCloud, http.MethodGet, "/commit/"+result.CommitSHA[:12])).NotTo(BeEmpty())
		})

		// TODO: confirm against real Bitbucket Cloud, with and without a merge queue on the target branch. Its merge
		// API documents no expected head SHA, and the go-bitbucket, drone/go-scm and jenkins-x/go-scm clients send none.
		It("merges a source branch that moved past the expected merge SHA", func(ctx SpecContext) {
			f := newFixture(ctx, scmserver.BitbucketCloud)
			prObj := f.createPR(ctx, "moved-head")
			moved := f.pushToSource(ctx, "replicas: 4\n")
			Eventually(func(g Gomega) {
				g.Expect(f.serverPR(ctx, prObj.Status.ID).HeadSHA).To(Equal(moved))
			}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())
			Expect(prObj.Spec.MergeSha).NotTo(Equal(moved))

			result, err := f.prs.Merge(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
			info := f.serverPR(ctx, prObj.Status.ID)
			Expect(info.State).To(Equal("merged"))
			Expect(info.HeadSHA).To(Equal(moved))
			Expect(result.CommitSHA).To(Equal(info.MergeCommitSHA))
		})
	})

	Context("Bitbucket Data Center", func() {
		It("accepts a token over git basic auth only with its owner's user name, so the provider sends a bearer header", func(ctx SpecContext) {
			f := newFixture(ctx, scmserver.BitbucketDataCenter)
			tmp := GinkgoT().TempDir()

			out, err := git(ctx, "", "clone", "--quiet", f.authenticatedCloneURL(ctx), filepath.Join(tmp, "basic"))
			Expect(err).To(HaveOccurred(), "x-token-auth is not the token's owner")
			Expect(out).To(ContainSubstring("Authentication failed"))

			out, err = f.git(ctx, "", "clone", "--quiet", f.authenticatedCloneURL(ctx), filepath.Join(tmp, "bearer"))
			Expect(err).NotTo(HaveOccurred(), out)

			cloneURL := f.gitAuth.GetGitHttpsRepoUrl(*f.gitRepo)
			ownerURL := strings.Replace(cloneURL, "https://", "https://"+scmserver.TokenUser(scmserver.BitbucketDataCenter)+":"+testToken+"@", 1)
			out, err = git(ctx, "", "clone", "--quiet", ownerURL, filepath.Join(tmp, "owner"))
			Expect(err).NotTo(HaveOccurred(), out)
		})

		It("bumps the pull request version on a rescope and refuses a merge that names the old version", func(ctx SpecContext) {
			f := newFixture(ctx, scmserver.BitbucketDataCenter)
			prObj := f.createPR(ctx, "stale-version")
			path := "/pull-requests/" + prObj.Status.ID
			_, body := f.bitbucketDC(ctx, http.MethodGet, path)
			version := gjson.Get(body, "version").Int()

			moved := f.pushToSource(ctx, "replicas: 4\n")
			Eventually(func(g Gomega) {
				_, body := f.bitbucketDC(ctx, http.MethodGet, path)
				g.Expect(gjson.Get(body, "fromRef.latestCommit").String()).To(Equal(moved))
				g.Expect(gjson.Get(body, "version").Int()).To(Equal(version + 1))
			}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())

			status, body := f.bitbucketDC(ctx, http.MethodPost, fmt.Sprintf("%s/merge?version=%d", path, version))
			Expect(status).To(Equal(http.StatusConflict))
			Expect(gjson.Get(body, "errors.0.exceptionName").String()).To(HaveSuffix("PullRequestOutOfDateException"))
			Expect(gjson.Get(body, "errors.0.currentVersion").Int()).To(Equal(version + 1))

			By("refusing, in the provider, to merge a source branch that moved past the expected merge SHA")
			_, err := f.prs.Merge(ctx, prObj)
			Expect(err).To(MatchError(ContainSubstring("does not match the expected merge SHA")))
			Expect(f.serverPR(ctx, prObj.Status.ID).State).To(Equal("open"))

			By("merging through the provider, which reads the current version first")
			prObj.Spec.MergeSha = moved
			result, err := f.prs.Merge(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.CommitSHA).To(Equal(f.branchSHA(ctx, targetBranch)))
		})

		It("refuses a second open pull request for the same branches with 409", func(ctx SpecContext) {
			f := newFixture(ctx, scmserver.BitbucketDataCenter)
			prObj := f.createPR(ctx, "duplicate")
			_, err := f.prs.Create(ctx, prObj.Spec.Title, sourceBranch, targetBranch, prObj.Spec.Description, prObj)
			Expect(err).To(MatchError(And(ContainSubstring("409"), ContainSubstring("DuplicatePullRequestException"))))
		})

		It("vetoes merging a conflicting pull request with 409", func(ctx SpecContext) {
			f := newFixture(ctx, scmserver.BitbucketDataCenter)
			prObj := f.createPR(ctx, "conflict")
			_, err := srv.Commit(ctx, f.repo.ID, scmserver.CommitRequest{
				Branch: targetBranch, Files: map[string]string{"manifest.yaml": "replicas: 3\n"}, Message: "conflicting change",
			})
			Expect(err).NotTo(HaveOccurred())

			_, err = f.prs.Merge(ctx, prObj)
			Expect(err).To(MatchError(And(ContainSubstring("409"), ContainSubstring(`"conflicted":true`))))
			Expect(f.serverPR(ctx, prObj.Status.ID).State).To(Equal("open"))
		})

		It("satisfies the Required Builds merge check only with builds the provider reports through the repository builds resource", func(ctx SpecContext) {
			f := newFixture(ctx, scmserver.BitbucketDataCenter)
			Expect(admin.SetProtection(ctx, f.repo.ID, targetBranch, scmserver.ProtectionRequest{RequiredStatuses: []string{"promoter-gate"}})).To(Succeed())
			prObj := f.createPR(ctx, "required-builds")

			By("vetoing the merge while the build is reported only through the deprecated /rest/build-status resource")
			status, body := bitbucketDCRequest(ctx, http.MethodPost, "/rest/build-status/1.0/commits/"+f.headSHA,
				`{"key":"promoter-gate","state":"SUCCESSFUL","url":"https://ci.example.com/promoter-gate"}`)
			Expect(status).To(Equal(http.StatusNoContent), body)
			_, err := f.prs.Merge(ctx, prObj)
			Expect(err).To(MatchError(And(ContainSubstring("409"), ContainSubstring("Not all required builds are successful yet"))))

			By("vetoing the merge while the repository builds resource has the build without a parent key")
			status, body = f.bitbucketDCPost(ctx, "/commits/"+f.headSHA+"/builds",
				`{"key":"promoter-gate","state":"SUCCESSFUL","url":"https://ci.example.com/promoter-gate"}`)
			Expect(status).To(Equal(http.StatusNoContent), body)
			_, err = f.prs.Merge(ctx, prObj)
			Expect(err).To(MatchError(ContainSubstring("Not all required builds are successful yet")))

			By("merging once the provider reports the build with its parent key")
			_, err = f.statuses.Set(ctx, f.commitStatus("promoter-gate", v1alpha1.CommitPhaseSuccess))
			Expect(err).NotTo(HaveOccurred())
			Expect(requestsMatching(scmserver.BitbucketDataCenter, http.MethodPost, "/commits/"+f.headSHA+"/builds")).NotTo(BeEmpty())
			_, err = f.prs.Merge(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("leaves mergeCommit out of a squash-merged pull request, which the provider reads from the MERGED activity", func(ctx SpecContext) {
			f := newFixture(ctx, scmserver.BitbucketDataCenter)
			prObj := f.createPR(ctx, "squashed")
			number, err := strconv.Atoi(prObj.Status.ID)
			Expect(err).NotTo(HaveOccurred())
			_, err = admin.PullRequestAction(ctx, f.repo.ID, number, scmserver.PullRequestAction{Action: "merge", Squash: true})
			Expect(err).NotTo(HaveOccurred())

			_, body := f.bitbucketDC(ctx, http.MethodGet, "/pull-requests/"+prObj.Status.ID)
			Expect(gjson.Get(body, "state").String()).To(Equal("MERGED"))
			Expect(gjson.Get(body, "properties.mergeCommit").Exists()).To(BeFalse())

			got, err := f.prs.Get(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.State).To(Equal(v1alpha1.PullRequestMerged))
			Expect(got.MergedTargetSHA).To(Equal(f.branchSHA(ctx, targetBranch)))
			Expect(requestsMatching(scmserver.BitbucketDataCenter, http.MethodGet, "/pull-requests/"+prObj.Status.ID+"/activities")).NotTo(BeEmpty())
		})
	})

	Context("GitHub", func() {
		It("reports an exhausted rate limit, after which go-github stops calling until the reset", func(ctx SpecContext) {
			f := newFixture(ctx, scmserver.GitHub)
			prObj := f.createPR(ctx, "rate-limit")
			withBehavior(scmserver.GitHub, func(b *scmserver.Behavior) { b.RateLimit = 2 })
			before := len(requestsMatching(scmserver.GitHub, http.MethodGet, "/pulls"))

			Eventually(func(g Gomega) {
				_, err := f.prs.FindOpen(ctx, prObj)
				g.Expect(err).To(MatchError(ContainSubstring("rate limit")))
			}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())
			Expect(len(requestsMatching(scmserver.GitHub, http.MethodGet, "/pulls"))-before).To(Equal(2),
				"once X-RateLimit-Remaining reaches 0, go-github refuses requests without sending them")
		})

		It("answers requests over the rate limit with GitHub's 403 and rate limit headers", func(ctx SpecContext) {
			f := newFixture(ctx, scmserver.GitHub)
			withBehavior(scmserver.GitHub, func(b *scmserver.Behavior) { b.RateLimit = 1 })
			url := "https://" + srv.Domain(scmserver.GitHub) + "/api/v3/repos/" + f.repo.FullName + "/pulls"
			get := func() (*http.Response, []byte) {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
				Expect(err).NotTo(HaveOccurred())
				req.Header.Set("Authorization", "token "+testToken)
				resp, err := http.DefaultClient.Do(req)
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = resp.Body.Close() }()
				body, err := io.ReadAll(resp.Body)
				Expect(err).NotTo(HaveOccurred())
				return resp, body
			}

			resp, _ := get()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(resp.Header.Get("X-Ratelimit-Limit")).To(Equal("1"))
			Expect(resp.Header.Get("X-Ratelimit-Remaining")).To(Equal("0"))
			Expect(resp.Header.Get("X-Ratelimit-Reset")).NotTo(BeEmpty())

			resp, body := get()
			Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
			Expect(resp.Header.Get("X-Ratelimit-Remaining")).To(Equal("0"))
			Expect(string(body)).To(ContainSubstring("API rate limit exceeded"))
		})

		It("rejects App JWTs signed with a key the App was not registered with", func(ctx SpecContext) {
			f := newFixture(ctx, scmserver.GitHub)
			key, err := rsa.GenerateKey(rand.Reader, 2048)
			Expect(err).NotTo(HaveOccurred())
			f.secret.Data["githubAppPrivateKey"] = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
			f.scmProvider.Spec.GitHub.AppID += 100000
			Expect(srv.RegisterGitHubApp(scmserver.RegisterGitHubAppRequest{
				AppID: f.scmProvider.Spec.GitHub.AppID, PublicKeyPEM: string(githubKeyPEM),
				Installations: []scmserver.GitHubInstallation{{ID: f.scmProvider.Spec.GitHub.AppID * 10, Account: f.repo.Owner}},
			})).To(Succeed())

			// The provider resolves the installation, signing a JWT, when it is created.
			_, err = github.NewGithubPullRequestProvider(ctx, f.k8s, f.scmProvider, *f.secret, f.repo.Owner)
			Expect(err).To(MatchError(ContainSubstring("401")))
		})

		It("hides new pull requests from list endpoints for the synthetic list lag", func(ctx SpecContext) {
			withBehavior(scmserver.GitHub, func(b *scmserver.Behavior) { b.ListLag = scmserver.D(time.Second) })
			f := newFixture(ctx, scmserver.GitHub)
			prObj := f.pullRequest("lagging")
			id, err := f.prs.Create(ctx, prObj.Spec.Title, sourceBranch, targetBranch, prObj.Spec.Description, prObj)
			Expect(err).NotTo(HaveOccurred())
			prObj.Status.ID = id

			res, err := f.prs.FindOpen(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Found).To(BeFalse())
			got, err := f.prs.Get(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.State).To(Equal(v1alpha1.PullRequestOpen), "fetching by number is not affected by list lag")
			Eventually(func(g Gomega) {
				res, err := f.prs.FindOpen(ctx, prObj)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(res.Found).To(BeTrue())
			}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())
		})

		It("blocks merges on missing required statuses until they succeed", func(ctx SpecContext) {
			f := newFixture(ctx, scmserver.GitHub)
			Expect(admin.SetProtection(ctx, f.repo.ID, targetBranch, scmserver.ProtectionRequest{RequiredStatuses: []string{"promoter-gate"}})).To(Succeed())
			prObj := f.createPR(ctx, "protected")

			_, err := f.prs.Merge(ctx, prObj)
			Expect(err).To(MatchError(ContainSubstring(`Required status check "promoter-gate" is expected.`)))

			_, err = f.statuses.Set(ctx, f.commitStatus("promoter-gate", v1alpha1.CommitPhaseSuccess))
			Expect(err).NotTo(HaveOccurred())
			_, err = f.prs.Merge(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	DescribeTable("marks a pull request merged when its head is pushed to the target branch",
		func(ctx SpecContext, p scmserver.Provider) {
			f := newFixture(ctx, p)
			prObj := f.createPR(ctx, "pushed")

			// Fast-forward the target branch to the pull request's head, as a person pushing by hand would.
			_, err := admin.SetBranch(ctx, f.repo.ID, targetBranch, scmserver.BranchRequest{SHA: f.headSHA})
			Expect(err).NotTo(HaveOccurred())

			info := f.serverPR(ctx, prObj.Status.ID)
			got, err := f.prs.Get(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
			if p == scmserver.AzureDevOps {
				Expect(info.State).To(Equal("open"), "Azure DevOps leaves the pull request active")
				Expect(got.State).To(Equal(v1alpha1.PullRequestOpen))
				return
			}
			Expect(info.State).To(Equal("merged"))
			Expect(info.MergeCommitSHA).To(Equal(f.headSHA), "a fast-forward attributes the head commit itself")
			Expect(got.State).To(Equal(v1alpha1.PullRequestMerged))
			Expect(got.MergedTargetSHA).To(Equal(f.headSHA))
			res, err := f.prs.FindOpen(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Found).To(BeFalse())
		},
		allProviderEntries,
	)

	It("attributes a merge commit pushed by hand to the pull request it merged", func(ctx SpecContext) {
		f := newFixture(ctx, scmserver.GitHub)
		prObj := f.createPR(ctx, "pushed-merge")

		dir := filepath.Join(GinkgoT().TempDir(), "clone")
		out, err := git(ctx, "", "clone", "--quiet", f.authenticatedCloneURL(ctx), dir)
		Expect(err).NotTo(HaveOccurred(), out)
		out, err = git(ctx, dir, "checkout", "--quiet", targetBranch)
		Expect(err).NotTo(HaveOccurred(), out)
		out, err = git(ctx, dir, "merge", "--quiet", "--no-ff", "-m", "merged by hand", "origin/"+sourceBranch)
		Expect(err).NotTo(HaveOccurred(), out)
		out, err = git(ctx, dir, "push", "--quiet", "origin", targetBranch)
		Expect(err).NotTo(HaveOccurred(), out)
		mergeSHA, err := git(ctx, dir, "rev-parse", "HEAD")
		Expect(err).NotTo(HaveOccurred())

		Eventually(func(g Gomega) {
			info := f.serverPR(ctx, prObj.Status.ID)
			g.Expect(info.State).To(Equal("merged"))
			g.Expect(info.MergeCommitSHA).To(Equal(mergeSHA))
		}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())
	})

	It("injects provider-native errors with fault rules", func(ctx SpecContext) {
		f := newFixture(ctx, scmserver.GitHub)
		_, err := srv.AddFault(scmserver.FaultRule{
			Provider: scmserver.GitHub, Method: http.MethodPost, Path: "/pulls$", Status: http.StatusServiceUnavailable,
			Message: "Service unavailable", Times: 1,
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(srv.ClearFaults)

		prObj := f.pullRequest("fault")
		_, err = f.prs.Create(ctx, prObj.Spec.Title, sourceBranch, targetBranch, prObj.Spec.Description, prObj)
		Expect(err).To(MatchError(ContainSubstring("503 Service unavailable")))
		_, err = f.prs.Create(ctx, prObj.Spec.Title, sourceBranch, targetBranch, prObj.Spec.Description, prObj)
		Expect(err).NotTo(HaveOccurred())
	})
})

// webhookSink records webhook deliveries and classifies them with the promoter's webhook receiver.
type webhookSink struct {
	server     *httptest.Server
	deliveries []sinkDelivery
	mu         sync.Mutex
}

type sinkDelivery struct {
	header   http.Header
	provider string
	body     []byte
}

func newWebhookSink() *webhookSink {
	sink := &webhookSink{}
	sink.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provider := (&webhookreceiver.WebhookReceiver{}).DetectProvider(r)
		body, _ := io.ReadAll(r.Body)
		sink.mu.Lock()
		sink.deliveries = append(sink.deliveries, sinkDelivery{header: r.Header.Clone(), provider: provider, body: body})
		sink.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	DeferCleanup(sink.server.Close)
	return sink
}

func (s *webhookSink) received() []sinkDelivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.deliveries)
}

var _ = Describe("push webhooks", func() {
	DescribeTable("deliver the provider's push payload, as classified by the promoter's webhook receiver",
		func(ctx SpecContext, p scmserver.Provider, detectedAs, beforePath, refPath string) {
			f := newFixture(ctx, p)
			sink := newWebhookSink()
			srv.AddWebhook(scmserver.WebhookSubscription{Provider: p, Repo: f.repo.FullName, URL: sink.server.URL, Secret: "s3cret"})

			after := f.pushToSource(ctx, "replicas: 9\n")

			var delivery sinkDelivery
			Eventually(func(g Gomega) {
				received := sink.received()
				g.Expect(received).To(HaveLen(1))
				delivery = received[0]
			}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())

			Expect(delivery.provider).To(Equal(detectedAs))
			Expect(gjson.GetBytes(delivery.body, beforePath).String()).To(Equal(f.headSHA))
			ref := gjson.GetBytes(delivery.body, refPath).String()
			if p == scmserver.BitbucketCloud {
				ref = "refs/heads/" + ref
			}
			Expect(ref).To(Equal("refs/heads/" + sourceBranch))
			Expect(string(delivery.body)).To(ContainSubstring(after))

			Eventually(func(g Gomega) {
				deliveries := srv.WebhookDeliveries()
				g.Expect(deliveries).NotTo(BeEmpty())
				last := deliveries[len(deliveries)-1]
				g.Expect(last.StatusCode).To(Equal(http.StatusNoContent))
				g.Expect(last.Before).To(Equal(f.headSHA))
				g.Expect(last.After).To(Equal(after))
			}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())
		},
		Entry("GitHub", scmserver.GitHub, webhookreceiver.ProviderGitHub, "before", "ref"),
		Entry("GitLab", scmserver.GitLab, webhookreceiver.ProviderGitLab, "before", "ref"),
		Entry("Azure DevOps", scmserver.AzureDevOps, webhookreceiver.ProviderAzureDevops, "resource.refUpdates.0.oldObjectId", "resource.refUpdates.0.name"),
		Entry("Bitbucket Cloud", scmserver.BitbucketCloud, webhookreceiver.ProviderBitbucketCloud, "push.changes.0.old.target.hash", "push.changes.0.new.name"),
		// Gitea and Forgejo also send X-GitHub-Event, which the receiver checks first.
		Entry("Gitea", scmserver.Gitea, webhookreceiver.ProviderGitHub, "before", "ref"),
		Entry("Forgejo", scmserver.Forgejo, webhookreceiver.ProviderGitHub, "before", "ref"),
		Entry("Bitbucket Data Center", scmserver.BitbucketDataCenter, webhookreceiver.ProviderBitbucketDataCenter, "changes.0.fromHash", "changes.0.refId"),
	)

	It("notifies subscribers when a merge moves the target branch", func(ctx SpecContext) {
		f := newFixture(ctx, scmserver.GitHub)
		sink := newWebhookSink()
		srv.AddWebhook(scmserver.WebhookSubscription{Provider: scmserver.GitHub, Repo: f.repo.FullName, URL: sink.server.URL})
		prObj := f.createPR(ctx, "merge-webhook")
		Eventually(func(g Gomega) {
			_, err := f.prs.Merge(ctx, prObj)
			g.Expect(err).NotTo(HaveOccurred())
		}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())

		Eventually(func(g Gomega) {
			// The pull request's own events (opened, closed) arrive on the same subscription.
			var pushes []sinkDelivery
			for _, d := range sink.received() {
				if d.header.Get("X-Github-Event") == "push" {
					pushes = append(pushes, d)
				}
			}
			g.Expect(pushes).To(HaveLen(1))
			g.Expect(gjson.GetBytes(pushes[0].body, "ref").String()).To(Equal("refs/heads/" + targetBranch))
			g.Expect(gjson.GetBytes(pushes[0].body, "before").String()).To(Equal(f.baseSHA))
			g.Expect(gjson.GetBytes(pushes[0].body, "after").String()).To(Equal(f.branchSHA(ctx, targetBranch)))
		}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())
	})

	It("drops deliveries at the configured rate", func(ctx SpecContext) {
		withBehavior(scmserver.GitLab, func(b *scmserver.Behavior) { b.WebhookDropRate = 1 })
		f := newFixture(ctx, scmserver.GitLab)
		sink := newWebhookSink()
		srv.AddWebhook(scmserver.WebhookSubscription{Provider: scmserver.GitLab, Repo: f.repo.FullName, URL: sink.server.URL})
		after := f.pushToSource(ctx, "replicas: 7\n")

		Eventually(func(g Gomega) {
			var dropped bool
			for _, d := range srv.WebhookDeliveries() {
				if d.After == after && d.Dropped {
					dropped = true
				}
			}
			g.Expect(dropped).To(BeTrue())
		}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())
		Consistently(sink.received).WithContext(ctx).WithTimeout(300 * time.Millisecond).Should(BeEmpty())
	})
})

// prEventSpec describes how one provider announces a pull request's lifecycle: how to recognize its pull request
// event and read the action, pull request ID and repository from it, the action names it uses for open, close,
// reopen and merge (an empty name means the provider sends no event for that transition), and the payload field
// that marks the merged pull request. The promoter's webhook receiver does not consume these events yet; the paths
// are what a receiver would read.
type prEventSpec struct {
	// isEvent reports whether a delivery is this provider's pull request event rather than a push.
	isEvent func(d sinkDelivery) bool
	// action reads the provider's action name from a delivery.
	action func(d sinkDelivery) string
	// idPath and repoPath are gjson paths to the pull request ID and the repository's name; repo is the expected
	// name, the repository's full name when nil.
	repo                             func(f *fixture) string
	idPath, repoPath                 string
	opened, closed, reopened, merged string
	mergedPath, mergedValue          string
}

func headerEvent(name, value string) func(d sinkDelivery) bool {
	return func(d sinkDelivery) bool { return d.header.Get(name) == value }
}

func bodyField(path string) func(d sinkDelivery) string {
	return func(d sinkDelivery) string { return gjson.GetBytes(d.body, path).String() }
}

func (e prEventSpec) actions() []string {
	var actions []string
	for _, a := range []string{e.opened, e.closed, e.reopened, e.merged} {
		if a != "" {
			actions = append(actions, a)
		}
	}
	return actions
}

var _ = Describe("pull request webhooks", func() {
	DescribeTable("announce opened, closed, reopened and merged pull requests in the provider's format",
		func(ctx SpecContext, p scmserver.Provider, detectedAs string, want prEventSpec) {
			f := newFixture(ctx, p)
			sink := newWebhookSink()
			srv.AddWebhook(scmserver.WebhookSubscription{Provider: p, Repo: f.repo.FullName, URL: sink.server.URL, Secret: "s3cret"})

			// Open, close, reopen and merge through the promoter's own provider implementation, except the reopen,
			// which the promoter never does and Bitbucket Cloud does not allow: a person does that on the provider.
			prObj := f.createPR(ctx, "pr-webhooks")
			number, err := strconv.Atoi(prObj.Status.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(f.prs.Close(ctx, prObj)).To(Succeed())
			_, err = admin.PullRequestAction(ctx, f.repo.ID, number, scmserver.PullRequestAction{Action: "reopen"})
			Expect(err).NotTo(HaveOccurred())
			Eventually(func(g Gomega) {
				_, err := f.prs.Merge(ctx, prObj)
				g.Expect(err).NotTo(HaveOccurred())
			}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())

			var events []sinkDelivery
			Eventually(func(g Gomega) {
				events = nil
				for _, d := range sink.received() {
					g.Expect(d.provider).To(Equal(detectedAs))
					if want.isEvent(d) {
						events = append(events, d)
					}
				}
				g.Expect(events).To(HaveLen(len(want.actions())))
			}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())

			// Deliveries are sent concurrently, so compare the set of actions rather than their order.
			actions := make([]string, 0, len(events))
			merged := 0
			for _, d := range events {
				actions = append(actions, want.action(d))
				Expect(gjson.GetBytes(d.body, want.idPath).String()).To(Equal(prObj.Status.ID))
				wantRepo := f.repo.FullName
				if want.repo != nil {
					wantRepo = want.repo(f)
				}
				Expect(gjson.GetBytes(d.body, want.repoPath).String()).To(Equal(wantRepo))
				if gjson.GetBytes(d.body, want.mergedPath).String() == want.mergedValue {
					merged++
				}
			}
			Expect(actions).To(ConsistOf(want.actions()))
			Expect(merged).To(Equal(1), "exactly one event carries the merged pull request")

			// The server's delivery log records the pull request events alongside the pushes.
			var logged []string
			for _, d := range srv.WebhookDeliveries() {
				if d.Repo == f.repo.FullName && d.Event == "pull_request" {
					Expect(d.Number).To(Equal(number))
					Expect(d.StatusCode).To(Equal(http.StatusNoContent))
					logged = append(logged, d.Action)
				}
			}
			wantLogged := []string{"opened", "closed", "merged"}
			if want.reopened != "" {
				wantLogged = append(wantLogged, "reopened")
			}
			Expect(logged).To(ConsistOf(wantLogged))
		},
		// https://docs.github.com/webhooks/webhook-events-and-payloads#pull_request
		Entry("GitHub", scmserver.GitHub, webhookreceiver.ProviderGitHub,
			prEventSpec{
				isEvent: headerEvent("X-Github-Event", "pull_request"), action: bodyField("action"),
				idPath: "pull_request.number", repoPath: "repository.full_name",
				opened: "opened", closed: "closed", reopened: "reopened", merged: "closed",
				mergedPath: "pull_request.merged", mergedValue: "true",
			}),
		// https://docs.gitlab.com/user/project/integrations/webhook_events/#merge-request-events
		Entry("GitLab", scmserver.GitLab, webhookreceiver.ProviderGitLab,
			prEventSpec{
				isEvent: headerEvent("X-Gitlab-Event", "Merge Request Hook"), action: bodyField("object_attributes.action"),
				idPath: "object_attributes.iid", repoPath: "project.path_with_namespace",
				opened: "open", closed: "close", reopened: "reopen", merged: "merge",
				mergedPath: "object_attributes.state", mergedValue: "merged",
			}),
		// https://learn.microsoft.com/azure/devops/service-hooks/events#git.pullrequest.updated
		Entry("Azure DevOps", scmserver.AzureDevOps, webhookreceiver.ProviderAzureDevops,
			prEventSpec{
				isEvent: func(d sinkDelivery) bool {
					return strings.HasPrefix(gjson.GetBytes(d.body, "eventType").String(), "git.pullrequest.")
				},
				action: bodyField("eventType"),
				// Azure DevOps names the repository alone; the project is resource.repository.project.name.
				idPath: "resource.pullRequestId", repoPath: "resource.repository.name", repo: func(f *fixture) string { return f.repo.Name },
				opened: "git.pullrequest.created", closed: "git.pullrequest.updated", reopened: "git.pullrequest.updated", merged: "git.pullrequest.updated",
				mergedPath: "resource.status", mergedValue: "completed",
			}),
		// https://support.atlassian.com/bitbucket-cloud/docs/event-payloads/#Pull-request-events: a declined pull request
		// cannot be reopened, so no event exists for it.
		Entry("Bitbucket Cloud", scmserver.BitbucketCloud, webhookreceiver.ProviderBitbucketCloud,
			prEventSpec{
				isEvent: func(d sinkDelivery) bool { return strings.HasPrefix(d.header.Get("X-Event-Key"), "pullrequest:") },
				action:  func(d sinkDelivery) string { return d.header.Get("X-Event-Key") },
				idPath:  "pullrequest.id", repoPath: "repository.full_name",
				opened: "pullrequest:created", closed: "pullrequest:rejected", merged: "pullrequest:fulfilled",
				mergedPath: "pullrequest.state", mergedValue: "MERGED",
			}),
		// https://docs.gitea.com/usage/webhooks: GitHub's shape and headers, so the receiver classifies them as GitHub.
		Entry("Gitea", scmserver.Gitea, webhookreceiver.ProviderGitHub,
			prEventSpec{
				isEvent: headerEvent("X-Gitea-Event", "pull_request"), action: bodyField("action"),
				idPath: "pull_request.number", repoPath: "repository.full_name",
				opened: "opened", closed: "closed", reopened: "reopened", merged: "closed",
				mergedPath: "pull_request.merged", mergedValue: "true",
			}),
		Entry("Forgejo", scmserver.Forgejo, webhookreceiver.ProviderGitHub,
			prEventSpec{
				isEvent: headerEvent("X-Forgejo-Event", "pull_request"), action: bodyField("action"),
				idPath: "pull_request.number", repoPath: "repository.full_name",
				opened: "opened", closed: "closed", reopened: "reopened", merged: "closed",
				mergedPath: "pull_request.merged", mergedValue: "true",
			}),
		// https://confluence.atlassian.com/bitbucketserver/event-payload-938025882.html: no event exists for a
		// reopened pull request.
		Entry("Bitbucket Data Center", scmserver.BitbucketDataCenter, webhookreceiver.ProviderBitbucketDataCenter,
			prEventSpec{
				isEvent: func(d sinkDelivery) bool { return strings.HasPrefix(d.header.Get("X-Event-Key"), "pr:") },
				action:  func(d sinkDelivery) string { return d.header.Get("X-Event-Key") },
				idPath:  "pullRequest.id", repoPath: "pullRequest.toRef.repository.slug", repo: func(f *fixture) string { return f.repo.Name },
				opened: "pr:opened", closed: "pr:declined", merged: "pr:merged",
				mergedPath: "pullRequest.state", mergedValue: "MERGED",
			}),
	)

	It("announces a pull request a push settles", func(ctx SpecContext) {
		f := newFixture(ctx, scmserver.GitHub)
		sink := newWebhookSink()
		srv.AddWebhook(scmserver.WebhookSubscription{Provider: scmserver.GitHub, Repo: f.repo.FullName, URL: sink.server.URL})
		prObj := f.createPR(ctx, "settled-by-push")

		// Land the source branch on the target by hand, as a person merging locally and pushing would.
		_, err := admin.SetBranch(ctx, f.repo.ID, targetBranch, scmserver.BranchRequest{SHA: f.headSHA})
		Expect(err).NotTo(HaveOccurred())

		Eventually(func(g Gomega) {
			var actions []string
			for _, d := range sink.received() {
				if d.header.Get("X-Github-Event") != "pull_request" {
					continue
				}
				g.Expect(gjson.GetBytes(d.body, "pull_request.number").String()).To(Equal(prObj.Status.ID))
				action := gjson.GetBytes(d.body, "action").String()
				actions = append(actions, action)
				if action == "closed" {
					g.Expect(gjson.GetBytes(d.body, "pull_request.merged").Bool()).To(BeTrue())
				}
			}
			g.Expect(actions).To(ConsistOf("opened", "closed"))
		}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())
	})
})
