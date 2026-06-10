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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// mappingSeed provides the objects needed to resolve reverse lookups
// (GitRepository, (Cluster)ScmProvider) during child->PS mapping.
func mappingSeed() []client.Object {
	return []client.Object{
		&promoterv1alpha1.PromotionStrategy{
			ObjectMeta: metav1.ObjectMeta{Name: testPSName, Namespace: testNamespace},
			Spec:       promoterv1alpha1.PromotionStrategySpec{RepositoryReference: promoterv1alpha1.ObjectReference{Name: "my-repo"}},
		},
		&promoterv1alpha1.GitRepository{
			ObjectMeta: objectMeta("my-repo"),
			Spec: promoterv1alpha1.GitRepositorySpec{
				ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{Kind: "ScmProvider", Name: "my-scm"},
			},
		},
		&promoterv1alpha1.GitRepository{
			ObjectMeta: objectMeta("cluster-repo"),
			Spec: promoterv1alpha1.GitRepositorySpec{
				ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{Kind: "ClusterScmProvider", Name: "cluster-scm"},
			},
		},
	}
}

var _ = Describe("mapObjectToPromotionStrategies", func() {
	psKey := types.NamespacedName{Namespace: testNamespace, Name: testPSName}

	DescribeTable("maps a changed child to the owning PromotionStrategy key(s)",
		func(obj client.Object, want []types.NamespacedName) {
			provider := newProviderWithReader(newFakeReader(mappingSeed()...))
			got := provider.mapObjectToPromotionStrategies(context.Background(), obj)
			Expect(got).To(HaveLen(len(want)))
			for _, w := range want {
				Expect(got).To(ContainElement(w))
			}
		},
		Entry("PromotionStrategy identity",
			&promoterv1alpha1.PromotionStrategy{ObjectMeta: metav1.ObjectMeta{Name: testPSName, Namespace: testNamespace}},
			[]types.NamespacedName{psKey}),
		Entry("ChangeTransferPolicy by label",
			&promoterv1alpha1.ChangeTransferPolicy{ObjectMeta: psLabeledMeta("ctp")},
			[]types.NamespacedName{psKey}),
		Entry("CommitStatus by label",
			&promoterv1alpha1.CommitStatus{ObjectMeta: psLabeledMeta("cs")},
			[]types.NamespacedName{psKey}),
		Entry("PullRequest by label",
			&promoterv1alpha1.PullRequest{ObjectMeta: psLabeledMeta("pr")},
			[]types.NamespacedName{psKey}),
		Entry("ArgoCDCommitStatus by ref",
			&promoterv1alpha1.ArgoCDCommitStatus{
				ObjectMeta: objectMeta("argo"),
				Spec:       promoterv1alpha1.ArgoCDCommitStatusSpec{PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: testPSName}},
			},
			[]types.NamespacedName{psKey}),
		Entry("GitCommitStatus by ref",
			&promoterv1alpha1.GitCommitStatus{
				ObjectMeta: objectMeta("git"),
				Spec:       promoterv1alpha1.GitCommitStatusSpec{PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: testPSName}},
			},
			[]types.NamespacedName{psKey}),
		Entry("TimedCommitStatus by ref",
			&promoterv1alpha1.TimedCommitStatus{
				ObjectMeta: objectMeta("timed"),
				Spec:       promoterv1alpha1.TimedCommitStatusSpec{PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: testPSName}},
			},
			[]types.NamespacedName{psKey}),
		Entry("WebRequestCommitStatus by ref",
			&promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: objectMeta("web"),
				Spec:       promoterv1alpha1.WebRequestCommitStatusSpec{PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: testPSName}},
			},
			[]types.NamespacedName{psKey}),
		Entry("GitRepository reverse lookup",
			&promoterv1alpha1.GitRepository{ObjectMeta: objectMeta("my-repo")},
			[]types.NamespacedName{psKey}),
		Entry("ScmProvider reverse lookup",
			&promoterv1alpha1.ScmProvider{ObjectMeta: objectMeta("my-scm")},
			[]types.NamespacedName{psKey}),
		Entry("ClusterScmProvider with no referencing PS",
			&promoterv1alpha1.ClusterScmProvider{ObjectMeta: metav1.ObjectMeta{Name: "cluster-scm"}},
			nil),
	)
})
