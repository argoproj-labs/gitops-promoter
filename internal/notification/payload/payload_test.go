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

package payload_test

import (
	"context"
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/notification/events"
	"github.com/argoproj-labs/gitops-promoter/internal/notification/payload"
)

// Known-answer test vectors. These are the contract between the signer here and the
// demo receiver / tester: given the exact secret and body, ComputeSignature MUST
// produce the listed header value. They are generated independently with the Go
// crypto/hmac primitives and pinned here so a regression (algorithm, encoding, or
// prefix change) is caught immediately.
//
// Receivers verify by recomputing HMAC-SHA256(secret, rawBody), hex-encoding, prefixing
// "sha256=", and comparing in constant time against the X-Promoter-Signature header.
const (
	vectorSecret = "promoter-signing-key"

	// Shared fixtures reused across the signing test cases.
	testWebhookURL    = "https://example.com/hook"
	testSigningSecret = "sign-secret"
	testSigningKey    = "hmac"

	// Vector 1: a representative JSON body.
	vector1Body = `{"eventID":"abc123","environment":"production"}`
	vector1Sig  = "sha256=5369094e61c0bd12d007eae09d58fa2eec3bea34b2e3421720c53e61a434aa8b"

	// Vector 2: empty body (signature is still defined and stable).
	vector2Body = ``
	vector2Sig  = "sha256=2b6a135ed2bc0da316d422d07aeb4512fcb2c5a2559b2ac8605c278511ca7050"

	// Vector 3: a short non-JSON body under a different secret.
	vector3Secret = "topsecret"
	vector3Body   = `hello world`
	vector3Sig    = "sha256=67a6479f7b6000f050577eea8b6b5e71d3c704e73a5f5d2aa09f607fce35cf1a"
)

func newScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	Expect(promoterv1alpha1.AddToScheme(scheme)).To(Succeed())
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	return scheme
}

func sampleEvent() events.Event {
	return events.Event{
		Type: events.TypePromotionComplete,
		Object: events.ObjectRef{
			APIVersion: "promoter.argoproj.io/v1alpha1",
			Kind:       "ChangeTransferPolicy",
			Namespace:  "team-a",
			Name:       "checkout-prod",
			UID:        "uid-123",
		},
		Labels:      map[string]string{"app": "checkout", "team": "payments"},
		Environment: "production",
		PreviousSha: "aaaaaaaaaaaa",
		NewSha:      "bbbbbbbbbbbb",
		ActiveURL:   "https://example.com/active",
		OccurredAt:  time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
	}
}

var _ = Describe("ComputeSignature / VerifySignature (HMAC-SHA256 test vectors)", func() {
	It("matches the pinned vector for a JSON body", func() {
		Expect(payload.ComputeSignature([]byte(vectorSecret), []byte(vector1Body))).To(Equal(vector1Sig))
		Expect(payload.VerifySignature([]byte(vectorSecret), []byte(vector1Body), vector1Sig)).To(BeTrue())
	})

	It("matches the pinned vector for an empty body", func() {
		Expect(payload.ComputeSignature([]byte(vectorSecret), []byte(vector2Body))).To(Equal(vector2Sig))
		Expect(payload.VerifySignature([]byte(vectorSecret), []byte(vector2Body), vector2Sig)).To(BeTrue())
	})

	It("matches the pinned vector for a plain body under a different secret", func() {
		Expect(payload.ComputeSignature([]byte(vector3Secret), []byte(vector3Body))).To(Equal(vector3Sig))
		Expect(payload.VerifySignature([]byte(vector3Secret), []byte(vector3Body), vector3Sig)).To(BeTrue())
	})

	It("rejects a tampered body (verification fails)", func() {
		tampered := []byte(vector1Body + " ")
		Expect(payload.VerifySignature([]byte(vectorSecret), tampered, vector1Sig)).To(BeFalse())
	})

	It("rejects a wrong secret", func() {
		Expect(payload.VerifySignature([]byte("wrong-key"), []byte(vector1Body), vector1Sig)).To(BeFalse())
	})

	It("uses the sha256= prefix and lowercase hex", func() {
		sig := payload.ComputeSignature([]byte(vectorSecret), []byte(vector1Body))
		Expect(sig).To(HavePrefix("sha256="))
		Expect(sig).To(MatchRegexp(`^sha256=[0-9a-f]{64}$`))
	})
})

