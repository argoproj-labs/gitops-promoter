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

package main

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
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/argoproj-labs/gitops-promoter/internal/resourcetemplate"
	webhookv1alpha1 "github.com/argoproj-labs/gitops-promoter/internal/webhook/v1alpha1"
)

// admissionWebhookOptions configures the controller's validating admission webhook and the self-managed
// certificate it is served with.
type admissionWebhookOptions struct {
	// certDir is where the webhook server reads its certificate: the Secret's mount point.
	certDir string
	// secretName holds the CA and serving certificate, in the controller namespace.
	secretName string
	// serviceName is the Service the API server reaches the webhook through; it names the certificate's DNS name.
	serviceName string
	// configurationName is the ValidatingWebhookConfiguration whose caBundle is kept in sync.
	configurationName string
	// enabled serves the webhook. Disable it only when the ValidatingWebhookConfiguration is not installed, such
	// as when running the controller from a development machine.
	enabled bool
}

func (o *admissionWebhookOptions) addFlags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&o.enabled, "admission-webhook", true,
		"Serve the validating admission webhook. The controller generates, rotates, and injects its certificate. "+
			"Disable only when the ValidatingWebhookConfiguration is not installed, "+
			"such as when running from a development machine.")
	cmd.Flags().StringVar(&o.certDir, "webhook-cert-dir", filepath.Join("/tmp", "k8s-webhook-server", "serving-certs"),
		"Directory the webhook server reads its certificate from; mount the --webhook-cert-secret Secret here.")
	cmd.Flags().StringVar(&o.secretName, "webhook-cert-secret", "promoter-webhook-server-cert",
		"Secret in the controller namespace holding the webhook CA and serving certificate.")
	cmd.Flags().StringVar(&o.serviceName, "webhook-service", "promoter-webhook-service",
		"Service in the controller namespace that fronts the webhook server.")
	cmd.Flags().StringVar(&o.configurationName, "webhook-configuration", "promoter-validating-webhook-configuration",
		"ValidatingWebhookConfiguration whose caBundle the controller keeps current.")
}

// setupAdmissionWebhook adds the certificate rotator to mgr and registers the webhooks once the certificate is on
// disk: the webhook server reads its certificate when it starts, so it must not start before. Every replica
// rotates and serves (the rotator does not need leader election). The returned readiness check fails until the
// webhook server has started, so the Service routes only to replicas that can answer.
func setupAdmissionWebhook(
	ctx context.Context,
	mgr ctrl.Manager,
	server webhook.Server,
	opts admissionWebhookOptions,
	namespace string,
	templates *resourcetemplate.Validator,
	isConfiguredKind func(schema.GroupVersionKind) bool,
) (healthz.Checker, error) {
	certsReady := make(chan struct{})
	if err := rotator.AddRotator(mgr, &rotator.CertRotator{
		SecretKey:      types.NamespacedName{Namespace: namespace, Name: opts.secretName},
		CertDir:        opts.certDir,
		CAName:         "gitops-promoter-webhook-ca",
		CAOrganization: "gitops-promoter",
		DNSName:        fmt.Sprintf("%s.%s.svc", opts.serviceName, namespace),
		ExtraDNSNames:  []string{fmt.Sprintf("%s.%s.svc.cluster.local", opts.serviceName, namespace)},
		IsReady:        certsReady,
		Webhooks:       []rotator.WebhookInfo{{Name: opts.configurationName, Type: rotator.Validating}},
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
		setupLog.Info("webhook certificate ready, registering admission webhooks")
		validator := &webhookv1alpha1.KubernetesResourceCommitStatusValidator{
			Templates:        templates,
			IsConfiguredKind: isConfiguredKind,
		}
		if err := validator.SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to register the KubernetesResourceCommitStatus webhook")
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
