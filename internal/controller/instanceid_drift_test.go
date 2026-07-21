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
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

const testValidCommitSHA = "abcdef1234567890abcdef1234567890abcdef12"

type instanceIDDriftSetup func(ctx context.Context) (utils.PromoterResource, types.NamespacedName, func())

var _ = Describe("When ControllerConfiguration.spec.instanceID drifts from bootstrap", Serial, func() {
	var ctx context.Context
	var restoreControllerConfiguration func()

	BeforeEach(func() {
		ctx = context.Background()
		restoreControllerConfiguration = patchControllerConfigurationInstanceID(ctx, ptr.To("wave-0"))
	})

	AfterEach(func() {
		restoreControllerConfiguration()
	})

	DescribeTable("sets Ready=False on reconciled resources",
		func(setup instanceIDDriftSetup) {
			obj, key, cleanup := setup(ctx)
			defer cleanup()
			expectReadyFalseOnInstanceIDDrift(ctx, key, obj)
		},
		Entry("ScmProvider", setupInstanceIDDriftScmProvider),
		Entry("ClusterScmProvider", setupInstanceIDDriftClusterScmProvider),
		Entry("GitRepository", setupInstanceIDDriftGitRepository),
		Entry("PromotionStrategy", setupInstanceIDDriftPromotionStrategy),
		Entry("ChangeTransferPolicy", setupInstanceIDDriftChangeTransferPolicy),
		Entry("PullRequest", setupInstanceIDDriftPullRequest),
		Entry("CommitStatus", setupInstanceIDDriftCommitStatus),
		Entry("TimedCommitStatus", setupInstanceIDDriftTimedCommitStatus),
		Entry("ScheduledCommitStatus", setupInstanceIDDriftScheduledCommitStatus),
		Entry("GitCommitStatus", setupInstanceIDDriftGitCommitStatus),
		Entry("WebRequestCommitStatus", setupInstanceIDDriftWebRequestCommitStatus),
		Entry("ArgoCDCommitStatus", setupInstanceIDDriftArgoCDCommitStatus),
	)
})

func instanceIDDriftResourceName(kind string) string {
	return fmt.Sprintf(
		"instance-id-drift-%s-%s",
		strings.ToLower(kind),
		utils.KubeSafeUniqueName(randomString(8)),
	)
}

func patchControllerConfigurationInstanceID(ctx context.Context, instanceID *string) func() {
	cc := &promoterv1alpha1.ControllerConfiguration{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{
		Name:      settings.ControllerConfigurationName,
		Namespace: "default",
	}, cc)).To(Succeed())
	base := cc.DeepCopy()
	cc.Spec.InstanceID = instanceID
	Expect(k8sClient.Patch(ctx, cc, client.MergeFrom(base))).To(Succeed())

	return func() {
		restore := &promoterv1alpha1.ControllerConfiguration{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name:      settings.ControllerConfigurationName,
			Namespace: "default",
		}, restore)).To(Succeed())
		restoreBase := restore.DeepCopy()
		restore.Spec.InstanceID = nil
		Expect(k8sClient.Patch(ctx, restore, client.MergeFrom(restoreBase))).To(Succeed())
	}
}

func expectReadyFalseOnInstanceIDDrift(ctx context.Context, key types.NamespacedName, obj utils.PromoterResource) {
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key, obj)).To(Succeed())
		ready := meta.FindStatusCondition(*obj.GetConditions(), string(conditions.Ready))
		g.Expect(ready).NotTo(BeNil())
		g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(ready.Message).To(ContainSubstring("drifted since startup"))
	}, constants.EventuallyTimeout).Should(Succeed())
}

func deleteIgnoringNotFound(ctx context.Context, obj client.Object) {
	Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, obj))).To(Succeed())
}

func setupInstanceIDDriftScmProvider(ctx context.Context) (utils.PromoterResource, types.NamespacedName, func()) {
	name := instanceIDDriftResourceName("ScmProvider")
	key := types.NamespacedName{Name: name, Namespace: "default"}
	sp := &promoterv1alpha1.ScmProvider{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       promoterv1alpha1.ScmProviderSpec{Fake: &promoterv1alpha1.Fake{}},
	}
	Expect(k8sClient.Create(ctx, sp)).To(Succeed())
	return sp, key, func() { deleteIgnoringNotFound(ctx, sp) }
}

