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
	_ "embed"
	"fmt"
	"os"
	"strings"

	"github.com/argoproj-labs/gitops-promoter/internal/types/argocd"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

//go:embed testdata/ArgoCDCommitStatus.yaml
var testArgoCDCommitStatusYAML string

var _ = Describe("ArgoCDCommitStatus Controller", func() {
	Context("When unmarshalling the test data", func() {
		It("should unmarshal the ArgoCDCommitStatus resource", func() {
			err := unmarshalYamlStrict(testArgoCDCommitStatusYAML, &promoterv1alpha1.ArgoCDCommitStatus{})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("When reconciling a resource", func() {
		It("should fail if the application's SyncSource.TargetBranch is empty", func() {
			ctx := context.TODO()

			// Create a PromotionStrategy resource FIRST (dependency)
			promotionStrategy := &promoterv1alpha1.PromotionStrategy{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "example-promotion-strategy",
				},
				Spec: promoterv1alpha1.PromotionStrategySpec{
					RepositoryReference: promoterv1alpha1.ObjectReference{
						Name: "example-repo",
					},
					Environments: []promoterv1alpha1.Environment{
						{
							Branch: testEnvironmentStaging,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			// Create ArgoCDCommitStatus SECOND (before Application!)
			// This ensures the controller's secondary watch on Applications will find this resource
			commitStatus := &promoterv1alpha1.ArgoCDCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-status",
				},
				Spec: promoterv1alpha1.ArgoCDCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "example-promotion-strategy"},
					ApplicationSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": "demo"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, commitStatus)).To(Succeed())

			// Create Application LAST (with empty TargetBranch to trigger validation error)
			app := &argocd.Application{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-app",
					Labels: map[string]string{
						"app": "demo",
					},
				},
				Spec: argocd.ApplicationSpec{
					SourceHydrator: &argocd.SourceHydrator{
						SyncSource: argocd.SyncSource{
							TargetBranch: "",
						},
						DrySource: argocd.DrySource{
							RepoURL: "https://example.com/repo.git",
						},
					},
				},
				Status: argocd.ApplicationStatus{
					Health: argocd.HealthStatus{
						Status: "Healthy",
					},
					Sync: argocd.SyncStatus{
						Status:   "Synced",
						Revision: "abc123",
					},
				},
			}
			Expect(k8sClient.Create(ctx, app)).To(Succeed())

			// Wait for reconciliation and check status condition
			Eventually(func(g Gomega) {
				updated := &promoterv1alpha1.ArgoCDCommitStatus{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "test-status", Namespace: "default"}, updated)
				g.Expect(err).ToNot(HaveOccurred())

				c := meta.FindStatusCondition(updated.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(c).ToNot(BeNil())
				g.Expect(c.Message).To(ContainSubstring("spec.sourceHydrator.syncSource.targetBranch must not be empty"))
			}, constants.EventuallyTimeout).Should(Succeed())

			// Clean up
			Expect(k8sClient.Delete(ctx, app)).To(Succeed())
			Expect(k8sClient.Delete(ctx, promotionStrategy)).To(Succeed())
			Expect(k8sClient.Delete(ctx, commitStatus)).To(Succeed())
		})

		It("should reconcile when sync status changes but health status and LastTransitionTime do not change", func() {
			ctx := context.TODO()

			// Create required dependencies using helper function
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy := promotionStrategyResource(ctx, "sync-bug-test", "default")

			// Set up a real git repository on the test server
			setupInitialTestGitRepoOnServer(ctx, name, name)

			// Simplify to just one environment for this test
			promotionStrategy.Spec.Environments = []promoterv1alpha1.Environment{
				{Branch: testEnvironmentStaging},
			}

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			// Create ControllerConfiguration to enable watching local applications
			controllerConfig := &promoterv1alpha1.ControllerConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "controller-config",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.ControllerConfigurationSpec{
					ArgoCDCommitStatus: promoterv1alpha1.ArgoCDCommitStatusConfiguration{
						WatchLocalApplications: true,
						WorkQueue: promoterv1alpha1.WorkQueue{
							RequeueDuration:         metav1.Duration{Duration: 5 * 60 * 1000000000}, // 5 minutes in nanoseconds
							MaxConcurrentReconciles: 10,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, controllerConfig)).To(Succeed())

			// Clone the repo to a work tree so we can read and make commits
			workTreePath, err := os.MkdirTemp("", "*")
			Expect(err).ToNot(HaveOccurred())
			defer func() {
				_ = os.RemoveAll(workTreePath)
			}()

			_, err = runGitCmd(ctx, workTreePath, "clone", fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPort, name, name), ".")
			Expect(err).ToNot(HaveOccurred())
			_, err = runGitCmd(ctx, workTreePath, "config", "user.name", "testuser")
			Expect(err).ToNot(HaveOccurred())
			_, err = runGitCmd(ctx, workTreePath, "config", "user.email", "testemail@test.com")
			Expect(err).ToNot(HaveOccurred())

			// Checkout the staging branch
			_, err = runGitCmd(ctx, workTreePath, "checkout", testEnvironmentStaging)
			Expect(err).ToNot(HaveOccurred())

			// Get initial git commit SHA
			sha, err := runGitCmd(ctx, workTreePath, "rev-parse", "HEAD")
			Expect(err).ToNot(HaveOccurred())
			sha = strings.TrimSpace(sha)

			// Create Application with initial state:
			// - Health: Healthy (no health checks configured, so Argo assumes Healthy)
			// - Sync: Synced
			// - Revision: current HEAD sha
			// - LastTransitionTime: nil (no health transitions)
			app := &argocd.Application{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Application",
					APIVersion: "argoproj.io/v1alpha1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-app-sync-bug",
					Labels: map[string]string{
						"test": "sync-status-bug",
					},
				},
				Spec: argocd.ApplicationSpec{
					SourceHydrator: &argocd.SourceHydrator{
						SyncSource: argocd.SyncSource{
							TargetBranch: testEnvironmentStaging,
						},
						DrySource: argocd.DrySource{
							RepoURL: fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPort, name, name),
						},
					},
				},
				Status: argocd.ApplicationStatus{
					Health: argocd.HealthStatus{
						Status:             argocd.HealthStatusHealthy, // No health checks configured, so Argo assumes Healthy
						LastTransitionTime: nil,                        // No transitions
					},
					Sync: argocd.SyncStatus{
						Status:   argocd.SyncStatusCodeSynced,
						Revision: sha,
					},
				},
			}
			// Create the application in the local cluster (simpler than multi-cluster setup)
			Expect(k8sClient.Create(ctx, app)).To(Succeed())

			// Wait for the application to be fully created and available
			Eventually(func(g Gomega) {
				found := &argocd.Application{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "test-app-sync-bug", Namespace: "default"}, found)
				g.Expect(err).ToNot(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())

			// Create ArgoCDCommitStatus
			commitStatus := &promoterv1alpha1.ArgoCDCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      name,
				},
				Spec: promoterv1alpha1.ArgoCDCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
					ApplicationSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"test": "sync-status-bug"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, commitStatus)).To(Succeed())

			// Step 1: Verify initial state is recorded (Healthy + Synced = Success)
			Eventually(func(g Gomega) {
				updated := &promoterv1alpha1.ArgoCDCommitStatus{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, updated)
				g.Expect(err).ToNot(HaveOccurred())

				// Verify that the application is selected with initial revision
				g.Expect(updated.Status.ApplicationsSelected).To(HaveLen(1))
				g.Expect(updated.Status.ApplicationsSelected[0].Name).To(Equal("test-app-sync-bug"))
				g.Expect(updated.Status.ApplicationsSelected[0].Sha).To(Equal(sha))
				g.Expect(updated.Status.ApplicationsSelected[0].Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			}, constants.EventuallyTimeout).Should(Succeed())

			// Step 2: Create a new commit in the git repository
			testFile := workTreePath + "/test-change.txt"
			err = os.WriteFile(testFile, []byte("test change for bug reproduction"), 0o644)
			Expect(err).ToNot(HaveOccurred())
			_, err = runGitCmd(ctx, workTreePath, "add", "test-change.txt")
			Expect(err).ToNot(HaveOccurred())
			_, err = runGitCmd(ctx, workTreePath, "commit", "-m", "test change")
			Expect(err).ToNot(HaveOccurred())
			_, err = runGitCmd(ctx, workTreePath, "push")
			Expect(err).ToNot(HaveOccurred())

			// Get the new SHA
			newSha, err := runGitCmd(ctx, workTreePath, "rev-parse", "HEAD")
			Expect(err).ToNot(HaveOccurred())
			newSha = strings.TrimSpace(newSha)

			// Step 3: Simulate a new commit being detected
			// Update the Application with new revision and OutOfSync status
			// (Application CRD has no status subresource, so Argo CD patches the whole CR)
			appToUpdate := &argocd.Application{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: "test-app-sync-bug", Namespace: "default"}, appToUpdate)
			Expect(err).ToNot(HaveOccurred())

			appToUpdate.Status.Sync.Revision = newSha
			appToUpdate.Status.Sync.Status = argocd.SyncStatusCodeOutOfSync
			// Health status and LastTransitionTime remain unchanged (no health checks)
			appToUpdate.Status.Health.Status = argocd.HealthStatusHealthy
			appToUpdate.Status.Health.LastTransitionTime = nil

			err = k8sClient.Update(ctx, appToUpdate)
			Expect(err).ToNot(HaveOccurred())

			// Step 4: Verify OutOfSync state is recorded (Healthy + OutOfSync = Pending)
			Eventually(func(g Gomega) {
				updated := &promoterv1alpha1.ArgoCDCommitStatus{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, updated)
				g.Expect(err).ToNot(HaveOccurred())

				// Verify that the application is updated with new revision and is in Pending phase
				g.Expect(updated.Status.ApplicationsSelected).To(HaveLen(1))
				g.Expect(updated.Status.ApplicationsSelected[0].Sha).To(Equal(newSha))
				g.Expect(updated.Status.ApplicationsSelected[0].Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
			}, constants.EventuallyTimeout).Should(Succeed())

			// Step 5: Simulate sync completing
			// Update the Application: sync status goes to Synced (revision stays at newSha)
			appToUpdate = &argocd.Application{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: "test-app-sync-bug", Namespace: "default"}, appToUpdate)
			Expect(err).ToNot(HaveOccurred())

			appToUpdate.Status.Sync.Status = argocd.SyncStatusCodeSynced
			// Revision stays the same (newSha)
			// Health status and LastTransitionTime remain unchanged - KEY TO BUG
			appToUpdate.Status.Health.Status = argocd.HealthStatusHealthy
			appToUpdate.Status.Health.LastTransitionTime = nil

			err = k8sClient.Update(ctx, appToUpdate)
			Expect(err).ToNot(HaveOccurred())

			// Step 6: Wait for reconciliation and verify that the status reflects the Synced state
			// The bug would manifest as the controller not reconciling after the second update
			Eventually(func(g Gomega) {
				// Check that the ArgoCDCommitStatus has been updated with the application
				updated := &promoterv1alpha1.ArgoCDCommitStatus{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, updated)
				g.Expect(err).ToNot(HaveOccurred())

				// Verify that the application is selected
				g.Expect(updated.Status.ApplicationsSelected).To(HaveLen(1))
				g.Expect(updated.Status.ApplicationsSelected[0].Name).To(Equal("test-app-sync-bug"))
				g.Expect(updated.Status.ApplicationsSelected[0].Sha).To(Equal(newSha))
				// Since health is Healthy and sync is Synced, phase should be Success
				g.Expect(updated.Status.ApplicationsSelected[0].Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			}, constants.EventuallyTimeout).Should(Succeed())

			// Clean up
			Expect(k8sClient.Delete(ctx, app)).To(Succeed())
			Expect(k8sClient.Delete(ctx, commitStatus)).To(Succeed())
			Expect(k8sClient.Delete(ctx, controllerConfig)).To(Succeed())
			Expect(k8sClient.Delete(ctx, promotionStrategy)).To(Succeed())
			Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
		})
	})
})
