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
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrlruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	ctrlreq "sigs.k8s.io/controller-runtime/pkg/reconcile"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/webrequest"
	"github.com/argoproj-labs/gitops-promoter/webrequestsimulator"
	wrstypes "github.com/argoproj-labs/gitops-promoter/webrequestsimulator/types"
)

const (
	parityWRCSKey = "parity-key"
	parityNS      = "wrcs-parity-ns"
)

var wrcsStatusParityCmpOpts = cmp.Options{
	cmpopts.IgnoreFields(promoterv1alpha1.WebRequestCommitStatusStatus{}, "ObservedGeneration", "Conditions"),
	cmpopts.IgnoreFields(promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus{}, "LastRequestTime"),
	cmpopts.IgnoreFields(promoterv1alpha1.WebRequestCommitStatusPromotionStrategyContextStatus{}, "LastRequestTime"),
}

func parityJSONMap(m map[string]any) *apiextensionsv1.JSON {
	raw, err := json.Marshal(m)
	Expect(err).NotTo(HaveOccurred())
	return &apiextensionsv1.JSON{Raw: raw}
}

func newWebRequestCommitStatusReconcilerForParity(cl client.Client, scheme *runtime.Scheme) *WebRequestCommitStatusReconciler {
	r := &WebRequestCommitStatusReconciler{
		Client:      cl,
		Scheme:      scheme,
		Recorder:    events.NewFakeRecorder(100),
		SettingsMgr: nil,
		EnqueueCTP:  func(string, string) {},
	}
	r.httpClient = &http.Client{Timeout: 30 * time.Second}
	r.evaluator = webrequest.NewEvaluator()
	return r
}

func twoBranchPromotionStrategyForParity() *promoterv1alpha1.PromotionStrategy {
	shaDev := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaProd := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	return &promoterv1alpha1.PromotionStrategy{
		ObjectMeta: metav1.ObjectMeta{Name: "ps", Namespace: parityNS, UID: types.UID("11111111-1111-1111-1111-111111111111")},
		Spec: promoterv1alpha1.PromotionStrategySpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{Name: "repo"},
			Environments: []promoterv1alpha1.Environment{
				{Branch: "dev", ProposedCommitStatuses: []promoterv1alpha1.CommitStatusSelector{{Key: parityWRCSKey}}},
				{Branch: "prod", ProposedCommitStatuses: []promoterv1alpha1.CommitStatusSelector{{Key: parityWRCSKey}}},
			},
		},
		Status: promoterv1alpha1.PromotionStrategyStatus{
			Environments: []promoterv1alpha1.EnvironmentStatus{
				{
					Branch:   "dev",
					Proposed: promoterv1alpha1.CommitBranchState{Hydrated: promoterv1alpha1.CommitShaState{Sha: shaDev}},
					Active:   promoterv1alpha1.CommitBranchState{Hydrated: promoterv1alpha1.CommitShaState{Sha: "1111111111111111111111111111111111111111"}},
				},
				{
					Branch:   "prod",
					Proposed: promoterv1alpha1.CommitBranchState{Hydrated: promoterv1alpha1.CommitShaState{Sha: shaProd}},
					Active:   promoterv1alpha1.CommitBranchState{Hydrated: promoterv1alpha1.CommitShaState{Sha: "2222222222222222222222222222222222222222"}},
				},
			},
		},
	}
}

