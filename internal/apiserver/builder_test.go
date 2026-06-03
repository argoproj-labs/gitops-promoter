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

package apiserver

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

const (
	testNamespace = "test-ns"
	testPSName    = "my-ps"
	testSecretVal = "SUPER-SECRET-TOKEN"
)

func objectMeta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: testNamespace}
}

func psLabeledMeta(name string) metav1.ObjectMeta {
	m := objectMeta(name)
	m.Labels = map[string]string{promoterv1alpha1.PromotionStrategyLabel: testPSName}
	return m
}

// newFakeReader builds a fake controller-runtime read client over the promoter scheme.
func newFakeReader(objs ...client.Object) client.Reader {
	return fake.NewClientBuilder().WithScheme(utils.GetScheme()).WithObjects(objs...).Build()
}

// seedObjects returns a representative PromotionStrategy and all of its related
// resources, including a Secret that must NEVER end up in the bundle.
func seedObjects() []client.Object {
	ps := &promoterv1alpha1.PromotionStrategy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testPSName,
			Namespace: testNamespace,
			ManagedFields: []metav1.ManagedFieldsEntry{
				{Manager: "controller", Operation: metav1.ManagedFieldsOperationUpdate, APIVersion: "promoter.argoproj.io/v1alpha1", FieldsType: "FieldsV1"},
			},
			Annotations: map[string]string{
				lastAppliedAnnotation: "{\"should\":\"be stripped\"}",
				"keep-me":             "yes",
			},
		},
		Spec: promoterv1alpha1.PromotionStrategySpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{Name: "my-repo"},
			Environments:        []promoterv1alpha1.Environment{{Branch: "environment/dev"}},
		},
		Status: promoterv1alpha1.PromotionStrategyStatus{
			Environments: []promoterv1alpha1.EnvironmentStatus{
				{
					Branch:   "environment/dev",
					Active:   promoterv1alpha1.CommitBranchState{Dry: promoterv1alpha1.CommitShaState{Sha: "aaaaaaa"}},
					Proposed: promoterv1alpha1.CommitBranchState{Dry: promoterv1alpha1.CommitShaState{Sha: "aaaaaaa"}},
				},
			},
		},
	}

	return []client.Object{
		ps,
		&promoterv1alpha1.ChangeTransferPolicy{
			ObjectMeta: psLabeledMeta("dev-ctp"),
			Spec:       promoterv1alpha1.ChangeTransferPolicySpec{ActiveBranch: "environment/dev"},
		},
		&promoterv1alpha1.PullRequest{ObjectMeta: psLabeledMeta("dev-pr")},
		&promoterv1alpha1.CommitStatus{ObjectMeta: psLabeledMeta("dev-cs")},
		// Managers that reference the PS by spec.promotionStrategyRef.name.
		&promoterv1alpha1.ArgoCDCommitStatus{
			ObjectMeta: objectMeta("argo-1"),
			Spec:       promoterv1alpha1.ArgoCDCommitStatusSpec{PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: testPSName}},
		},
		// A manager referencing a DIFFERENT PS; must be excluded.
		&promoterv1alpha1.ArgoCDCommitStatus{
			ObjectMeta: objectMeta("argo-other"),
			Spec:       promoterv1alpha1.ArgoCDCommitStatusSpec{PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "other-ps"}},
		},
		&promoterv1alpha1.GitCommitStatus{
			ObjectMeta: objectMeta("git-1"),
			Spec:       promoterv1alpha1.GitCommitStatusSpec{PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: testPSName}},
		},
		&promoterv1alpha1.TimedCommitStatus{
			ObjectMeta: objectMeta("timed-1"),
			Spec:       promoterv1alpha1.TimedCommitStatusSpec{PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: testPSName}},
		},
		&promoterv1alpha1.WebRequestCommitStatus{
			ObjectMeta: objectMeta("web-1"),
			Spec:       promoterv1alpha1.WebRequestCommitStatusSpec{PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: testPSName}},
		},
		&promoterv1alpha1.GitRepository{
			ObjectMeta: objectMeta("my-repo"),
			Spec: promoterv1alpha1.GitRepositorySpec{
				ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{Kind: "ScmProvider", Name: "my-scm"},
			},
		},
		&promoterv1alpha1.ScmProvider{
			ObjectMeta: objectMeta("my-scm"),
			Spec: promoterv1alpha1.ScmProviderSpec{
				SecretRef: &corev1.LocalObjectReference{Name: "my-secret"},
			},
		},
		&corev1.Secret{
			ObjectMeta: objectMeta("my-secret"),
			Data:       map[string][]byte{"token": []byte(testSecretVal)},
		},
	}
}

