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

package notification

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/notification/events"
	"github.com/argoproj-labs/gitops-promoter/internal/notification/payload"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// testTeamLabelKey is the label key paired with testTeamLabelValue on event/selector labels.
// The value lives in the shared testconsts_test.go; the key is local to this suite.
const testTeamLabelKey = "team"

// capturedRequest is a single request the test receiver saw, recorded for assertions.
type capturedRequest struct {
	method  string
	headers http.Header
	body    []byte
}

// recordingReceiver is an httptest.Server wrapper that records every request and lets a test
// control the HTTP status returned per request (so retry/dead-letter paths can be exercised).
type recordingReceiver struct {
	server *httptest.Server
	// statusFor returns the HTTP status to respond with, given the 1-based request index.
	statusFor func(n int) int
	requests  []capturedRequest
	mu        sync.Mutex
}

func newRecordingReceiver(statusFor func(n int) int) *recordingReceiver {
	rr := &recordingReceiver{statusFor: statusFor}
	rr.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rr.mu.Lock()
		rr.requests = append(rr.requests, capturedRequest{
			method:  r.Method,
			headers: r.Header.Clone(),
			body:    body,
		})
		n := len(rr.requests)
		rr.mu.Unlock()
		w.WriteHeader(rr.statusFor(n))
	}))
	return rr
}

func (rr *recordingReceiver) count() int {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return len(rr.requests)
}

func (rr *recordingReceiver) last() capturedRequest {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return rr.requests[len(rr.requests)-1]
}

func (rr *recordingReceiver) close() { rr.server.Close() }

// alwaysOK is a statusFor helper that always returns 200.
func alwaysOK(int) int { return http.StatusOK }

// e2eEvent builds an event in namespace ns with the given labels. It is the same shape the
// CTP reconciler publishes for a CTPActive transition (see publishShaTransitions).
func e2eEvent(ns string, labels map[string]string) events.Event {
	return events.Event{
		Type: events.TypeCTPActive,
		Object: events.ObjectRef{
			APIVersion: testAPIVersion,
			Kind:       testKindCTP,
			Namespace:  ns,
			Name:       "ctp-e2e",
			UID:        "uid-e2e",
		},
		Labels:      labels,
		Environment: "environment/production",
		PreviousSha: "0000000000000000000000000000000000000000",
		NewSha:      "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		OccurredAt:  time.Unix(1700000100, 0).UTC(),
	}
}

// makeNamespace creates a fresh namespace for a spec so its PromoterNotification list is
// isolated from other specs (the controller lists notifications namespace-wide).
func makeNamespace(prefix string) string {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: prefix}}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	return ns.Name
}

// applyNotification builds + creates a PromoterNotification subscribing to CTPActive that
// delivers to url. Optional mutators tweak the spec (selector, signing, retry, etc.).
func applyNotification(ns, name, url string, mutators ...func(*promoterv1alpha1.PromoterNotification)) *promoterv1alpha1.PromoterNotification {
	pn := &promoterv1alpha1.PromoterNotification{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: promoterv1alpha1.PromoterNotificationSpec{
			EventTypes: []promoterv1alpha1.NotificationEventType{promoterv1alpha1.NotificationEventCTPActive},
			Delivery: promoterv1alpha1.NotificationDelivery{
				Webhook: &promoterv1alpha1.WebhookDelivery{
					URL:            url,
					TimeoutSeconds: 5,
					Retry:          &promoterv1alpha1.WebhookRetry{MaxAttempts: 1, Backoff: promoterv1alpha1.BackoffFixed},
				},
			},
		},
	}
	for _, m := range mutators {
		m(pn)
	}
	Expect(k8sClient.Create(ctx, pn)).To(Succeed())
	return pn
}

// statusOf reloads the PromoterNotification and returns its status.
func statusOf(pn *promoterv1alpha1.PromoterNotification) promoterv1alpha1.PromoterNotificationStatus {
	var got promoterv1alpha1.PromoterNotification
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pn), &got)).To(Succeed())
	return got.Status
}

