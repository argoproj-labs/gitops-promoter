package metrics

import (
	"context"
	"errors"
	"fmt"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	// MetricsTokenSecretName is the Secret that the ServiceMonitor (config/prometheus/monitor.yaml)
	// references as its bearer token credential (authorization.credentials.name).
	MetricsTokenSecretName = "metrics-token"

	metricsTokenKey              = "token"
	metricsTokenExpiryAnnotation = "promoter.argoproj.io/token-expiry"
	// metricsTokenExpirySeconds is the lifetime requested for each minted token.
	metricsTokenExpirySeconds = int64(3600)
	// metricsTokenRenewalPercent: renew once less than this percentage of the token's lifetime remains.
	metricsTokenRenewalPercent = 20
	// metricsTokenMinRequeueAfter is a floor on the self-scheduled RequeueAfter, so clock skew (or
	// a token that's already past its renewal threshold) can't produce a zero/negative/tight-loop
	// requeue.
	metricsTokenMinRequeueAfter = time.Minute
)

// tokenRequestFunc mints a bound, time-limited token for the given ServiceAccount via the TokenRequest API
type tokenRequestFunc func(ctx context.Context, c client.Client, namespace, serviceAccountName string, expirationSeconds int64) (token string, expiry time.Time, err error)

// requestServiceAccountToken is the production tokenRequestFunc, backed by the serviceaccounts/token subresource.
func requestServiceAccountToken(ctx context.Context, c client.Client, namespace, serviceAccountName string, expirationSeconds int64) (string, time.Time, error) {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: serviceAccountName, Namespace: namespace},
	}
	tr := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			ExpirationSeconds: &expirationSeconds,
		},
	}
	if err := c.SubResource("token").Create(ctx, sa, tr); err != nil {
		return "", time.Time{}, fmt.Errorf("requesting token for service account %s/%s: %w", namespace, serviceAccountName, err)
	}
	return tr.Status.Token, tr.Status.ExpirationTimestamp.Time, nil
}

// MetricsTokenReconciler mints and keeps fresh the bearer token that the ServiceMonitor
// (config/prometheus/monitor.yaml) presents to the manager's own secure /metrics endpoint.
//
// Why this exists: Prometheus Operator's ServiceMonitor can only source a bearer token from a
// Secret (endpoints[].authorization.credentials) - it cannot read a local token file via
// "credentialsFile" the way the deprecated bearerTokenFile did (credentialsFile is only
// available on Prometheus.spec.scrapeClasses, a different CR this project doesn't own), and
// bearerTokenFile itself is rejected outright by OpenShift's user-workload-monitoring admission.
// Kubernetes also no longer auto-populates a long-lived token into a Secret just from the
// kubernetes.io/service-account.name annotation the way it used to - and even where it still
// does, that token never expires, which is exactly what we're trying to avoid. So something has
// to call the TokenRequest API and keep the Secret's token fresh; this does that in-process, for
// the manager's own ServiceAccount, which is also the principal config/rbac/metrics_reader_role_binding.yaml
// authorizes to read /metrics.
//
// It reacts to two independent triggers, rather than polling the secret's contents on a timer:
//   - a Watch on that specific Secret (SetupWithManager), so an external delete/edit is corrected
//     as soon as controller-runtime observes it (and retried with the controller's normal
//     exponential backoff on failure) - not on the next poll.
//   - ctrl.Result.RequeueAfter, scheduled for exactly when the current token will cross its
//     renewal threshold, so there is no busy-checking in between.
type MetricsTokenReconciler struct {
	client.Client
	requestToken tokenRequestFunc

	Namespace          string
	ServiceAccountName string
}

// NewMetricsTokenReconciler returns a reconciler that mints/rotates the metrics-token Secret in
// namespace for serviceAccountName. Call SetupWithManager to register it.
func NewMetricsTokenReconciler(c client.Client, namespace, serviceAccountName string) *MetricsTokenReconciler {
	return &MetricsTokenReconciler{
		Client:             c,
		requestToken:       requestServiceAccountToken,
		Namespace:          namespace,
		ServiceAccountName: serviceAccountName,
	}
}

// SetupWithManager registers the reconciler, watching only the metrics-token Secret in
// r.Namespace, plus a one-shot bootstrap event so the Secret is created
// even on a brand new cluster where it has never existed yet.
func (r *MetricsTokenReconciler) SetupWithManager(mgr ctrl.Manager) error {
	err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Secret{}).
		WatchesRawSource(source.Channel(newBootstrapEvent(r.Namespace), &handler.EnqueueRequestForObject{})).
		WithEventFilter(metricsTokenSecretPredicate(r.Namespace)).
		Named("metrics-token").
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create metrics-token controller: %w", err)
	}
	return nil
}

// newBootstrapEvent returns a channel containing exactly one event for the metrics-token Secret
// in namespace, already closed. A real Kubernetes Watch only fires when something happens to an
// object; on a brand new cluster the Secret has never existed, so there's nothing for a Watch to
// react to yet. This feeds the controller one synthetic "it changed" event at startup so the
// first Reconcile actually runs and creates it.
func newBootstrapEvent(namespace string) <-chan event.GenericEvent {
	ch := make(chan event.GenericEvent, 1)
	ch <- event.GenericEvent{Object: &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: MetricsTokenSecretName, Namespace: namespace},
	}}
	close(ch)
	return ch
}

func metricsTokenSecretPredicate(namespace string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetNamespace() == namespace && obj.GetName() == MetricsTokenSecretName
	})
}

