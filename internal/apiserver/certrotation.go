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
	"errors"
	"fmt"
	"path/filepath"

	"github.com/open-policy-agent/cert-controller/pkg/rotator"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/types"
	clientrest "k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

const (
	defaultCertRotationSecretName  = "promoter-apiserver-serving-cert"
	defaultCertRotationServiceName = "promoter-apiserver"
	defaultCertRotationAPIService  = "v1alpha1.view.promoter.argoproj.io"
	certRotationControllerName     = "promoter-apiserver-cert-rotator"
)

// CertRotationOptions configures the built-in serving-cert management backed by
// github.com/open-policy-agent/cert-controller. When enabled, the apiserver generates a
// self-signed CA and serving certificate, stores them in a Secret, keeps them rotated,
// and injects the CA into the APIService .spec.caBundle. No cert-manager is required.
type CertRotationOptions struct {
	// SecretName is the Secret (in Namespace) holding the CA and serving cert. It must
	// already exist (an empty Secret is shipped in the install manifests) and must be
	// mounted into the pod at the directory of --tls-cert-file.
	SecretName string
	// Namespace is the namespace of the Secret and the apiserver Service. Defaults to the
	// namespace the apiserver runs in.
	Namespace string
	// ServiceName is the apiserver Service name, used to build the certificate DNS names.
	ServiceName string
	// APIServiceName is the APIService whose caBundle is kept in sync with the generated CA.
	APIServiceName string
	// Enabled turns on cert-controller based serving-cert management.
	Enabled bool
}

func newCertRotationOptions() CertRotationOptions {
	return CertRotationOptions{
		SecretName:     defaultCertRotationSecretName,
		ServiceName:    defaultCertRotationServiceName,
		APIServiceName: defaultCertRotationAPIService,
	}
}

// AddFlags registers the cert rotation flags.
func (o *CertRotationOptions) AddFlags(fs *pflag.FlagSet) {
	fs.BoolVar(&o.Enabled, "cert-rotation", o.Enabled,
		"Generate, rotate and inject the serving certificate with the built-in cert-controller. "+
			"The cert/key are written to the Secret --cert-rotation-secret-name, which must be mounted "+
			"at the directory of --tls-cert-file. Disable when providing certs yourself or via cert-manager.")
	fs.StringVar(&o.SecretName, "cert-rotation-secret-name", o.SecretName,
		"Name of the Secret the built-in cert-controller stores the CA and serving certificate in.")
	fs.StringVar(&o.Namespace, "cert-rotation-namespace", o.Namespace,
		"Namespace of the serving-cert Secret and apiserver Service. Defaults to the apiserver's namespace.")
	fs.StringVar(&o.ServiceName, "cert-rotation-service-name", o.ServiceName,
		"Name of the apiserver Service; used to build the serving certificate DNS names.")
	fs.StringVar(&o.APIServiceName, "cert-rotation-apiservice-name", o.APIServiceName,
		"Name of the APIService whose caBundle is kept in sync with the generated CA.")
}

// Validate validates the cert rotation options against the configured serving cert files.
func (o *CertRotationOptions) Validate(certFile, keyFile string) error {
	if !o.Enabled {
		return nil
	}
	var errs []error
	if o.SecretName == "" {
		errs = append(errs, errors.New("--cert-rotation-secret-name must be set when --cert-rotation is enabled"))
	}
	if o.Namespace == "" {
		errs = append(errs, errors.New("--cert-rotation-namespace must be set when --cert-rotation is enabled"))
	}
	if o.ServiceName == "" {
		errs = append(errs, errors.New("--cert-rotation-service-name must be set when --cert-rotation is enabled"))
	}
	if o.APIServiceName == "" {
		errs = append(errs, errors.New("--cert-rotation-apiservice-name must be set when --cert-rotation is enabled"))
	}
	if certFile == "" || keyFile == "" {
		errs = append(errs, errors.New("--tls-cert-file and --tls-private-key-file must be set when --cert-rotation is enabled"))
	} else if filepath.Dir(certFile) != filepath.Dir(keyFile) {
		errs = append(errs, errors.New("--tls-cert-file and --tls-private-key-file must be in the same directory when --cert-rotation is enabled"))
	}
	return errors.Join(errs...)
}

// newCertRotator builds the cert-controller CertRotator for the given serving cert files.
func (o *CertRotationOptions) newCertRotator(certFile, keyFile string) *rotator.CertRotator {
	dnsName := fmt.Sprintf("%s.%s.svc", o.ServiceName, o.Namespace)
	return &rotator.CertRotator{
		SecretKey: types.NamespacedName{
			Namespace: o.Namespace,
			Name:      o.SecretName,
		},
		CertDir:        filepath.Dir(certFile),
		CertName:       filepath.Base(certFile),
		KeyName:        filepath.Base(keyFile),
		CAName:         "promoter-apiserver-ca",
		CAOrganization: "gitops-promoter",
		DNSName:        dnsName,
		ExtraDNSNames: []string{
			fmt.Sprintf("%s.%s", o.ServiceName, o.Namespace),
			dnsName + ".cluster.local",
		},
		IsReady: make(chan struct{}),
		Webhooks: []rotator.WebhookInfo{{
			Name: o.APIServiceName,
			Type: rotator.APIService,
		}},
		FieldOwner:     certRotationControllerName,
		ControllerName: certRotationControllerName,
		// The generic apiserver reloads --tls-cert-file/--tls-private-key-file from disk
		// when they change, so no restart is needed on rotation.
		RestartOnSecretRefresh: false,
		// Every replica runs its own rotator; Secret/APIService updates use optimistic
		// concurrency, so concurrent rotators converge on a single CA.
		RequireLeaderElection: false,
	}
}

// startCertRotation starts a minimal controller-runtime manager running the cert-controller
// rotator. It returns a channel that is closed once the serving cert has been written to disk
// and the CA has been injected into the APIService, and a channel that receives the manager's
// exit error (nil on clean shutdown when ctx is cancelled).
func startCertRotation(ctx context.Context, restConfig *clientrest.Config, opts *CertRotationOptions, certFile, keyFile string) (<-chan struct{}, <-chan error, error) {
	mgr, err := manager.New(restConfig, manager.Options{
		Scheme: utils.GetScheme(),
		// The rotator only needs a client and its own namespaced cache; disable the
		// manager's metrics and health endpoints so they don't collide with the apiserver.
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{opts.Namespace: {}},
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create cert rotation manager: %w", err)
	}

	cr := opts.newCertRotator(certFile, keyFile)
	if err := rotator.AddRotator(mgr, cr); err != nil {
		return nil, nil, fmt.Errorf("failed to add cert rotator: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		if err := mgr.Start(ctx); err != nil {
			errCh <- fmt.Errorf("cert rotation manager stopped: %w", err)
			return
		}
		errCh <- nil
	}()
	return cr.IsReady, errCh, nil
}