var _ = Describe("WebRequestCommitStatus simulator parity", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
		ctrlruntime.SetLogger(zap.New(zap.UseDevMode(true)))
	})

	It("environments: carry-forward when trigger is false matches simulator", func() {
		scheme := runtime.NewScheme()
		Expect(promoterv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		cl := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&promoterv1alpha1.WebRequestCommitStatus{}, &promoterv1alpha1.CommitStatus{}).
			Build()

		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:        parityNS,
				Labels:      map[string]string{"test": "parity"},
				Annotations: map[string]string{"a": "b"},
			},
		}
		ps := twoBranchPromotionStrategyForParity()
		wrcs := &promoterv1alpha1.WebRequestCommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name: "wrcs", Namespace: parityNS, UID: types.UID("22222222-2222-2222-2222-222222222222"), Generation: 1,
			},
			Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "ps"},
				Key:                  parityWRCSKey,
				ReportOn:             "proposed",
				HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
					URLTemplate: "https://example.invalid/{{ .Branch }}",
					Method:      "GET",
				},
				Success: promoterv1alpha1.SuccessSpec{When: promoterv1alpha1.WhenWithOutputSpec{Expression: "true"}},
				Mode: promoterv1alpha1.ModeSpec{Trigger: &promoterv1alpha1.TriggerModeSpec{
					RequeueDuration: metav1.Duration{Duration: 0},
					When:            promoterv1alpha1.WhenWithOutputSpec{Expression: "false"},
				}},
			},
			Status: promoterv1alpha1.WebRequestCommitStatusStatus{
				Environments: []promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus{
					{
						Branch:        "dev",
						ReportedSha:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
						Phase:         promoterv1alpha1.CommitPhasePending,
						TriggerOutput: parityJSONMap(map[string]any{"note": "prev-dev"}),
					},
					{
						Branch:        "prod",
						ReportedSha:   "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
						Phase:         promoterv1alpha1.CommitPhaseSuccess,
						TriggerOutput: parityJSONMap(map[string]any{"note": "prev-prod"}),
					},
				},
			},
		}

		for _, o := range []client.Object{ns, ps, wrcs} {
			Expect(cl.Create(ctx, o)).To(Succeed())
		}

		wrcsInput := wrcs.DeepCopy()
		r := newWebRequestCommitStatusReconcilerForParity(cl, scheme)
		_, err := r.Reconcile(ctx, ctrlreq.Request{NamespacedName: client.ObjectKeyFromObject(wrcs)})
		Expect(err).NotTo(HaveOccurred())

		var got promoterv1alpha1.WebRequestCommitStatus
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(wrcs), &got)).To(Succeed())

		simRes, err := webrequestsimulator.Simulate(ctx, wrstypes.Input{
			WebRequestCommitStatus: wrcsInput,
			PromotionStrategy:      ps.DeepCopy(),
			NamespaceMetadata: wrstypes.NamespaceMetadata{
				Labels:      ns.Labels,
				Annotations: ns.Annotations,
			},
		})
		Expect(err).NotTo(HaveOccurred())

		diff := cmp.Diff(got.Status, simRes.Status, wrcsStatusParityCmpOpts...)
		Expect(diff).To(BeEmpty(), "WebRequestCommitStatus.Status differs from simulator (-kube +simulator):\n%s", diff)
	})

	It("environments: polling HTTP 200 matches simulator", func() {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		}))
		DeferCleanup(srv.Close)

		scheme := runtime.NewScheme()
		Expect(promoterv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		cl := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&promoterv1alpha1.WebRequestCommitStatus{}, &promoterv1alpha1.CommitStatus{}).
			Build()

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: parityNS}}
		ps := twoBranchPromotionStrategyForParity()
		wrcs := &promoterv1alpha1.WebRequestCommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name: "wrcs-poll", Namespace: parityNS, UID: types.UID("33333333-3333-3333-3333-333333333333"), Generation: 1,
			},
			Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "ps"},
				Key:                  parityWRCSKey,
				ReportOn:             "proposed",
				HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
					URLTemplate: srv.URL + "/{{ .Branch }}",
					Method:      "GET",
				},
				Success: promoterv1alpha1.SuccessSpec{When: promoterv1alpha1.WhenWithOutputSpec{Expression: "Response.StatusCode == 200"}},
				Mode:    promoterv1alpha1.ModeSpec{Polling: &promoterv1alpha1.PollingModeSpec{Interval: metav1.Duration{Duration: 0}}},
			},
		}

		for _, o := range []client.Object{ns, ps, wrcs} {
			Expect(cl.Create(ctx, o)).To(Succeed())
		}

		wrcsInput := wrcs.DeepCopy()
		r := newWebRequestCommitStatusReconcilerForParity(cl, scheme)
		_, err := r.Reconcile(ctx, ctrlreq.Request{NamespacedName: client.ObjectKeyFromObject(wrcs)})
		Expect(err).NotTo(HaveOccurred())

		var got promoterv1alpha1.WebRequestCommitStatus
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(wrcs), &got)).To(Succeed())

		simRes, err := webrequestsimulator.Simulate(ctx, wrstypes.Input{
			WebRequestCommitStatus: wrcsInput,
			PromotionStrategy:      ps.DeepCopy(),
			HTTPResponse:           &wrstypes.HTTPResponse{StatusCode: http.StatusOK, Body: map[string]any{"ok": true}},
		})
		Expect(err).NotTo(HaveOccurred())

		diff := cmp.Diff(got.Status, simRes.Status, wrcsStatusParityCmpOpts...)
		Expect(diff).To(BeEmpty(), "WebRequestCommitStatus.Status differs from simulator (-kube +simulator):\n%s", diff)
	})
})