func setupInstanceIDDriftClusterScmProvider(ctx context.Context) (utils.PromoterResource, types.NamespacedName, func()) {
	name := instanceIDDriftResourceName("ClusterScmProvider")
	key := types.NamespacedName{Name: name, Namespace: "default"}
	csp := &promoterv1alpha1.ClusterScmProvider{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       promoterv1alpha1.ScmProviderSpec{Fake: &promoterv1alpha1.Fake{}},
	}
	Expect(k8sClient.Create(ctx, csp)).To(Succeed())
	return csp, key, func() { deleteIgnoringNotFound(ctx, csp) }
}

func setupInstanceIDDriftGitRepository(ctx context.Context) (utils.PromoterResource, types.NamespacedName, func()) {
	name := instanceIDDriftResourceName("GitRepository")
	key := types.NamespacedName{Name: name, Namespace: "default"}
	gr := &promoterv1alpha1.GitRepository{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: promoterv1alpha1.GitRepositorySpec{
			Fake: &promoterv1alpha1.FakeRepo{Owner: name, Name: name},
			ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{
				Kind: promoterv1alpha1.ScmProviderKind,
				Name: name + "-scm",
			},
		},
	}
	Expect(k8sClient.Create(ctx, gr)).To(Succeed())
	return gr, key, func() { deleteIgnoringNotFound(ctx, gr) }
}

func setupInstanceIDDriftPromotionStrategy(ctx context.Context) (utils.PromoterResource, types.NamespacedName, func()) {
	name := instanceIDDriftResourceName("PromotionStrategy")
	key := types.NamespacedName{Name: name, Namespace: "default"}
	ps := &promoterv1alpha1.PromotionStrategy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: promoterv1alpha1.PromotionStrategySpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{Name: name + "-repo"},
			Environments: []promoterv1alpha1.Environment{
				{Branch: testBranchDevelopment},
			},
		},
		Status: promoterv1alpha1.PromotionStrategyStatus{
			Environments: []promoterv1alpha1.EnvironmentStatus{
				{Branch: testBranchDevelopment},
			},
		},
	}
	Expect(k8sClient.Create(ctx, ps)).To(Succeed())
	Expect(k8sClient.Get(ctx, key, ps)).To(Succeed())
	ps.Status.Environments = []promoterv1alpha1.EnvironmentStatus{{Branch: testBranchDevelopment}}
	Expect(k8sClient.Status().Update(ctx, ps)).To(Succeed())
	return ps, key, func() { deleteIgnoringNotFound(ctx, ps) }
}

func setupInstanceIDDriftChangeTransferPolicy(ctx context.Context) (utils.PromoterResource, types.NamespacedName, func()) {
	name := instanceIDDriftResourceName("ChangeTransferPolicy")
	key := types.NamespacedName{Name: name, Namespace: "default"}
	ctp := &promoterv1alpha1.ChangeTransferPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: promoterv1alpha1.ChangeTransferPolicySpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{Name: name + "-repo"},
			ProposedBranch:      testBranchDevelopmentNext,
			ActiveBranch:        testBranchDevelopment,
		},
	}
	Expect(k8sClient.Create(ctx, ctp)).To(Succeed())
	return ctp, key, func() { deleteIgnoringNotFound(ctx, ctp) }
}

func setupInstanceIDDriftPullRequest(ctx context.Context) (utils.PromoterResource, types.NamespacedName, func()) {
	name := instanceIDDriftResourceName("PullRequest")
	key := types.NamespacedName{Name: name, Namespace: "default"}
	pr := &promoterv1alpha1.PullRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: promoterv1alpha1.PullRequestSpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{Name: name + "-repo"},
			Title:               "instance ID drift test",
			TargetBranch:        testBranchDevelopment,
			SourceBranch:        testBranchDevelopmentNext,
			MergeSha:            testValidCommitSHA,
			State:               "open",
		},
	}
	Expect(k8sClient.Create(ctx, pr)).To(Succeed())
	return pr, key, func() { deleteIgnoringNotFound(ctx, pr) }
}

