package metrics

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

const (
	testNamespace          = "promoter-system"
	testServiceAccountName = "promoter-controller-manager"
)

func stubTokenRequester(token string, expiry time.Time, err error) tokenRequestFunc {
	return func(_ context.Context, _ client.Client, _, _ string, _ int64) (string, time.Time, error) {
		return token, expiry, err
	}
}

func TestEvaluateTokenRenewal(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		secret      *corev1.Secret
		name        string
		wantRefresh bool
	}{
		{
			name:        "wrong secret type",
			secret:      &corev1.Secret{Type: corev1.SecretTypeServiceAccountToken},
			wantRefresh: true,
		},
		{
			name: "empty token",
			secret: &corev1.Secret{
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{metricsTokenKey: []byte("")},
			},
			wantRefresh: true,
		},
		{
			name: "missing expiry annotation",
			secret: &corev1.Secret{
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{metricsTokenKey: []byte("tok")},
			},
			wantRefresh: true,
		},
		{
			name: "unparseable expiry annotation",
			secret: &corev1.Secret{
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{metricsTokenKey: []byte("tok")},
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{metricsTokenExpiryAnnotation: "not-a-time"},
				},
			},
			wantRefresh: true,
		},
		{
			name: "far from expiry",
			secret: &corev1.Secret{
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{metricsTokenKey: []byte("tok")},
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						metricsTokenExpiryAnnotation: now.Add(time.Hour).Format(time.RFC3339),
					},
				},
			},
			wantRefresh: false,
		},
		{
			name: "within renewal threshold",
			secret: &corev1.Secret{
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{metricsTokenKey: []byte("tok")},
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						metricsTokenExpiryAnnotation: now.Add(5 * time.Minute).Format(time.RFC3339),
					},
				},
			},
			wantRefresh: true,
		},
		{
			name: "already expired",
			secret: &corev1.Secret{
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{metricsTokenKey: []byte("tok")},
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						metricsTokenExpiryAnnotation: now.Add(-time.Minute).Format(time.RFC3339),
					},
				},
			},
			wantRefresh: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			refresh, _, _ := evaluateTokenRenewal(tt.secret, now)
			if refresh != tt.wantRefresh {
				t.Errorf("evaluateTokenRenewal() refresh = %v, want %v", refresh, tt.wantRefresh)
			}
		})
	}
}

func TestRequeueAfterFor(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		expiry time.Time
		name   string
		want   time.Duration
	}{
		{
			// 1h lifetime, 20% threshold = 12m. Expiring in 1m is already past that threshold.
			name:   "floors at metricsTokenMinRequeueAfter when already at/past the renewal threshold",
			expiry: now.Add(time.Minute),
			want:   metricsTokenMinRequeueAfter,
		},
		{
			// 1h - 12m threshold = 48m. No ceiling: external tampering is caught by the Watch,
			// not by re-polling, so there's nothing capping how far out this can be.
			name:   "schedules well out into the future for a freshly minted token, with no ceiling",
			expiry: now.Add(time.Hour),
			want:   48 * time.Minute,
		},
		{
			// 20m - 12m threshold = 8m, above the 1m floor, so it passes through unclamped.
			name:   "returns the unclamped remaining-minus-threshold duration above the floor",
			expiry: now.Add(20 * time.Minute),
			want:   8 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := requeueAfterFor(tt.expiry, now); got != tt.want {
				t.Errorf("requeueAfterFor() = %v, want %v", got, tt.want)
			}
		})
	}
}

// newTestReconciler returns a MetricsTokenReconciler backed by a fake client seeded with objs,
// along with that same client for assertions.
func newTestReconciler(objs ...client.Object) (*MetricsTokenReconciler, client.Client) {
	fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(objs...).Build()
	return &MetricsTokenReconciler{
		Client:             fakeClient,
		Namespace:          testNamespace,
		ServiceAccountName: testServiceAccountName,
	}, fakeClient
}

