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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/internal/types/argocd"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

const (
	migrationWave0            = "wave-0"
	migrationGateGitKey       = "gcs-migration"
	migrationGateWebKey       = "wrs-migration"
	migrationGateScheduledKey = "scs-migration"
	migrationAppLabelKey      = "instance-id-migration-app"
)

func setInstanceIDLabel(obj metav1.Object, instanceID string) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[promoterv1alpha1.InstanceIDLabel] = instanceID
	obj.SetLabels(labels)
}

func patchInstanceIDLabel(ctx context.Context, cl client.Client, obj client.Object, instanceID string) {
	key := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
	baseObj, ok := obj.DeepCopyObject().(client.Object)
	Expect(ok).To(BeTrue())
	Expect(cl.Get(ctx, key, obj)).To(Succeed())
	setInstanceIDLabel(obj, instanceID)
	Expect(cl.Patch(ctx, obj, client.MergeFrom(baseObj))).To(Succeed())
}

func expectGateCommitStatusUnlabeled(ctx context.Context, g Gomega, cl client.Client, gate client.Object, branch string) {
	commitStatusName := utils.CommitStatusResourceName(ctx, gate, branch)
	var cs promoterv1alpha1.CommitStatus
	g.Expect(cl.Get(ctx, types.NamespacedName{Name: commitStatusName, Namespace: gate.GetNamespace()}, &cs)).To(Succeed())
	_, has := cs.Labels[promoterv1alpha1.InstanceIDLabel]
	g.Expect(has).To(BeFalse(), "CommitStatus %s should be unlabeled before migration", commitStatusName)
}

func expectGateMigrated(ctx context.Context, g Gomega, cl client.Client, gate client.Object, branch, instanceID string) {
	key := types.NamespacedName{Name: gate.GetName(), Namespace: gate.GetNamespace()}
	g.Expect(cl.Get(ctx, key, gate)).To(Succeed())
	g.Expect(gate.GetLabels()).To(HaveKeyWithValue(promoterv1alpha1.InstanceIDLabel, instanceID))

	commitStatusName := utils.CommitStatusResourceName(ctx, gate, branch)
	var cs promoterv1alpha1.CommitStatus
	g.Expect(cl.Get(ctx, types.NamespacedName{Name: commitStatusName, Namespace: gate.GetNamespace()}, &cs)).To(Succeed())
	g.Expect(cs.Labels).To(HaveKeyWithValue(promoterv1alpha1.InstanceIDLabel, instanceID),
		"CommitStatus for gate %s should inherit instance-id after migration", gate.GetName())

	_, ok := gate.(utils.PromoterResource)
	g.Expect(ok).To(BeTrue(),
		"%T must implement utils.PromoterResource (SetStatusInstanceID)", gate)
	statusID := reflect.ValueOf(gate).Elem().FieldByName("Status").FieldByName("InstanceID")
	g.Expect(statusID.IsValid()).To(BeTrue(),
		"%T missing Status.InstanceID *string; add the field and SetStatusInstanceID on the API type", gate)
	g.Expect(statusID.IsNil()).To(BeFalse(),
		"%T status.instanceID unset after migration. Ensure the reconciler calls ensureControllerInstanceIDStable "+
			"and defers HandleReconciliationResult so status is stamped from ControllerConfiguration.spec.instanceID",
		gate)
	g.Expect(statusID.Elem().String()).To(Equal(instanceID),
		"%T status.instanceID = %q, want %q after migration", gate, statusID.Elem().String(), instanceID)
}