var _ = Describe("BuildBundle", func() {
	It("assembles a bundle joining all related resources without any Secret", func() {
		reader := newFakeReader(seedObjects()...)

		bundle, err := buildBundle(context.Background(), reader, testNamespace, testPSName, "42")
		Expect(err).NotTo(HaveOccurred())
		Expect(bundle).NotTo(BeNil())

		Expect(bundle.Name).To(Equal(testPSName))
		Expect(bundle.Namespace).To(Equal(testNamespace))
		Expect(bundle.ResourceVersion).To(Equal("42"))
		Expect(bundle.PromotionStrategy.Name).To(Equal(testPSName))

		By("selecting label-owned children")
		Expect(bundle.ChangeTransferPolicies).To(HaveLen(1))
		Expect(bundle.PullRequests).To(HaveLen(1))
		Expect(bundle.CommitStatuses).To(HaveLen(1))

		By("filtering managers by promotionStrategyRef.name")
		Expect(bundle.ArgoCDCommitStatuses).To(HaveLen(1))
		Expect(bundle.ArgoCDCommitStatuses[0].Name).To(Equal("argo-1"))
		Expect(bundle.GitCommitStatuses).To(HaveLen(1))
		Expect(bundle.TimedCommitStatuses).To(HaveLen(1))
		Expect(bundle.WebRequestCommitStatuses).To(HaveLen(1))

		By("resolving git config but never the Secret")
		Expect(bundle.GitRepository).NotTo(BeNil())
		Expect(bundle.GitRepository.Name).To(Equal("my-repo"))
		Expect(bundle.ScmProvider).NotTo(BeNil())
		Expect(bundle.ScmProvider.Spec.SecretRef).NotTo(BeNil())
		Expect(bundle.ScmProvider.Spec.SecretRef.Name).To(Equal("my-secret"))
		Expect(bundle.ClusterScmProvider).To(BeNil())

		By("stripping managedFields and the last-applied annotation")
		Expect(bundle.PromotionStrategy.ManagedFields).To(BeNil())
		Expect(bundle.PromotionStrategy.Annotations).NotTo(HaveKey(lastAppliedAnnotation))
		Expect(bundle.PromotionStrategy.Annotations).To(HaveKeyWithValue("keep-me", "yes"))

		By("dropping the PromotionStrategy status (reconstructed from CTPs by consumers)")
		Expect(bundle.PromotionStrategy.Status.Environments).To(BeEmpty())

		By("never leaking the Secret value into the serialized bundle")
		// Marshal the served view type; any leaked secret value would appear in the JSON.
		raw, err := json.Marshal(bundle)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring(testSecretVal))
	})

	It("returns NotFound when the PromotionStrategy is missing", func() {
		reader := newFakeReader()

		_, err := buildBundle(context.Background(), reader, testNamespace, "missing", "1")
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("resolves a ClusterScmProvider when the GitRepository references one", func() {
		objs := []client.Object{
			&promoterv1alpha1.PromotionStrategy{
				ObjectMeta: metav1.ObjectMeta{Name: testPSName, Namespace: testNamespace},
				Spec: promoterv1alpha1.PromotionStrategySpec{
					RepositoryReference: promoterv1alpha1.ObjectReference{Name: "my-repo"},
					Environments:        []promoterv1alpha1.Environment{{Branch: "environment/dev"}},
				},
			},
			&promoterv1alpha1.GitRepository{
				ObjectMeta: objectMeta("my-repo"),
				Spec: promoterv1alpha1.GitRepositorySpec{
					ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{Kind: "ClusterScmProvider", Name: "cluster-scm"},
				},
			},
			&promoterv1alpha1.ClusterScmProvider{ObjectMeta: metav1.ObjectMeta{Name: "cluster-scm"}},
		}
		reader := newFakeReader(objs...)

		bundle, err := buildBundle(context.Background(), reader, testNamespace, testPSName, "1")
		Expect(err).NotTo(HaveOccurred())
		Expect(bundle.ScmProvider).To(BeNil())
		Expect(bundle.ClusterScmProvider).NotTo(BeNil())
		Expect(bundle.ClusterScmProvider.Name).To(Equal("cluster-scm"))
	})
})
