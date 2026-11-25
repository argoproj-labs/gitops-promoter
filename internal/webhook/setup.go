/*
Copyright 2026.

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

package webhook

// Adapted from the webhook setup in PR #2343 (Zach Aller).

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/open-policy-agent/cert-controller/pkg/rotator"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	crwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"
)

// FieldManager is the field manager of every field the webhook certificate rotator writes: the Secret's data and
// the ValidatingWebhookConfiguration's caBundle. GitOps tools re-applying the shipped (empty) manifests therefore
// leave the generated values alone.
const FieldManager = "promoter-webhook-cert-rotator"

// Options configures the controller's validating admission webhook and the self-managed certificate it is served
// with.
type Options struct {
	// CertDir is where the webhook server reads its certificate: the Secret's mount point.
	CertDir string
	// SecretName holds the CA and serving certificate, in the controller namespace.
	SecretName string
	// ServiceName is the Service the API server reaches the webhook through; it names the certificate's DNS name.
	ServiceName string
	// ConfigurationName is the ValidatingWebhookConfiguration whose caBundle is kept in sync.
	ConfigurationName string
	// Enabled serves the webhook. Disable it only when the ValidatingWebhookConfiguration is not installed, such
	// as when running the controller from a development machine.
	Enabled bool
}

// AddFlags registers the webhook flags.
func (o *Options) AddFlags(flags *pflag.FlagSet) {
	flags.BoolVar(&o.Enabled, "admission-webhook", true,
		"Serve the validating admission webhook for KubernetesResourceCommitStatus. The controller generates, "+
			"rotates, and injects its certificate. Disable only when the ValidatingWebhookConfiguration is not "+
			"installed, such as when running from a development machine.")
	flags.StringVar(&o.CertDir, "webhook-cert-dir", filepath.Join("/tmp", "k8s-webhook-server", "serving-certs"),
		"Directory the webhook server reads its certificate from; mount the --webhook-cert-secret Secret here.")
	flags.StringVar(&o.SecretName, "webhook-cert-secret", "promoter-webhook-server-cert",
		"Secret in the controller namespace holding the webhook CA and serving certificate.")
	flags.StringVar(&o.ServiceName, "webhook-service", "promoter-webhook-service",
		"Service in the controller namespace that fronts the webhook server.")
	flags.StringVar(&o.ConfigurationName, "webhook-configuration", "promoter-validating-webhook-configuration",
		"ValidatingWebhookConfiguration whose caBundle the controller keeps current.")
}

// Setup adds the certificate rotator to mgr and registers validator once the certificate is on disk: the webhook
// server reads its certificate when it starts, so it must not start before. Every replica rotates and serves (the
// rotator does not need leader election). The returned readiness check fails until the webhook server has
// started, so the Service routes only to replicas that can answer.
func Setup(ctx context.Context, mgr ctrl.Manager, server crwebhook.Server, opts Options, namespace string, validator *KubernetesResourceCommitStatusValidator) (healthz.Checker, error) {
	logger := ctrl.Log.WithName("webhook-setup")
	// Direct client, not mgr.GetClient(): the controller's cache partitions Secrets by instance-id, strips their
	// data, and makes writes wait on it. cert-controller reads through its own cache and only needs a writer. The
	// client-wide field owner attributes the Secret's data, like the caBundle, to FieldManager. Same approach as
	// the dashboard apiserver's rotation in internal/certrotation.
	writer, err := client.New(mgr.GetConfig(), client.Options{
		Scheme:     mgr.GetScheme(),
		Mapper:     mgr.GetRESTMapper(),
		FieldOwner: FieldManager,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create the webhook certificate rotation client: %w", err)
	}
	certsReady := make(chan struct{})
	if err := rotator.AddRotator(writerManager{Manager: mgr, writer: writer}, &rotator.CertRotator{
		SecretKey:      types.NamespacedName{Namespace: namespace, Name: opts.SecretName},
		CertDir:        opts.CertDir,
		CAName:         "gitops-promoter-webhook-ca",
		CAOrganization: "gitops-promoter",
		DNSName:        fmt.Sprintf("%s.%s.svc", opts.ServiceName, namespace),
		ExtraDNSNames:  []string{fmt.Sprintf("%s.%s.svc.cluster.local", opts.ServiceName, namespace)},
		IsReady:        certsReady,
		Webhooks:       []rotator.WebhookInfo{{Name: opts.ConfigurationName, Type: rotator.Validating}},
		// Every replica serves the webhook, so every replica needs the certificate on disk.
		RequireLeaderElection: false,
		FieldOwner:            FieldManager,
		// The controller also runs the dashboard apiserver's rotator; controller names must be unique.
		ControllerName: FieldManager,
	}); err != nil {
		return nil, fmt.Errorf("failed to add the webhook certificate rotator: %w", err)
	}

	registered := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-certsReady:
		}
		logger.Info("webhook certificate ready, registering admission webhooks")
		if err := validator.SetupWithManager(mgr); err != nil {
			logger.Error(err, "unable to register the KubernetesResourceCommitStatus webhook")
			return
		}
		close(registered)
	}()

	started := server.StartedChecker()
	return func(req *http.Request) error {
		select {
		case <-registered:
			return started(req)
		default:
			return errors.New("the webhook certificate is not ready yet")
		}
	}, nil
}

// writerManager is a manager whose GetClient returns writer; cert-controller takes its writer from GetClient.
type writerManager struct {
	ctrl.Manager
	writer client.Client
}

func (m writerManager) GetClient() client.Client { return m.writer }
