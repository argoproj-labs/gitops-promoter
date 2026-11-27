package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
)

const twoContextKubeconfig = `apiVersion: v1
kind: Config
current-context: first
clusters:
- name: first
  cluster:
    server: https://first.example.com
- name: second
  cluster:
    server: https://second.example.com
users:
- name: user
  user:
    token: dummy
contexts:
- name: first
  context:
    cluster: first
    user: user
    namespace: first-ns
- name: second
  context:
    cluster: second
    user: user
    namespace: second-ns
`

const otherKubeconfig = `apiVersion: v1
kind: Config
current-context: other
clusters:
- name: other
  cluster:
    server: https://other.example.com
users:
- name: user
  user:
    token: dummy
contexts:
- name: other
  context:
    cluster: other
    user: user
    namespace: other-ns
`

//nolint:paralleltest // t.Setenv sets KUBECONFIG process-wide.
func TestControllerRESTConfigHonorsKubectlFlags(t *testing.T) {
	dir := t.TempDir()
	explicit := filepath.Join(dir, "explicit")
	other := filepath.Join(dir, "other")
	if err := os.WriteFile(explicit, []byte(twoContextKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte(otherKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	// KUBECONFIG points elsewhere, so the flags must win.
	t.Setenv("KUBECONFIG", other)

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	clientConfig := addKubectlFlags(flags)
	if err := flags.Parse([]string{"--kubeconfig", explicit, "--context", "second"}); err != nil {
		t.Fatal(err)
	}

	restConfig, err := controllerRESTConfig(clientConfig)
	if err != nil {
		t.Fatalf("controllerRESTConfig: %v", err)
	}
	if restConfig.Host != "https://second.example.com" {
		t.Errorf("Host = %q, want %q", restConfig.Host, "https://second.example.com")
	}
	if restConfig.QPS != -1 {
		t.Errorf("QPS = %v, want -1 (client-side rate limiting disabled, as controller-runtime does)", restConfig.QPS)
	}

	namespace, _, err := clientConfig.Namespace()
	if err != nil {
		t.Fatal(err)
	}
	if namespace != "second-ns" {
		t.Errorf("namespace = %q, want %q", namespace, "second-ns")
	}
}
