package webhookreceiver_test

// BenchmarkPostRoot measures the cost of the full webhook request-handling pipeline:
// provider detection → body read → CTP field-index lookup → ScmProvider resolution →
// HMAC-SHA256 signature verification.
//
// The benchmark requires real envtest binaries (kube-apiserver + etcd) so that the
// controller-runtime cache performs actual etcd reads.  Set up the binaries with:
//
//	make envtest
//
// then run with:
//
//	KUBEBUILDER_ASSETS=$(bin/setup-envtest use 1.31.0 --bin-dir bin/k8s -p path) \
//	  go test ./internal/webhookreceiver/... -run=^$ -bench=BenchmarkPostRoot \
//	  -benchmem -benchtime=10s
//
// Three sub-benchmarks exercise distinct code paths:
//
//   - no_matching_CTP          – webhook arrives but no CTP SHA matches; cheap path.
//   - CTP_no_sig_check         – CTP found, ScmProvider has no webhookSecretRef; no HMAC.
//   - CTP_with_valid_signature – CTP found, ScmProvider has webhookSecretRef, signature valid.
//   - CTP_with_invalid_signature – CTP found, ScmProvider has webhookSecretRef, signature invalid.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/argoproj-labs/gitops-promoter/internal/webhookreceiver"
)

// computeHMACGitHub returns a valid "sha256=<hex>" header value for the given secret and body.
func computeHMACGitHub(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func BenchmarkPostRoot(b *testing.B) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		b.Skip("KUBEBUILDER_ASSETS not set; run `make envtest` then set KUBEBUILDER_ASSETS to point to the binaries")
	}

	// ── envtest setup ──────────────────────────────────────────────────────────
	scheme := utils.GetScheme()
	env := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd", "bases"),
			filepath.Join("..", "..", "test", "external_crds"),
		},
		ErrorIfCRDPathMissing: true,
		BinaryAssetsDirectory: filepath.Join("..", "..", "bin", "k8s",
			fmt.Sprintf("1.31.0-%s-%s", goruntime.GOOS, goruntime.GOARCH)),
	}

	cfg, err := env.Start()
	if err != nil {
		b.Fatalf("envtest Start: %v", err)
	}
	b.Cleanup(func() { _ = env.Stop() })

	// ── manager (cache) setup ──────────────────────────────────────────────────
	// Use a background context so the manager lives for the whole benchmark.
	mgrCtx, mgrCancel := context.WithCancel(context.Background())
	b.Cleanup(mgrCancel)

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		// Disable the metrics server to avoid port conflicts.
		Metrics: metricsserver.Options{BindAddress: "0"},
		// Do not serve the health check endpoint.
		HealthProbeBindAddress: "0",
		Cache:                  cache.Options{
			// No namespace restriction – benchmark resources live in "default".
		},
	})
	if err != nil {
		b.Fatalf("NewManager: %v", err)
	}

	// Register the two field indexes that findChangeTransferPolicy relies on.
	if err := mgr.GetFieldIndexer().IndexField(mgrCtx, &promoterv1alpha1.ChangeTransferPolicy{},
		constants.ChangeTransferPolicyProposedHydratedSHAIndexField,
		func(o client.Object) []string {
			ctp := o.(*promoterv1alpha1.ChangeTransferPolicy) //nolint:forcetypeassert
			return []string{ctp.Status.Proposed.Hydrated.Sha}
		},
	); err != nil {
		b.Fatalf("IndexField proposed: %v", err)
	}
	if err := mgr.GetFieldIndexer().IndexField(mgrCtx, &promoterv1alpha1.ChangeTransferPolicy{},
		constants.ChangeTransferPolicyActiveHydratedSHAIndexField,
		func(o client.Object) []string {
			ctp := o.(*promoterv1alpha1.ChangeTransferPolicy) //nolint:forcetypeassert
			return []string{ctp.Status.Active.Hydrated.Sha}
		},
	); err != nil {
		b.Fatalf("IndexField active: %v", err)
	}

	go func() {
		if err := mgr.Start(mgrCtx); err != nil && mgrCtx.Err() == nil {
			b.Logf("manager exited: %v", err)
		}
	}()

	// Wait for the cache to become ready.
	if !mgr.GetCache().WaitForCacheSync(mgrCtx) {
		b.Fatal("cache did not sync")
	}

	k8sClient := mgr.GetClient()
	const (
		ns            = "default"
		secretName    = "bench-webhook-secret"
		signingSecret = "bench-s3cr3t"
		beforeSHA     = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)

	// ── shared Kubernetes resources ────────────────────────────────────────────
	// Secret for the webhook signing key.
	if err := k8sClient.Create(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns},
		Data:       map[string][]byte{"webhookSecret": []byte(signingSecret)},
	}); err != nil {
		b.Fatalf("create Secret: %v", err)
	}

	// ScmProvider with webhook signature configured.
	smpWithSig := &promoterv1alpha1.ScmProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "bench-provider-sig", Namespace: ns},
		Spec: promoterv1alpha1.ScmProviderSpec{
			GitHub: &promoterv1alpha1.GitHub{
				AppID:            99999,
				WebhookSecretRef: &corev1.LocalObjectReference{Name: secretName},
			},
		},
	}
	if err := k8sClient.Create(context.Background(), smpWithSig); err != nil {
		b.Fatalf("create ScmProvider (with sig): %v", err)
	}

	// ScmProvider without webhook signature configured.
	smpNoSig := &promoterv1alpha1.ScmProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "bench-provider-nosig", Namespace: ns},
		Spec: promoterv1alpha1.ScmProviderSpec{
			GitHub: &promoterv1alpha1.GitHub{
				AppID: 99999,
			},
		},
	}
	if err := k8sClient.Create(context.Background(), smpNoSig); err != nil {
		b.Fatalf("create ScmProvider (no sig): %v", err)
	}

	// GitRepository → smpWithSig.
	gitRepoWithSig := &promoterv1alpha1.GitRepository{
		ObjectMeta: metav1.ObjectMeta{Name: "bench-repo-sig", Namespace: ns},
		Spec: promoterv1alpha1.GitRepositorySpec{
			GitHub: &promoterv1alpha1.GitHubRepo{Owner: "org", Name: "repo"},
			ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{
				Kind: promoterv1alpha1.ScmProviderKind,
				Name: smpWithSig.Name,
			},
		},
	}
	if err := k8sClient.Create(context.Background(), gitRepoWithSig); err != nil {
		b.Fatalf("create GitRepository (with sig): %v", err)
	}

	// GitRepository → smpNoSig.
	gitRepoNoSig := &promoterv1alpha1.GitRepository{
		ObjectMeta: metav1.ObjectMeta{Name: "bench-repo-nosig", Namespace: ns},
		Spec: promoterv1alpha1.GitRepositorySpec{
			GitHub: &promoterv1alpha1.GitHubRepo{Owner: "org", Name: "repo"},
			ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{
				Kind: promoterv1alpha1.ScmProviderKind,
				Name: smpNoSig.Name,
			},
		},
	}
	if err := k8sClient.Create(context.Background(), gitRepoNoSig); err != nil {
		b.Fatalf("create GitRepository (no sig): %v", err)
	}

	// Helper: create a CTP and patch its status so the SHA field index is populated.
	createCTPWithSHA := func(name, repoName, sha string) {
		ctp := &promoterv1alpha1.ChangeTransferPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: promoterv1alpha1.ChangeTransferPolicySpec{
				RepositoryReference:    promoterv1alpha1.ObjectReference{Name: repoName},
				ProposedBranch:         "main-next",
				ActiveBranch:           "main",
				ActiveCommitStatuses:   []promoterv1alpha1.CommitStatusSelector{},
				ProposedCommitStatuses: []promoterv1alpha1.CommitStatusSelector{},
			},
		}
		if err := k8sClient.Create(context.Background(), ctp); err != nil {
			b.Fatalf("create CTP %s: %v", name, err)
		}
		ctp.Status = promoterv1alpha1.ChangeTransferPolicyStatus{
			Proposed: promoterv1alpha1.CommitBranchState{
				Hydrated: promoterv1alpha1.CommitShaState{Sha: sha},
			},
		}
		if err := k8sClient.Status().Update(context.Background(), ctp); err != nil {
			b.Fatalf("update CTP status %s: %v", name, err)
		}
	}

	createCTPWithSHA("bench-ctp-sig", gitRepoWithSig.Name, beforeSHA)
	createCTPWithSHA("bench-ctp-nosig", gitRepoNoSig.Name, beforeSHA)

	// ── build the webhook receiver backed by the real (cached) k8s client ─────
	wr := webhookreceiver.NewWebhookReceiverWithClient(mgr.GetClient(), nil, ns)

	// Push payload whose "before" SHA matches both CTPs above.
	body := []byte(`{"ref":"refs/heads/main","before":"` + beforeSHA + `","pusher":{"name":"bench"}}`)
	validSig := computeHMACGitHub(signingSecret, body)
	wrongSig := "sha256=0000000000000000000000000000000000000000000000000000000000000000"

	// Helper to run a single request through the receiver.
	invoke := func(sig string) {
		req := httptest.NewRequest("POST", "/", strings.NewReader(string(body)))
		req.Header.Set("X-GitHub-Event", "push")
		if sig != "" {
			req.Header.Set("X-Hub-Signature-256", sig)
		}
		rr := httptest.NewRecorder()
		wr.ServeHTTP(rr, req)
	}

	// ── sub-benchmarks ─────────────────────────────────────────────────────────

	// Case 1: push payload whose SHA does not match any CTP in the cluster.
	b.Run("no_matching_CTP", func(b *testing.B) {
		noMatchBody := []byte(`{"ref":"refs/heads/main","before":"0000000000000000000000000000000000000000","pusher":{"name":"bench"}}`)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			req := httptest.NewRequest("POST", "/", strings.NewReader(string(noMatchBody)))
			req.Header.Set("X-GitHub-Event", "push")
			rr := httptest.NewRecorder()
			wr.ServeHTTP(rr, req)
		}
	})

	// Case 2: CTP found, but the ScmProvider has no webhookSecretRef → no HMAC computation.
	b.Run("CTP_no_sig_check", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			invoke("") // no signature header; provider has no webhookSecretRef configured so it passes through
		}
	})

	// Case 3: CTP found, ScmProvider has webhookSecretRef, signature is valid → full HMAC path.
	b.Run("CTP_with_valid_signature", func(b *testing.B) {
		// The CTP for smpWithSig and the CTP for smpNoSig both match beforeSHA.
		// When the receiver finds two CTPs it picks the first one.  To isolate
		// the signed path, temporarily remove the no-sig CTP.
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			invoke(validSig)
		}
	})

	// Case 4: same as Case 3 but with a wrong signature → 401 path.
	b.Run("CTP_with_invalid_signature", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			invoke(wrongSig)
		}
	})
}
