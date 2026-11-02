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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
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
		// Only the elected controller-manager replica rotates. When leader election is
		// disabled, the manager runs every runnable, including this one.
		RequireLeaderElection: true,
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

// AddToManager registers serving-cert rotation on mgr. The rotator shares that manager,
// so its controller-runtime metrics are served on the controller's metrics endpoint and
// it follows the controller's leader election and shutdown. An error from the rotator
// stops the manager.
func AddToManager(mgr manager.Manager, opts *Options) error {
	certDir, err := markerCertDir()
	if err != nil {
		return err
	}
	if err := mgr.Add(markerDirCleanup{dir: certDir}); err != nil {
		_ = os.RemoveAll(certDir)
		return fmt.Errorf("failed to register cert rotation cleanup: %w", err)
	}
	// Direct client, not mgr.GetClient(). The controller cache partitions Secrets by
	// instance-id, strips their data, and makes writes wait on that cache.
	// cert-controller reads through its own cache and only needs a writer here.
	// FieldOwner attributes Secret data to FieldManager; the APIService caBundle
	// update passes the same owner explicitly.
	writer, err := client.New(mgr.GetConfig(), client.Options{
		Scheme:     mgr.GetScheme(),
		Mapper:     mgr.GetRESTMapper(),
		FieldOwner: FieldManager,
	})
	if err != nil {
		_ = os.RemoveAll(certDir)
		return fmt.Errorf("failed to create cert rotation client: %w", err)
	}
	if err := rotator.AddRotator(fieldOwnerManager{
		Manager: mgr,
		client:  writer,
	}, opts.newCertRotator(certDir)); err != nil {
		_ = os.RemoveAll(certDir)
		return fmt.Errorf("failed to add cert rotator: %w", err)
	}
	return nil
}

// fieldOwnerManager is mgr with GetClient replaced by the cert-rotation writer.
// cert-controller takes its writer from Manager.GetClient.
type fieldOwnerManager struct {
	manager.Manager
	client client.Client
}

func (m fieldOwnerManager) GetClient() client.Client { return m.client }

// markerDirCleanup removes the marker cert directory when the manager stops.
// It does not need leader election: every replica creates the directory at startup.
type markerDirCleanup struct {
	dir string
}

func (m markerDirCleanup) Start(ctx context.Context) error {
	<-ctx.Done()
	_ = os.RemoveAll(m.dir)
	return nil
}

func (m markerDirCleanup) NeedLeaderElection() bool { return false }
