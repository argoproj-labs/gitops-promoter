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

// Package webhook serves the controller's admission webhooks with a certificate the controller manages itself.
package webhook

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/open-policy-agent/cert-controller/pkg/rotator"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	crwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/argoproj-labs/gitops-promoter/internal/resourcetemplate"
	webhookv1alpha1 "github.com/argoproj-labs/gitops-promoter/internal/webhook/v1alpha1"
)

// Options configures the controller's validating admission webhook and the self-managed
// certificate it is served with.
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

// AddFlags registers the webhook flags on cmd.
func (o *Options) AddFlags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&o.Enabled, "admission-webhook", true,
		"Serve the validating admission webhook. The controller generates, rotates, and injects its certificate. "+
			"Disable only when the ValidatingWebhookConfiguration is not installed, "+
			"such as when running from a development machine.")
	cmd.Flags().StringVar(&o.CertDir, "webhook-cert-dir", filepath.Join("/tmp", "k8s-webhook-server", "serving-certs"),
		"Directory the webhook server reads its certificate from; mount the --webhook-cert-secret Secret here.")
	cmd.Flags().StringVar(&o.SecretName, "webhook-cert-secret", "promoter-webhook-server-cert",
		"Secret in the controller namespace holding the webhook CA and serving certificate.")
	cmd.Flags().StringVar(&o.ServiceName, "webhook-service", "promoter-webhook-service",
		"Service in the controller namespace that fronts the webhook server.")
	cmd.Flags().StringVar(&o.ConfigurationName, "webhook-configuration", "promoter-validating-webhook-configuration",
		"ValidatingWebhookConfiguration whose caBundle the controller keeps current.")
}

// Setup adds the certificate rotator to mgr and registers the webhooks once the certificate is on
// disk: the webhook server reads its certificate when it starts, so it must not start before. Every replica
// rotates and serves (the rotator does not need leader election). The returned readiness check fails until the
// webhook server has started, so the Service routes only to replicas that can answer.
func Setup(
	ctx context.Context,
	mgr ctrl.Manager,
	server crwebhook.Server,
	opts Options,
	namespace string,
	templates *resourcetemplate.Validator,
	isConfiguredKind func(schema.GroupVersionKind) bool,
) (healthz.Checker, error) {
	logger := ctrl.Log.WithName("webhook-setup")
	certsReady := make(chan struct{})
	if err := rotator.AddRotator(mgr, &rotator.CertRotator{
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
		FieldOwner:            "gitops-promoter-cert-rotator",
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
		validator := &webhookv1alpha1.KubernetesResourceCommitStatusValidator{
			Templates:        templates,
			IsConfiguredKind: isConfiguredKind,
		}
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