// Reconcile checks the metrics-token secret and, if it is missing, malformed, or close to
// expiring, mints a fresh token and writes it back. The returned RequeueAfter schedules the next
// routine check for exactly when the (possibly just-minted) token will need renewing; on error,
// controller-runtime retries with its normal exponential backoff instead.
func (r *MetricsTokenReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	key := types.NamespacedName{Namespace: r.Namespace, Name: MetricsTokenSecretName}

	var secret corev1.Secret
	err := r.Get(ctx, key, &secret)
	switch {
	case err == nil:
		needsRefresh, reason, expiry := evaluateTokenRenewal(&secret, time.Now())
		if !needsRefresh {
			return ctrl.Result{RequeueAfter: requeueAfterFor(expiry, time.Now())}, nil
		}
		logger.Info("renewing metrics token", "reason", reason)
	case apierrors.IsNotFound(err):
		logger.Info("metrics token secret not found; minting initial token")
	default:
		return ctrl.Result{}, fmt.Errorf("getting metrics token secret: %w", err)
	}

	token, expiry, err := r.requestToken(ctx, r.Client, r.Namespace, r.ServiceAccountName, metricsTokenExpirySeconds)
	if err != nil {
		return ctrl.Result{}, err
	}
	if token == "" {
		return ctrl.Result{}, errors.New("token request returned an empty token")
	}

	if err := r.applySecret(ctx, key, token, expiry); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: requeueAfterFor(expiry, time.Now())}, nil
}

// evaluateTokenRenewal reports whether secret's token should be renewed - when the secret is the
// wrong type, missing a non-empty token, missing/unparsable expiry metadata (never trust a
// stored expiry we can't actually validate), or within metricsTokenRenewalPercent of expiring.
// When needsRefresh is false, expiry is the token's parsed expiry time (for scheduling the next check).
func evaluateTokenRenewal(secret *corev1.Secret, now time.Time) (needsRefresh bool, reason string, expiry time.Time) {
	if secret.Type != corev1.SecretTypeOpaque {
		return true, fmt.Sprintf("secret type %q is not Opaque (likely the legacy annotation-based secret)", secret.Type), time.Time{}
	}
	if len(secret.Data[metricsTokenKey]) == 0 {
		return true, "token value is empty", time.Time{}
	}
	raw, ok := secret.Annotations[metricsTokenExpiryAnnotation]
	if !ok {
		return true, "expiry annotation is missing", time.Time{}
	}
	parsedExpiry, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return true, "expiry annotation is unparsable", time.Time{}
	}

	lifetime := time.Duration(metricsTokenExpirySeconds) * time.Second
	threshold := lifetime * time.Duration(metricsTokenRenewalPercent) / 100
	if remaining := parsedExpiry.Sub(now); remaining <= threshold {
		return true, fmt.Sprintf("token expires in %s (<= %s renewal threshold)", remaining.Round(time.Second), threshold), time.Time{}
	}
	return false, "", parsedExpiry
}

// requeueAfterFor returns how long to wait before the next routine check, given the token's
// expiry: the time remaining until it crosses its renewal threshold, floored at
// metricsTokenMinRequeueAfter so clock skew can't produce a zero/negative/tight-loop requeue.
func requeueAfterFor(expiry, now time.Time) time.Duration {
	lifetime := time.Duration(metricsTokenExpirySeconds) * time.Second
	threshold := lifetime * time.Duration(metricsTokenRenewalPercent) / 100
	if next := expiry.Sub(now) - threshold; next > metricsTokenMinRequeueAfter {
		return next
	}
	return metricsTokenMinRequeueAfter
}

// applySecret writes token/expiry into the metrics-token secret. A Secret's type field is
// immutable, so migrating away from a differently-typed secret (e.g. the legacy
// kubernetes.io/service-account-token annotation secret) requires Delete+Create; every other
// path - the common case - is a single atomic Create or Update.
func (r *MetricsTokenReconciler) applySecret(ctx context.Context, key types.NamespacedName, token string, expiry time.Time) error {
	desiredAnnotations := map[string]string{metricsTokenExpiryAnnotation: expiry.UTC().Format(time.RFC3339)}
	desiredData := map[string][]byte{metricsTokenKey: []byte(token)}

	var existing corev1.Secret
	err := r.Get(ctx, key, &existing)
	if apierrors.IsNotFound(err) {
		return r.createSecret(ctx, key, desiredAnnotations, desiredData)
	}
	if err != nil {
		return fmt.Errorf("getting metrics token secret before update: %w", err)
	}

	if existing.Type != corev1.SecretTypeOpaque {
		// Type is immutable on Secrets; this path is only hit once, migrating away from a
		// differently-typed secret such as the legacy annotation-based token secret.
		if err := r.Delete(ctx, &existing); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting legacy-typed metrics token secret: %w", err)
		}
		return r.createSecret(ctx, key, desiredAnnotations, desiredData)
	}

	if existing.Annotations == nil {
		existing.Annotations = make(map[string]string, 1)
	}
	existing.Annotations[metricsTokenExpiryAnnotation] = desiredAnnotations[metricsTokenExpiryAnnotation]
	existing.Data = desiredData
	if err := r.Update(ctx, &existing); err != nil {
		return fmt.Errorf("updating metrics token secret: %w", err)
	}
	return nil
}

func (r *MetricsTokenReconciler) createSecret(ctx context.Context, key types.NamespacedName, annotations map[string]string, data map[string][]byte) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        key.Name,
			Namespace:   key.Namespace,
			Annotations: annotations,
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
	}
	if err := r.Create(ctx, secret); err != nil {
		return fmt.Errorf("creating metrics token secret: %w", err)
	}
	return nil
}