var _ = Describe("Instance ID migration", Ordered, func() {
	var (
		migCtx    context.Context
		migEnv    *envtest.Environment
		migCfg    *rest.Config
		migClient client.Client
	)

	BeforeAll(func() {
		migCtx = context.Background()
		migEnv, migCfg, migClient = createAndStartTestEnv()
	})

	AfterAll(func() {
		Expect(migEnv.Stop()).To(Succeed())
	})

	It("DefaultToMultiInstall_whenRunbookFollowed", func() {
		ctx := migCtx
		wave0 := migrationWave0

		controllerConfiguration, err := loadShippedControllerConfigurationForTests(settings.ControllerConfigurationName)
		Expect(err).NotTo(HaveOccurred())
		Expect(migClient.Create(ctx, controllerConfiguration)).To(Succeed())

		cancelDefault := startPartitionedManager(ctx, migCfg, "default", nil)

		psName, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy := promotionStrategyResource(ctx, "instance-id-migration", "default")
		promotionStrategy.Spec.Environments = []promoterv1alpha1.Environment{
			{Branch: testBranchDevelopment},
		}
		promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
			{Key: promoterv1alpha1.TimedCommitStatusDefaultKey},
			{Key: promoterv1alpha1.ArgoCDCommitStatusDefaultKey},
		}
		promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
			{Key: migrationGateGitKey},
			{Key: migrationGateWebKey},
			{Key: migrationGateScheduledKey},
			{Key: promoterv1alpha1.PreviousEnvironmentCommitStatusKey},
		}

		timedCommitStatus := &promoterv1alpha1.TimedCommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      psName + "-timer",
				Namespace: "default",
			},
			Spec: promoterv1alpha1.TimedCommitStatusSpec{
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: psName},
				Environments: []promoterv1alpha1.TimedCommitStatusEnvironments{
					{
						Branch:   testBranchDevelopment,
						Duration: metav1.Duration{Duration: time.Second},
					},
				},
			},
		}

		gitCommitStatus := &promoterv1alpha1.GitCommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      psName + "-git",
				Namespace: "default",
			},
			Spec: promoterv1alpha1.GitCommitStatusSpec{
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: psName},
				Key:                  migrationGateGitKey,
				Expression:           `Commit.Author != ""`,
			},
		}

		testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"approved": true})
		}))
		defer testServer.Close()

		webRequestCommitStatus := &promoterv1alpha1.WebRequestCommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      psName + "-web",
				Namespace: "default",
			},
			Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: psName},
				Key:                  migrationGateWebKey,
				ReportOn:             constants.CommitRefProposed,
				HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
					URLTemplate:    testServer.URL + `/validate/{{ range .PromotionStrategy.Status.Environments }}{{ if eq .Branch $.Branch }}{{ .Proposed.Hydrated.Sha }}{{ end }}{{ end }}`,
					MethodTemplate: "GET",
					Timeout:        metav1.Duration{Duration: 10 * time.Second},
				},
				Success: promoterv1alpha1.SuccessSpec{
					When: promoterv1alpha1.WhenWithOutputSpec{
						Expression: `Response != nil ? (Response.StatusCode == 200 && Response.Body.approved == true) : Phase == "success"`,
					},
				},
				Mode: promoterv1alpha1.ModeSpec{
					Polling: &promoterv1alpha1.PollingModeSpec{
						Interval: metav1.Duration{Duration: 30 * time.Second},
					},
				},
			},
		}

		scheduledCommitStatus := &promoterv1alpha1.ScheduledCommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      psName + "-scheduled",
				Namespace: "default",
			},
			Spec: promoterv1alpha1.ScheduledCommitStatusSpec{
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: psName},
				Key:                  migrationGateScheduledKey,
				Environments: []promoterv1alpha1.ScheduledEnvironment{
					{
						Branch: testBranchDevelopment,
						Allow: []promoterv1alpha1.CronWindow{
							{
								Description: "always",
								Cron:        "0 0 * * *",
								Duration:    metav1.Duration{Duration: 24 * time.Hour},
							},
						},
					},
				},
			},
		}

		previousEnvironmentCommitStatus := &promoterv1alpha1.PreviousEnvironmentCommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      psName,
				Namespace: "default",
			},
			Spec: promoterv1alpha1.PreviousEnvironmentCommitStatusSpec{
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: psName},
				Key:                  promoterv1alpha1.PreviousEnvironmentCommitStatusKey,
			},
		}

		setupInitialTestGitRepoOnServer(ctx, gitRepo)
		Expect(migClient.Create(ctx, scmSecret)).To(Succeed())
		Expect(migClient.Create(ctx, scmProvider)).To(Succeed())
		Expect(migClient.Create(ctx, gitRepo)).To(Succeed())
		Expect(migClient.Create(ctx, promotionStrategy)).To(Succeed())
		Expect(migClient.Create(ctx, previousEnvironmentCommitStatus)).To(Succeed())
		Expect(migClient.Create(ctx, timedCommitStatus)).To(Succeed())
		Expect(migClient.Create(ctx, gitCommitStatus)).To(Succeed())
		Expect(migClient.Create(ctx, webRequestCommitStatus)).To(Succeed())
		Expect(migClient.Create(ctx, scheduledCommitStatus)).To(Succeed())

		workTreePath, err := os.MkdirTemp("", "instance-id-migration-*")
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = os.RemoveAll(workTreePath) }()

		_, err = runGitCmd(ctx, workTreePath, "clone", testGitRepoCloneURL(gitRepo), ".")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(ctx, workTreePath, "checkout", testBranchDevelopment)
		Expect(err).NotTo(HaveOccurred())
		sha, err := runGitCmd(ctx, workTreePath, "rev-parse", "HEAD")
		Expect(err).NotTo(HaveOccurred())
		sha = strings.TrimSpace(sha)

		argoApplication := &argocd.Application{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Application",
				APIVersion: "argoproj.io/v1alpha1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      psName + "-app",
				Labels: map[string]string{
					migrationAppLabelKey: psName,
				},
			},
			Spec: argocd.ApplicationSpec{
				Source: &argocd.Source{
					RepoURL:        testGitRepoCloneURL(gitRepo),
					TargetRevision: testBranchDevelopment,
				},
			},
			Status: argocd.ApplicationStatus{
				Health: argocd.HealthStatus{Status: argocd.HealthStatusHealthy},
				Sync: argocd.SyncStatus{
					Status:   argocd.SyncStatusCodeSynced,
					Revision: sha,
				},
			},
		}
		Expect(migClient.Create(ctx, argoApplication)).To(Succeed())

		argoCDCommitStatus := &promoterv1alpha1.ArgoCDCommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      psName + "-argocd",
				Namespace: "default",
			},
			Spec: promoterv1alpha1.ArgoCDCommitStatusSpec{
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: psName},
				ApplicationSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{migrationAppLabelKey: psName},
				},
			},
		}
		Expect(migClient.Create(ctx, argoCDCommitStatus)).To(Succeed())

		psKey := types.NamespacedName{Name: psName, Namespace: "default"}
		migrationGates := []client.Object{
			timedCommitStatus,
			gitCommitStatus,
			webRequestCommitStatus,
			scheduledCommitStatus,
			argoCDCommitStatus,
		}

		Eventually(func(g Gomega) {
			var ctpList promoterv1alpha1.ChangeTransferPolicyList
			g.Expect(migClient.List(ctx, &ctpList, client.InNamespace("default"),
				client.MatchingLabels{promoterv1alpha1.PromotionStrategyLabel: utils.KubeSafeLabel(psName)})).To(Succeed())
			g.Expect(ctpList.Items).NotTo(BeEmpty())
			for _, ctp := range ctpList.Items {
				_, has := ctp.Labels[promoterv1alpha1.InstanceIDLabel]
				g.Expect(has).To(BeFalse(), "CTP %s should be unlabeled before migration", ctp.Name)
			}

			for _, gate := range migrationGates {
				expectGateCommitStatusUnlabeled(ctx, g, migClient, gate, testBranchDevelopment)
			}
		}, constants.EventuallyTimeout).Should(Succeed())

		cancelDefault()

		patchInstanceIDLabel(ctx, migClient, promotionStrategy, wave0)
		patchInstanceIDLabel(ctx, migClient, scmSecret, wave0)
		patchInstanceIDLabel(ctx, migClient, previousEnvironmentCommitStatus, wave0)
		var dag promoterv1alpha1.DAGCommitStatus
		Expect(migClient.Get(ctx, types.NamespacedName{Name: psName, Namespace: "default"}, &dag)).To(Succeed())
		patchInstanceIDLabel(ctx, migClient, &dag, wave0)
		for _, gate := range migrationGates {
			patchInstanceIDLabel(ctx, migClient, gate, wave0)
		}

		var cc promoterv1alpha1.ControllerConfiguration
		Expect(migClient.Get(ctx, types.NamespacedName{
			Name:      settings.ControllerConfigurationName,
			Namespace: "default",
		}, &cc)).To(Succeed())
		ccBase := cc.DeepCopy()
		cc.Spec.InstanceID = &wave0
		Expect(migClient.Patch(ctx, &cc, client.MergeFrom(ccBase))).To(Succeed())

		cancelWave0 := startPartitionedManager(ctx, migCfg, "default", &wave0)
		defer cancelWave0()

		Eventually(func(g Gomega) {
			var ps promoterv1alpha1.PromotionStrategy
			g.Expect(migClient.Get(ctx, psKey, &ps)).To(Succeed())
			g.Expect(ps.Labels).To(HaveKeyWithValue(promoterv1alpha1.InstanceIDLabel, wave0))
			g.Expect(ps.Status.InstanceID).NotTo(BeNil())
			g.Expect(*ps.Status.InstanceID).To(Equal(wave0))

			var ctpList promoterv1alpha1.ChangeTransferPolicyList
			g.Expect(migClient.List(ctx, &ctpList, client.InNamespace("default"),
				client.MatchingLabels{promoterv1alpha1.PromotionStrategyLabel: utils.KubeSafeLabel(psName)})).To(Succeed())
			g.Expect(ctpList.Items).NotTo(BeEmpty())
			for _, ctp := range ctpList.Items {
				g.Expect(ctp.Labels).To(HaveKeyWithValue(promoterv1alpha1.InstanceIDLabel, wave0))
			}

			for _, gate := range []client.Object{timedCommitStatus, gitCommitStatus, webRequestCommitStatus, scheduledCommitStatus} {
				expectGateMigrated(ctx, g, migClient, gate, testBranchDevelopment, wave0)
			}

			// ArgoCDCommitStatus child propagation after partition restart depends on multicluster
			// Application engagement; verify the labeled root gate here (child coverage is in
			// argocdcommitstatus_controller_test).
			var acs promoterv1alpha1.ArgoCDCommitStatus
			g.Expect(migClient.Get(ctx, types.NamespacedName{Name: psName + "-argocd", Namespace: "default"}, &acs)).To(Succeed())
			g.Expect(acs.Labels).To(HaveKeyWithValue(promoterv1alpha1.InstanceIDLabel, wave0))

			commitStatusName := utils.CommitStatusResourceName(ctx, timedCommitStatus, testBranchDevelopment)
			var cs promoterv1alpha1.CommitStatus
			g.Expect(migClient.Get(ctx, types.NamespacedName{Name: commitStatusName, Namespace: "default"}, &cs)).To(Succeed())
			g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess),
				"gating should still work after migration")
		}, constants.EventuallyTimeout).Should(Succeed())
	})
})