var _ = Describe("Render", func() {
	It("renders a Go template against the event fields", func() {
		n := &promoterv1alpha1.PromoterNotification{
			Spec: promoterv1alpha1.PromoterNotificationSpec{
				PayloadTemplate: `{"env":"{{ .Environment }}","sha":"{{ .NewSha }}","id":"{{ .EventID }}","app":"{{ .Labels.app }}"}`,
			},
		}
		ev := sampleEvent()
		body, err := payload.Render(n, ev)
		Expect(err).NotTo(HaveOccurred())

		var got map[string]string
		Expect(json.Unmarshal(body, &got)).To(Succeed())
		Expect(got["env"]).To(Equal("production"))
		Expect(got["sha"]).To(Equal("bbbbbbbbbbbb"))
		Expect(got["app"]).To(Equal("checkout"))
		Expect(got["id"]).To(Equal(ev.EventID()))
	})

	It("tolerates references to absent map keys via missingkey=zero", func() {
		n := &promoterv1alpha1.PromoterNotification{
			Spec: promoterv1alpha1.PromoterNotificationSpec{
				PayloadTemplate: `gate={{ .Labels.does_not_exist }}`,
			},
		}
		body, err := payload.Render(n, sampleEvent())
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("gate="))
	})

	It("supports sanitized sprig functions", func() {
		n := &promoterv1alpha1.PromoterNotification{
			Spec: promoterv1alpha1.PromoterNotificationSpec{
				PayloadTemplate: `{{ .Environment | upper }}`,
			},
		}
		body, err := payload.Render(n, sampleEvent())
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("PRODUCTION"))
	})

	It("falls back to default JSON serialization when template is empty", func() {
		n := &promoterv1alpha1.PromoterNotification{}
		ev := sampleEvent()
		body, err := payload.Render(n, ev)
		Expect(err).NotTo(HaveOccurred())

		var got map[string]any
		Expect(json.Unmarshal(body, &got)).To(Succeed())
		Expect(got["environment"]).To(Equal("production"))
		Expect(got["eventID"]).To(Equal(ev.EventID()))
		Expect(got["type"]).To(Equal(string(events.TypePromotionComplete)))
	})

	It("returns an error for an unparseable template (does not panic)", func() {
		n := &promoterv1alpha1.PromoterNotification{
			Spec: promoterv1alpha1.PromoterNotificationSpec{
				PayloadTemplate: `{{ .Environment `, // unterminated action
			},
		}
		_, err := payload.Render(n, sampleEvent())
		Expect(err).To(HaveOccurred())
	})

	It("returns an error for an exec failure (calling a method that does not exist)", func() {
		n := &promoterv1alpha1.PromoterNotification{
			Spec: promoterv1alpha1.PromoterNotificationSpec{
				PayloadTemplate: `{{ .NoSuchField }}`,
			},
		}
		_, err := payload.Render(n, sampleEvent())
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("Render with spec.variables (expr)", func() {
	It("evaluates variables and exposes them to the template as .Vars", func() {
		n := &promoterv1alpha1.PromoterNotification{
			Spec: promoterv1alpha1.PromoterNotificationSpec{
				Variables: map[string]string{
					"severity": `event.type == "GateFailed" ? "critical" : "info"`,
					"shortSha": `event.newSha[:7]`,
				},
				PayloadTemplate: `{"sev":"{{ .Vars.severity }}","short":"{{ .Vars.shortSha }}"}`,
			},
		}
		body, err := payload.Render(n, sampleEvent())
		Expect(err).NotTo(HaveOccurred())

		var got map[string]string
		Expect(json.Unmarshal(body, &got)).To(Succeed())
		// sampleEvent is a PromotionComplete, so severity is the "info" branch.
		Expect(got["sev"]).To(Equal("info"))
		Expect(got["short"]).To(Equal("bbbbbbb")) // first 7 of "bbbbbbbbbbbb"
	})

	It("evaluates the conditional branch based on the event", func() {
		n := &promoterv1alpha1.PromoterNotification{
			Spec: promoterv1alpha1.PromoterNotificationSpec{
				Variables:       map[string]string{"severity": `event.type == "GateFailed" ? "critical" : "info"`},
				PayloadTemplate: `{{ .Vars.severity }}`,
			},
		}
		ev := sampleEvent()
		ev.Type = events.TypeGateFailed
		body, err := payload.Render(n, ev)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("critical"))
	})

	It("exposes variables as a vars object in the default JSON payload (no template)", func() {
		n := &promoterv1alpha1.PromoterNotification{
			Spec: promoterv1alpha1.PromoterNotificationSpec{
				Variables: map[string]string{"shortSha": `event.newSha[:7]`},
			},
		}
		body, err := payload.Render(n, sampleEvent())
		Expect(err).NotTo(HaveOccurred())

		var got map[string]any
		Expect(json.Unmarshal(body, &got)).To(Succeed())
		vars, ok := got["vars"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(vars["shortSha"]).To(Equal("bbbbbbb"))
	})

	It("returns a configuration error for an expression that fails to compile (no panic)", func() {
		n := &promoterv1alpha1.PromoterNotification{
			Spec: promoterv1alpha1.PromoterNotificationSpec{
				Variables:       map[string]string{"bad": `event.type ==`}, // syntactically invalid
				PayloadTemplate: `{{ .Vars.bad }}`,
			},
		}
		_, err := payload.Render(n, sampleEvent())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`variable "bad"`))
	})

	It("returns a configuration error for a type-mismatched expression (no panic)", func() {
		n := &promoterv1alpha1.PromoterNotification{
			Spec: promoterv1alpha1.PromoterNotificationSpec{
				// Comparing a string field to an int is a compile-time type error in expr.
				Variables:       map[string]string{"oops": `event.environment == 42`},
				PayloadTemplate: `{{ .Vars.oops }}`,
			},
		}
		_, err := payload.Render(n, sampleEvent())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(`variable "oops"`))
	})

	It("can reference event labels and the eventID in expressions", func() {
		n := &promoterv1alpha1.PromoterNotification{
			Spec: promoterv1alpha1.PromoterNotificationSpec{
				Variables: map[string]string{
					"app": `event.labels["app"]`,
					"id":  `eventID`,
				},
				PayloadTemplate: `{"app":"{{ .Vars.app }}","id":"{{ .Vars.id }}"}`,
			},
		}
		ev := sampleEvent()
		body, err := payload.Render(n, ev)
		Expect(err).NotTo(HaveOccurred())
		var got map[string]string
		Expect(json.Unmarshal(body, &got)).To(Succeed())
		Expect(got["app"]).To(Equal("checkout"))
		Expect(got["id"]).To(Equal(ev.EventID()))
	})

	It("is a no-op when no variables are configured (.Vars is nil)", func() {
		n := &promoterv1alpha1.PromoterNotification{
			Spec: promoterv1alpha1.PromoterNotificationSpec{
				PayloadTemplate: `{{ if .Vars }}has-vars{{ else }}no-vars{{ end }}`,
			},
		}
		body, err := payload.Render(n, sampleEvent())
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("no-vars"))
	})
})