func setupInstanceIDDriftCommitStatus(ctx context.Context) (utils.PromoterResource, types.NamespacedName, func()) {
	name := instanceIDDriftResourceName("CommitStatus")
	key := types.NamespacedName{Name: name, Namespace: "default"}
	cs := &promoterv1alpha1.CommitStatus{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: promoterv1alpha1.CommitStatusSpec{
			Phase:               promoterv1alpha1.CommitPhasePending,
			RepositoryReference: promoterv1alpha1.ObjectReference{Name: name + "-repo"},
			Sha:                 testValidCommitSHA,
			Name:                "instance-id-drift",
			Description:         "instance ID drift test",
		},
	}
	Expect(k8sClient.Create(ctx, cs)).To(Succeed())
	return cs, key, func() { deleteIgnoringNotFound(ctx, cs) }
}

func setupInstanceIDDriftTimedCommitStatus(ctx context.Context) (utils.PromoterResource, types.NamespacedName, func()) {
	name := instanceIDDriftResourceName("TimedCommitStatus")
	key := types.NamespacedName{Name: name, Namespace: "default"}
	tcs := &promoterv1alpha1.TimedCommitStatus{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: promoterv1alpha1.TimedCommitStatusSpec{
			PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name + "-ps"},
			Environments: []promoterv1alpha1.TimedCommitStatusEnvironments{
				{
					Branch:   testBranchDevelopment,
					Duration: metav1.Duration{Duration: time.Hour},
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, tcs)).To(Succeed())
	return tcs, key, func() { deleteIgnoringNotFound(ctx, tcs) }
}

func setupInstanceIDDriftScheduledCommitStatus(ctx context.Context) (utils.PromoterResource, types.NamespacedName, func()) {
	name := instanceIDDriftResourceName("ScheduledCommitStatus")
	key := types.NamespacedName{Name: name, Namespace: "default"}
	scs := &promoterv1alpha1.ScheduledCommitStatus{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: promoterv1alpha1.ScheduledCommitStatusSpec{
			PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name + "-ps"},
			Key:                  "instance-id-drift",
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
	Expect(k8sClient.Create(ctx, scs)).To(Succeed())
	return scs, key, func() { deleteIgnoringNotFound(ctx, scs) }
}

func setupInstanceIDDriftGitCommitStatus(ctx context.Context) (utils.PromoterResource, types.NamespacedName, func()) {
	name := instanceIDDriftResourceName("GitCommitStatus")
	key := types.NamespacedName{Name: name, Namespace: "default"}
	gcs := &promoterv1alpha1.GitCommitStatus{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: promoterv1alpha1.GitCommitStatusSpec{
			PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name + "-ps"},
			Key:                  "instance-id-drift",
			Expression:           `true`,
		},
	}
	Expect(k8sClient.Create(ctx, gcs)).To(Succeed())
	return gcs, key, func() { deleteIgnoringNotFound(ctx, gcs) }
}

func setupInstanceIDDriftWebRequestCommitStatus(ctx context.Context) (utils.PromoterResource, types.NamespacedName, func()) {
	name := instanceIDDriftResourceName("WebRequestCommitStatus")
	key := types.NamespacedName{Name: name, Namespace: "default"}
	wrcs := &promoterv1alpha1.WebRequestCommitStatus{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
			PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name + "-ps"},
			Key:                  "instance-id-drift",
			HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
				URLTemplate:    "https://example.com/check",
				MethodTemplate: "GET",
			},
			Success: promoterv1alpha1.SuccessSpec{
				When: promoterv1alpha1.WhenWithOutputSpec{
					Expression: "true",
				},
			},
			Mode: promoterv1alpha1.ModeSpec{
				Polling: &promoterv1alpha1.PollingModeSpec{
					Interval: metav1.Duration{Duration: time.Hour},
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, wrcs)).To(Succeed())
	return wrcs, key, func() { deleteIgnoringNotFound(ctx, wrcs) }
}

func setupInstanceIDDriftArgoCDCommitStatus(ctx context.Context) (utils.PromoterResource, types.NamespacedName, func()) {
	name := instanceIDDriftResourceName("ArgoCDCommitStatus")
	key := types.NamespacedName{Name: name, Namespace: "default"}
	acs := &promoterv1alpha1.ArgoCDCommitStatus{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: promoterv1alpha1.ArgoCDCommitStatusSpec{
			PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name + "-ps"},
			ApplicationSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "instance-id-drift"},
			},
		},
	}
	Expect(k8sClient.Create(ctx, acs)).To(Succeed())
	return acs, key, func() { deleteIgnoringNotFound(ctx, acs) }
}
