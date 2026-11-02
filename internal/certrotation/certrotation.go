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

// Package certrotation issues and rotates the dashboard aggregation apiserver's serving
// certificate with github.com/open-policy-agent/cert-controller.
//
// It runs in the controller-manager, not in the apiserver: the controller-manager already
// reads Secrets, so watching the serving-cert Secret gives it no new read access, and the
// apiserver itself needs no Secret permissions at all. The apiserver only mounts the
// Secret and serves the files (see the apiserver's --wait-for-serving-cert flag).
package certrotation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/open-policy-agent/cert-controller/pkg/rotator"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/types"
	clientrest "k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

const (
	// DefaultSecretName is the Secret holding the dashboard apiserver's CA and serving cert.
	DefaultSecretName = "promoter-apiserver-serving-cert"
	// DefaultServiceName is the dashboard apiserver's Service.
	DefaultServiceName = "promoter-apiserver"
	// DefaultAPIServiceName is the dashboard's APIService.
	DefaultAPIServiceName = "v1alpha1.view.promoter.argoproj.io"
	// FieldManager is the field manager of every field the rotator writes (the Secret's
	// .data and the APIService's .spec.caBundle).
	FieldManager = "promoter-apiserver-cert-rotator"

	certName = "tls.crt"
	keyName  = "tls.key"
)

// Options configures serving-cert management for the dashboard aggregation apiserver.
type Options struct {
	// SecretName is the Secret (in Namespace) holding the CA and serving cert. It must
	// already exist (an empty Secret is shipped in the install manifests); the apiserver
	// mounts it.
	SecretName string
	// Namespace is the namespace of the Secret and the apiserver Service. The controller
	// command sets this from its own namespace.
	Namespace string
	// ServiceName is the apiserver Service name, used to build the certificate DNS names.
	ServiceName string
	// APIServiceName is the APIService whose caBundle is kept in sync with the generated CA.
	APIServiceName string
	// Enabled turns on serving-cert management.
	Enabled bool
}

// NewOptions returns Options with the defaults used by the install manifests.
func NewOptions() Options {
	return Options{
		SecretName:     DefaultSecretName,
		ServiceName:    DefaultServiceName,
		APIServiceName: DefaultAPIServiceName,
	}
}

// AddFlags registers the controller flags for dashboard apiserver serving-cert management.
func (o *Options) AddFlags(fs *pflag.FlagSet) {
	fs.BoolVar(&o.Enabled, "apiserver-cert-rotation", o.Enabled,
		"Generate, rotate and inject the dashboard aggregation apiserver's serving certificate with the "+
			"built-in cert-controller. The CA and cert are written to --apiserver-cert-rotation-secret-name "+
			"and the CA is injected into the APIService. Leave disabled when the serving cert comes from "+
			"cert-manager or is provided by you.")
	fs.StringVar(&o.SecretName, "apiserver-cert-rotation-secret-name", o.SecretName,
		"Name of the Secret the dashboard apiserver's CA and serving certificate are stored in.")
	fs.StringVar(&o.ServiceName, "apiserver-cert-rotation-service-name", o.ServiceName,
		"Name of the dashboard apiserver Service; used to build the serving certificate DNS names.")
	fs.StringVar(&o.APIServiceName, "apiserver-cert-rotation-apiservice-name", o.APIServiceName,
		"Name of the APIService whose caBundle is kept in sync with the generated CA.")
}

// Validate validates the options.
func (o *Options) Validate() error {
	if !o.Enabled {
		return nil
	}
	var errs []error
	if o.SecretName == "" {
		errs = append(errs, errors.New("--apiserver-cert-rotation-secret-name must be set when --apiserver-cert-rotation is enabled"))
	}
	if o.Namespace == "" {
		errs = append(errs, errors.New("apiserver cert rotation namespace must be set when --apiserver-cert-rotation is enabled"))
	}
	if o.ServiceName == "" {
		errs = append(errs, errors.New("--apiserver-cert-rotation-service-name must be set when --apiserver-cert-rotation is enabled"))
	}
	if o.APIServiceName == "" {
		errs = append(errs, errors.New("--apiserver-cert-rotation-apiservice-name must be set when --apiserver-cert-rotation is enabled"))
	}
	return errors.Join(errs...)
}