var _ = Describe("Sign / BuildRequest", func() {
	const ns = "team-a"

	newSignedNotification := func(secretName, key string) *promoterv1alpha1.PromoterNotification {
		return &promoterv1alpha1.PromoterNotification{
			ObjectMeta: metav1.ObjectMeta{Name: "n", Namespace: ns},
			Spec: promoterv1alpha1.PromoterNotificationSpec{
				PayloadTemplate: vector1Body,
				Delivery: promoterv1alpha1.NotificationDelivery{
					Webhook: &promoterv1alpha1.WebhookDelivery{
						URL: testWebhookURL,
						Signing: &promoterv1alpha1.WebhookSigning{
							SecretRef: promoterv1alpha1.SecretKeyReference{Name: secretName, Key: key},
						},
					},
				},
			},
		}
	}

	It("signs the body using a secret read via the client", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: testSigningSecret, Namespace: ns},
			Data:       map[string][]byte{testSigningKey: []byte(vectorSecret)},
		}
		cl := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(secret).Build()

		name, value, err := payload.Sign(context.Background(), cl, ns,
			&promoterv1alpha1.WebhookSigning{SecretRef: promoterv1alpha1.SecretKeyReference{Name: testSigningSecret, Key: testSigningKey}},
			[]byte(vector1Body))
		Expect(err).NotTo(HaveOccurred())
		Expect(name).To(Equal(payload.SignatureHeader))
		Expect(value).To(Equal(vector1Sig))
	})

	It("errors when the secret is missing", func() {
		cl := fake.NewClientBuilder().WithScheme(newScheme()).Build()
		_, _, err := payload.Sign(context.Background(), cl, ns,
			&promoterv1alpha1.WebhookSigning{SecretRef: promoterv1alpha1.SecretKeyReference{Name: "absent", Key: testSigningKey}},
			[]byte(vector1Body))
		Expect(err).To(HaveOccurred())
	})

	It("errors when the key is missing from the secret", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: testSigningSecret, Namespace: ns},
			Data:       map[string][]byte{"other": []byte(vectorSecret)},
		}
		cl := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(secret).Build()
		_, _, err := payload.Sign(context.Background(), cl, ns,
			&promoterv1alpha1.WebhookSigning{SecretRef: promoterv1alpha1.SecretKeyReference{Name: testSigningSecret, Key: testSigningKey}},
			[]byte(vector1Body))
		Expect(err).To(HaveOccurred())
	})

	It("BuildRequest renders and attaches the signature header when signing is set", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: testSigningSecret, Namespace: ns},
			Data:       map[string][]byte{testSigningKey: []byte(vectorSecret)},
		}
		cl := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(secret).Build()
		n := newSignedNotification(testSigningSecret, testSigningKey)

		body, headers, err := payload.BuildRequest(context.Background(), cl, n, sampleEvent())
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal(vector1Body))
		Expect(headers).To(HaveKeyWithValue(payload.SignatureHeader, vector1Sig))
		// And the receiver can verify it end-to-end.
		Expect(payload.VerifySignature([]byte(vectorSecret), body, headers[payload.SignatureHeader])).To(BeTrue())
	})

	It("BuildRequest returns an empty header map when signing is not configured", func() {
		cl := fake.NewClientBuilder().WithScheme(newScheme()).Build()
		n := &promoterv1alpha1.PromoterNotification{
			ObjectMeta: metav1.ObjectMeta{Name: "n", Namespace: ns},
			Spec: promoterv1alpha1.PromoterNotificationSpec{
				PayloadTemplate: vector1Body,
				Delivery: promoterv1alpha1.NotificationDelivery{
					Webhook: &promoterv1alpha1.WebhookDelivery{URL: testWebhookURL},
				},
			},
		}
		body, headers, err := payload.BuildRequest(context.Background(), cl, n, sampleEvent())
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal(vector1Body))
		Expect(headers).To(BeEmpty())
	})

	It("BuildRequest surfaces render errors without performing I/O", func() {
		cl := fake.NewClientBuilder().WithScheme(newScheme()).Build()
		n := &promoterv1alpha1.PromoterNotification{
			ObjectMeta: metav1.ObjectMeta{Name: "n", Namespace: ns},
			Spec: promoterv1alpha1.PromoterNotificationSpec{
				PayloadTemplate: `{{ .Environment `,
				Delivery: promoterv1alpha1.NotificationDelivery{
					Webhook: &promoterv1alpha1.WebhookDelivery{URL: testWebhookURL},
				},
			},
		}
		_, _, err := payload.BuildRequest(context.Background(), cl, n, sampleEvent())
		Expect(err).To(HaveOccurred())
	})
})