var _ = Describe("Notification framework end-to-end (envtest)", func() {
	const eventuallyTimeout = 20 * time.Second
	const consistentlyDuration = 4 * time.Second
	const pollInterval = 200 * time.Millisecond

	Describe("happy path", func() {
		It("delivers a matching event to the webhook with expected body, headers, and a valid signature, and records status", func() {
			ns := makeNamespace("notif-happy-")

			// Receiver always 200.
			rr := newRecordingReceiver(alwaysOK)
			DeferCleanup(rr.close)

			// Signing secret in the notification's namespace.
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: testSigningSecret, Namespace: ns},
				Data:       map[string][]byte{testSigningKey: []byte("e2e-top-secret")},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			pn := applyNotification(ns, "notif-happy", rr.server.URL, func(n *promoterv1alpha1.PromoterNotification) {
				n.Spec.Delivery.Webhook.Headers = map[string]string{testCustomHeaderName: testCustomHeaderVal}
				n.Spec.Delivery.Webhook.Signing = &promoterv1alpha1.WebhookSigning{
					SecretRef: promoterv1alpha1.SecretKeyReference{Name: testSigningSecret, Key: testSigningKey},
				}
			})

			// Wait for the CR to be visible to the controller's cache before publishing, so the
			// list inside handleEvent sees it.
			Eventually(func() error {
				return k8sClient.Get(ctx, client.ObjectKeyFromObject(pn), &promoterv1alpha1.PromoterNotification{})
			}, eventuallyTimeout, pollInterval).Should(Succeed())

			ev := e2eEvent(ns, map[string]string{testTeamLabelKey: testTeamLabelValue})
			Expect(broker.Publish(ctx, ev)).To(Succeed())

			By("the receiver getting exactly one POST")
			Eventually(rr.count, eventuallyTimeout, pollInterval).Should(Equal(1))

			req := rr.last()
			Expect(req.method).To(Equal(http.MethodPost))
			Expect(req.headers.Get("Content-Type")).To(Equal("application/json"))
			Expect(req.headers.Get(testCustomHeaderName)).To(Equal(testCustomHeaderVal))

			By("the body being the default JSON payload carrying the event identity")
			Expect(req.body).NotTo(BeEmpty())
			Expect(string(req.body)).To(ContainSubstring(ev.EventID()))
			Expect(string(req.body)).To(ContainSubstring("CTPActive"))

			By("the signature verifying against the signing secret over the exact received body")
			sig := req.headers.Get(payload.SignatureHeader)
			Expect(sig).NotTo(BeEmpty())
			Expect(payload.VerifySignature([]byte("e2e-top-secret"), req.body, sig)).To(BeTrue(),
				"X-Promoter-Signature must verify over the exact received body")

			By("the request carrying the X-Promoter-Event-Id dedup header equal to the event's stable ID")
			Expect(req.headers.Get(EventIDHeader)).To(Equal(ev.EventID()),
				"X-Promoter-Event-Id must equal the event's stable EventID for receiver dedup")

			By("status recording a successful delivery")
			Eventually(func(g Gomega) {
				st := statusOf(pn)
				g.Expect(st.TotalDelivered).To(Equal(int64(1)))
				g.Expect(st.TotalFailed).To(Equal(int64(0)))
				g.Expect(st.LastDelivery).NotTo(BeNil())
				g.Expect(st.LastDelivery.EventID).To(Equal(ev.EventID()))
				g.Expect(st.LastDelivery.ResponseStatus).To(Equal(int32(http.StatusOK)))
				cond := meta.FindStatusCondition(st.Conditions, string(promoterConditions.Ready))
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(cond.Reason).To(Equal(string(promoterConditions.DeliveryHealthy)))
			}, eventuallyTimeout, pollInterval).Should(Succeed())
		})
	})

	Describe("negative (a): non-matching label selector delivers nothing", func() {
		It("does not deliver when the event labels do not satisfy the notification selector", func() {
			ns := makeNamespace("notif-selneg-")

			rr := newRecordingReceiver(alwaysOK)
			DeferCleanup(rr.close)

			pn := applyNotification(ns, "notif-selneg", rr.server.URL, func(n *promoterv1alpha1.PromoterNotification) {
				n.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{testTeamLabelKey: testTeamLabelValue}}
			})
			Eventually(func() error {
				return k8sClient.Get(ctx, client.ObjectKeyFromObject(pn), &promoterv1alpha1.PromoterNotification{})
			}, eventuallyTimeout, pollInterval).Should(Succeed())

			// Event labels do NOT satisfy the selector.
			Expect(broker.Publish(ctx, e2eEvent(ns, map[string]string{testTeamLabelKey: "search"}))).To(Succeed())

			By("the receiver getting ZERO deliveries, consistently")
			Consistently(rr.count, consistentlyDuration, pollInterval).Should(Equal(0))

			st := statusOf(pn)
			Expect(st.TotalDelivered).To(Equal(int64(0)))
			Expect(st.TotalFailed).To(Equal(int64(0)))
		})
	})

	Describe("negative (b): zero PromoterNotification CRs is a no-op", func() {
		It("delivers nothing and produces no error when no notifications exist in the namespace", func() {
			ns := makeNamespace("notif-zero-")

			rr := newRecordingReceiver(alwaysOK)
			DeferCleanup(rr.close)

			// No PromoterNotification created in ns.
			Expect(broker.Publish(ctx, e2eEvent(ns, map[string]string{testTeamLabelKey: testTeamLabelValue}))).To(Succeed())

			By("the receiver getting ZERO deliveries, consistently")
			Consistently(rr.count, consistentlyDuration, pollInterval).Should(Equal(0))
		})
	})

	Describe("negative (c): retry then success yields a single logical delivery", func() {
		It("retries after a transient 500 and records exactly one successful delivery", func() {
			ns := makeNamespace("notif-retry-")

			// First request 500, all subsequent 200.
			rr := newRecordingReceiver(func(n int) int {
				if n == 1 {
					return http.StatusInternalServerError
				}
				return http.StatusOK
			})
			DeferCleanup(rr.close)

			pn := applyNotification(ns, "notif-retry", rr.server.URL, func(n *promoterv1alpha1.PromoterNotification) {
				// 2 attempts, Fixed backoff (baseBackoff ~1s) — well under the eventually timeout.
				n.Spec.Delivery.Webhook.Retry = &promoterv1alpha1.WebhookRetry{MaxAttempts: 2, Backoff: promoterv1alpha1.BackoffFixed}
			})
			Eventually(func() error {
				return k8sClient.Get(ctx, client.ObjectKeyFromObject(pn), &promoterv1alpha1.PromoterNotification{})
			}, eventuallyTimeout, pollInterval).Should(Succeed())

			Expect(broker.Publish(ctx, e2eEvent(ns, nil))).To(Succeed())

			By("the receiver getting exactly two requests (500 then 200)")
			Eventually(rr.count, eventuallyTimeout, pollInterval).Should(Equal(2))

			By("status recording a single successful logical delivery (not a failure)")
			Eventually(func(g Gomega) {
				st := statusOf(pn)
				g.Expect(st.TotalDelivered).To(Equal(int64(1)))
				g.Expect(st.TotalFailed).To(Equal(int64(0)))
				cond := meta.FindStatusCondition(st.Conditions, string(promoterConditions.Ready))
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			}, eventuallyTimeout, pollInterval).Should(Succeed())

			By("the delivery not being double-counted")
			Consistently(func() int64 { return statusOf(pn).TotalDelivered }, consistentlyDuration, pollInterval).Should(Equal(int64(1)))
		})
	})

	Describe("negative (d): dead-letter after maxAttempts", func() {
		It("increments TotalFailed and sets DeliveryFailing when the receiver always fails", func() {
			ns := makeNamespace("notif-deadletter-")

			// Receiver always 500.
			rr := newRecordingReceiver(func(int) int { return http.StatusInternalServerError })
			DeferCleanup(rr.close)

			pn := applyNotification(ns, "notif-deadletter", rr.server.URL, func(n *promoterv1alpha1.PromoterNotification) {
				n.Spec.Delivery.Webhook.Retry = &promoterv1alpha1.WebhookRetry{MaxAttempts: 2, Backoff: promoterv1alpha1.BackoffFixed}
			})
			Eventually(func() error {
				return k8sClient.Get(ctx, client.ObjectKeyFromObject(pn), &promoterv1alpha1.PromoterNotification{})
			}, eventuallyTimeout, pollInterval).Should(Succeed())

			ev := e2eEvent(ns, nil)
			Expect(broker.Publish(ctx, ev)).To(Succeed())

			By("the receiver getting exactly maxAttempts requests")
			Eventually(rr.count, eventuallyTimeout, pollInterval).Should(Equal(2))

			By("status recording a dead-lettered failure")
			Eventually(func(g Gomega) {
				st := statusOf(pn)
				g.Expect(st.TotalFailed).To(Equal(int64(1)))
				g.Expect(st.TotalDelivered).To(Equal(int64(0)))
				g.Expect(st.LastDelivery).NotTo(BeNil())
				g.Expect(st.LastDelivery.ResponseStatus).To(Equal(int32(http.StatusInternalServerError)))
				cond := meta.FindStatusCondition(st.Conditions, string(promoterConditions.Ready))
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(string(promoterConditions.DeliveryFailing)))
			}, eventuallyTimeout, pollInterval).Should(Succeed())
		})
	})

	Describe("event-type filtering across a real list", func() {
		It("delivers only to notifications subscribed to the event's type", func() {
			ns := makeNamespace("notif-typefilter-")

			matchRR := newRecordingReceiver(alwaysOK)
			DeferCleanup(matchRR.close)
			otherRR := newRecordingReceiver(alwaysOK)
			DeferCleanup(otherRR.close)

			// Subscribes to CTPActive (matches the published event).
			matchPN := applyNotification(ns, "notif-match", matchRR.server.URL)
			// Subscribes only to PromotionComplete (must NOT match a CTPActive event).
			otherPN := applyNotification(ns, "notif-other", otherRR.server.URL, func(n *promoterv1alpha1.PromoterNotification) {
				n.Spec.EventTypes = []promoterv1alpha1.NotificationEventType{promoterv1alpha1.NotificationEventPromotionComplete}
			})

			Eventually(func() error {
				return k8sClient.Get(ctx, client.ObjectKeyFromObject(otherPN), &promoterv1alpha1.PromoterNotification{})
			}, eventuallyTimeout, pollInterval).Should(Succeed())

			Expect(broker.Publish(ctx, e2eEvent(ns, nil))).To(Succeed())

			By("the subscribed receiver getting the delivery")
			Eventually(matchRR.count, eventuallyTimeout, pollInterval).Should(Equal(1))
			By("the non-subscribed receiver getting nothing, consistently")
			Consistently(otherRR.count, consistentlyDuration, pollInterval).Should(Equal(0))

			_ = matchPN
		})
	})
})