// newCertRotator builds the cert-controller CertRotator. certDir is the directory
// cert-controller checks for the cert files (see markerCertDir).
func (o *Options) newCertRotator(certDir string) *rotator.CertRotator {
	dnsName := fmt.Sprintf("%s.%s.svc", o.ServiceName, o.Namespace)
	return &rotator.CertRotator{
		SecretKey: types.NamespacedName{
			Namespace: o.Namespace,
			Name:      o.SecretName,
		},
		CertDir:        certDir,
		CertName:       certName,
		KeyName:        keyName,
		CAName:         "promoter-apiserver-ca",
		CAOrganization: "gitops-promoter",
		DNSName:        dnsName,
		ExtraDNSNames: []string{
			fmt.Sprintf("%s.%s", o.ServiceName, o.Namespace),
			dnsName + ".cluster.local",
		},
		// Closed by cert-controller once the CA has been injected; must be non-nil.
		IsReady: make(chan struct{}),
		Webhooks: []rotator.WebhookInfo{{
			Name: o.APIServiceName,
			Type: rotator.APIService,
		}},
		FieldOwner:     FieldManager,
		ControllerName: FieldManager,
		// The apiserver reloads its cert files from the mounted Secret when they change, so
		// nothing needs to restart on rotation.
		RestartOnSecretRefresh: false,
		// Run only inside the elected controller-manager (see Runnable); this manager has
		// no leader election of its own.
		RequireLeaderElection: false,
	}
}

// markerCertDir creates a directory holding empty files named like the cert and key.
//
// cert-controller is designed to run in the pod that serves the certificate: before it
// reports ready it waits for the cert file to appear in CertDir (the mounted Secret), and
// it stops with an error if it never does. The controller-manager does not serve the
// certificate and does not mount the Secret, so it points cert-controller at this
// directory instead. Nothing reads these files.
func markerCertDir() (string, error) {
	dir, err := os.MkdirTemp("", "promoter-apiserver-cert-rotation-")
	if err != nil {
		return "", fmt.Errorf("failed to create cert rotation marker directory: %w", err)
	}
	for _, name := range []string{certName, keyName} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			return "", fmt.Errorf("failed to create cert rotation marker file: %w", err)
		}
	}
	return dir, nil
}

// Start runs a minimal controller-runtime manager with the cert-controller rotator until
// ctx is cancelled. It returns a channel that receives the manager's exit error (nil on a
// clean shutdown).
func Start(ctx context.Context, restConfig *clientrest.Config, opts *Options) (<-chan error, error) {
	mgr, err := manager.New(restConfig, manager.Options{
		Scheme: utils.GetScheme(),
		// The rotator only needs a client and its own namespaced cache; disable the
		// manager's metrics and health endpoints so they don't collide with the controller's.
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
		// Attribute every write (Secret data and APIService caBundle) to a stable field
		// manager, so GitOps tools using server-side apply see these fields as owned by
		// the rotator and leave them alone.
		Client: client.Options{FieldOwner: FieldManager},
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{opts.Namespace: {}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create cert rotation manager: %w", err)
	}

	certDir, err := markerCertDir()
	if err != nil {
		return nil, err
	}
	if err := rotator.AddRotator(mgr, opts.newCertRotator(certDir)); err != nil {
		return nil, fmt.Errorf("failed to add cert rotator: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		defer func() { _ = os.RemoveAll(certDir) }()
		if err := mgr.Start(ctx); err != nil {
			errCh <- fmt.Errorf("cert rotation manager stopped: %w", err)
			return
		}
		errCh <- nil
	}()
	return errCh, nil
}

// Runnable returns a controller-runtime Runnable that runs cert rotation for as long as
// the manager it is added to runs. It needs leader election, so with --leader-elect only the
// elected controller-manager replica rotates. If cert rotation stops with an error, the
// runnable returns it, which stops the controller-manager.
func Runnable(restConfig *clientrest.Config, opts *Options) manager.Runnable {
	return manager.RunnableFunc(func(ctx context.Context) error {
		errCh, err := Start(ctx, restConfig, opts)
		if err != nil {
			return err
		}
		return <-errCh
	})
}