func TestMetricsTokenReconciler_Reconcile(t *testing.T) {
	t.Parallel()
	secretKey := types.NamespacedName{Namespace: testNamespace, Name: MetricsTokenSecretName}
	req := ctrl.Request{NamespacedName: secretKey}

	t.Run("creates the secret when it does not exist", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		reconciler, fakeClient := newTestReconciler()
		reconciler.requestToken = stubTokenRequester("minted-token", time.Now().Add(time.Hour), nil)

		result, err := reconciler.Reconcile(ctx, req)
		if err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
		if result.RequeueAfter < metricsTokenMinRequeueAfter {
			t.Errorf("RequeueAfter = %v, want >= %v", result.RequeueAfter, metricsTokenMinRequeueAfter)
		}

		var secret corev1.Secret
		if err := fakeClient.Get(ctx, secretKey, &secret); err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if secret.Type != corev1.SecretTypeOpaque {
			t.Errorf("Type = %v, want Opaque", secret.Type)
		}
		if got := string(secret.Data[metricsTokenKey]); got != "minted-token" {
			t.Errorf("token = %q, want %q", got, "minted-token")
		}
		if _, ok := secret.Annotations[metricsTokenExpiryAnnotation]; !ok {
			t.Error("missing expiry annotation")
		}
	})

	t.Run("does not mint a new token when the existing one is fresh", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		fresh := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      MetricsTokenSecretName,
				Namespace: testNamespace,
				Annotations: map[string]string{
					metricsTokenExpiryAnnotation: time.Now().Add(time.Hour).Format(time.RFC3339),
				},
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{metricsTokenKey: []byte("still-fresh")},
		}
		reconciler, fakeClient := newTestReconciler(fresh)
		reconciler.requestToken = func(context.Context, client.Client, string, string, int64) (string, time.Time, error) {
			t.Fatal("requestToken should not be called when the token is still fresh")
			return "", time.Time{}, nil
		}

		result, err := reconciler.Reconcile(ctx, req)
		if err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
		if result.RequeueAfter <= metricsTokenMinRequeueAfter {
			t.Errorf("RequeueAfter = %v, want > %v", result.RequeueAfter, metricsTokenMinRequeueAfter)
		}

		var secret corev1.Secret
		if err := fakeClient.Get(ctx, secretKey, &secret); err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if got := string(secret.Data[metricsTokenKey]); got != "still-fresh" {
			t.Errorf("token = %q, want %q", got, "still-fresh")
		}
	})

	t.Run("rotates the token in place when nearing expiry", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		nearExpiry := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      MetricsTokenSecretName,
				Namespace: testNamespace,
				Annotations: map[string]string{
					metricsTokenExpiryAnnotation: time.Now().Add(time.Minute).Format(time.RFC3339),
				},
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{metricsTokenKey: []byte("about-to-expire")},
		}
		reconciler, fakeClient := newTestReconciler(nearExpiry)
		reconciler.requestToken = stubTokenRequester("rotated-token", time.Now().Add(time.Hour), nil)

		if _, err := reconciler.Reconcile(ctx, req); err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}

		var secret corev1.Secret
		if err := fakeClient.Get(ctx, secretKey, &secret); err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if got := string(secret.Data[metricsTokenKey]); got != "rotated-token" {
			t.Errorf("token = %q, want %q", got, "rotated-token")
		}
	})

	t.Run("migrates away from a legacy-typed secret via delete+create", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		legacy := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      MetricsTokenSecretName,
				Namespace: testNamespace,
				Annotations: map[string]string{
					corev1.ServiceAccountNameKey: testServiceAccountName,
				},
			},
			Type: corev1.SecretTypeServiceAccountToken,
			Data: map[string][]byte{metricsTokenKey: []byte("legacy-token")},
		}
		reconciler, fakeClient := newTestReconciler(legacy)
		reconciler.requestToken = stubTokenRequester("modern-token", time.Now().Add(time.Hour), nil)

		if _, err := reconciler.Reconcile(ctx, req); err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}

		var secret corev1.Secret
		if err := fakeClient.Get(ctx, secretKey, &secret); err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if secret.Type != corev1.SecretTypeOpaque {
			t.Errorf("Type = %v, want Opaque", secret.Type)
		}
		if got := string(secret.Data[metricsTokenKey]); got != "modern-token" {
			t.Errorf("token = %q, want %q", got, "modern-token")
		}
	})

	t.Run("returns an error (left to controller-runtime's backoff) when the token request fails", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		reconciler, _ := newTestReconciler()
		reconciler.requestToken = stubTokenRequester("", time.Time{}, errors.New("injected failure"))

		_, err := reconciler.Reconcile(ctx, req)
		if err == nil || !strings.Contains(err.Error(), "injected failure") {
			t.Errorf("Reconcile() error = %v, want containing %q", err, "injected failure")
		}
	})

	t.Run("returns an error when the token request returns an empty token", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		reconciler, _ := newTestReconciler()
		reconciler.requestToken = stubTokenRequester("", time.Now().Add(time.Hour), nil)

		_, err := reconciler.Reconcile(ctx, req)
		if err == nil || !strings.Contains(err.Error(), "empty token") {
			t.Errorf("Reconcile() error = %v, want containing %q", err, "empty token")
		}
	})
}

func TestNewMetricsTokenReconciler(t *testing.T) {
	t.Parallel()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
	reconciler := NewMetricsTokenReconciler(fakeClient, testNamespace, testServiceAccountName)

	if reconciler.Namespace != testNamespace {
		t.Errorf("Namespace = %q, want %q", reconciler.Namespace, testNamespace)
	}
	if reconciler.ServiceAccountName != testServiceAccountName {
		t.Errorf("ServiceAccountName = %q, want %q", reconciler.ServiceAccountName, testServiceAccountName)
	}
	if reconciler.requestToken == nil {
		t.Error("requestToken is nil, want the production token requester wired in")
	}
}

func TestMetricsTokenSecretPredicate(t *testing.T) {
	t.Parallel()
	pred := metricsTokenSecretPredicate(testNamespace)

	tests := []struct {
		secret *corev1.Secret
		name   string
		want   bool
	}{
		{
			name:   "matches the metrics-token secret in the configured namespace",
			secret: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: MetricsTokenSecretName, Namespace: testNamespace}},
			want:   true,
		},
		{
			name:   "ignores secrets with a different name",
			secret: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "other-secret", Namespace: testNamespace}},
			want:   false,
		},
		{
			name:   "ignores secrets in a different namespace",
			secret: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: MetricsTokenSecretName, Namespace: "other-namespace"}},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := pred.Create(event.CreateEvent{Object: tt.secret}); got != tt.want {
				t.Errorf("predicate.Create() = %v, want %v", got, tt.want)
			}
		})
	}
}
