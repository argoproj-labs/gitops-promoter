package demo

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/goccy/go-yaml"
)

//go:embed app/app.yaml
var appYAML []byte

//go:embed config/requeue-duration.yaml
var controllerConfigYAML []byte

// Installer handles cluster setup operations
type Installer struct {
	config     Config
	configPath string
}

// NewInstaller creates a new Installer with the given config path
func NewInstaller(configPath string) (*Installer, error) {
	content, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(content, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return &Installer{
		config:     config,
		configPath: configPath,
	}, nil
}

// SetupCluster runs the full cluster setup
func (i *Installer) SetupCluster(ctx context.Context) error {
	if err := i.InstallArgoCD(ctx); err != nil {
		return fmt.Errorf("failed to install ArgoCD: %w", err)
	}

	if err := i.InstallGitOpsPromoter(ctx); err != nil {
		return fmt.Errorf("failed to install GitOps Promoter: %w", err)
	}

	if err := i.PatchArgoCD(ctx); err != nil {
		return fmt.Errorf("failed to patch ArgoCD: %w", err)
	}

	if err := i.PatchControllerConfiguration(ctx); err != nil {
		return fmt.Errorf("failed to patch controller configuration: %w", err)
	}

	return nil
}

// InstallArgoCD installs ArgoCD into the cluster
func (i *Installer) InstallArgoCD(ctx context.Context) error {
	if err := i.ensureNamespace(ctx, "argocd"); err != nil {
		return err
	}

	color.Green("Installing ArgoCD from %s...\n", i.config.ArgoCD.Upstream)
	return i.kubectlApplyURL(ctx, i.config.ArgoCD.Upstream, "argocd", true, true)
}

// InstallGitOpsPromoter installs the GitOps Promoter controller
func (i *Installer) InstallGitOpsPromoter(ctx context.Context) error {
	if err := i.ensureNamespace(ctx, "promoter-system"); err != nil {
		return err
	}

	// Download and extract CRDs first
	color.Green("Installing GitOps Promoter CRDs...")
	if err := i.installCRDsFromURL(ctx, i.config.GitOpsPromoter.Upstream); err != nil {
		return fmt.Errorf("failed to install CRDs: %w", err)
	}

	color.Green("Installing GitOps Promoter from %s...\n", i.config.GitOpsPromoter.Upstream)
	if err := i.kubectlApplyURL(ctx, i.config.GitOpsPromoter.Upstream, "", true, true); err != nil {
		return err
	}

	color.Green("GitOps Promoter installed ✓")
	return nil
}

// installCRDsFromURL downloads a manifest URL and applies only CRD resources
func (i *Installer) installCRDsFromURL(ctx context.Context, url string) error {
	// Download the manifest
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download manifest: %w", err)
	}
	if resp == nil {
		return errors.New("received nil response from server")
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			setupLog.Error(closeErr, "failed to close response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download manifest: status %s", resp.Status)
	}

	output, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read manifest: %w", err)
	}

	// Split into individual YAML documents and filter CRDs
	var crds []string
	docs := strings.Split(string(output), "\n---")
	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}
		// Check if this document is a CRD
		if strings.Contains(doc, "kind: CustomResourceDefinition") {
			crds = append(crds, doc)
		}
	}

	if len(crds) == 0 {
		return errors.New("no CRDs found in manifest")
	}

	// Apply CRDs with server-side apply
	crdManifest := strings.Join(crds, "\n---\n")
	args := []string{"apply", "--server-side", "-f", "-"}
	kubectlCmd := exec.CommandContext(ctx, "kubectl", args...)
	kubectlCmd.Stdin = strings.NewReader(crdManifest)
	kubectlCmd.Stdout = os.Stdout
	kubectlCmd.Stderr = os.Stderr

	if err := kubectlCmd.Run(); err != nil {
		return fmt.Errorf("kubectl apply CRDs failed: %w", err)
	}

	color.Green("CRDs installed ✓")
	return nil
}

// PatchArgoCD patches the ArgoCD server deployment
func (i *Installer) PatchArgoCD(ctx context.Context) error {
	patchFile := "cmd/demo/config/argocd-extension.yaml"
	args := []string{"patch", "deployment", "argocd-server", "-n", "argocd", "--patch-file", patchFile}

	if err := i.runKubectl(ctx, args...); err != nil {
		return fmt.Errorf("kubectl patch failed: %w", err)
	}

	color.Green("Argo CD patched successfully ✓")
	return nil
}

// PatchControllerConfiguration patches the ControllerConfiguration resource in the cluster.
func (i *Installer) PatchControllerConfiguration(ctx context.Context) error {
	args := []string{
		"patch", "controllerconfiguration", "promoter-controller-configuration",
		"-n", "promoter-system",
		"--type", "merge",
		"--patch", string(controllerConfigYAML),
	}
	if err := i.runKubectl(ctx, args...); err != nil {
		return fmt.Errorf("failed to patch controller configuration: %w", err)
	}
	color.Green("Controller configuration patched ✓")
	return nil
}

// ApplyBaseApp applies the embedded ArgoCD base application
func (i *Installer) ApplyBaseApp(ctx context.Context, githubUser string, repoName string) error {
	modifiedYAML := strings.ReplaceAll(string(appYAML), "<GITHUB_ORG_USERNAME>", githubUser)
	modifiedYAML = strings.ReplaceAll(modifiedYAML, "<REPO_NAME>", repoName)
	return i.kubectlApplyManifest(ctx, modifiedYAML, "argocd")
}

// RefreshApp forces a hard refresh on an ArgoCD application
func (i *Installer) RefreshApp(ctx context.Context, appName string) error {
	args := []string{
		"annotate", "application", appName,
		"-n", "argocd",
		"argocd.argoproj.io/refresh=hard",
		"--overwrite",
	}

	if err := i.runKubectl(ctx, args...); err != nil {
		return fmt.Errorf("failed to refresh app %s: %w", appName, err)
	}

	color.Green("Application %s refreshed ✓", appName)
	return nil
}

// RefreshAllApps refreshes all ArgoCD applications in the argocd namespace
func (i *Installer) RefreshAllApps(ctx context.Context) error {
	// Get all application names
	cmd := exec.CommandContext(ctx, "kubectl", "get", "applications", "-n",
		"argocd", "-o", "jsonpath={.items[*].metadata.name}")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to list applications: %w", err)
	}

	appNames := strings.Fields(string(output))
	for _, appName := range appNames {
		if err := i.RefreshApp(ctx, appName); err != nil {
			return err
		}
	}

	return nil
}

// PortForward starts port forwarding with automatic restart on failure
func (i *Installer) PortForward(ctx context.Context) error {
	color.Green("Starting port forwards...")
	color.Yellow("ArgoCD UI:         https://localhost:8000")
	color.Yellow("  kubectl -n argocd get secret argocd-initial-admin-secret -o go-template=" +
		"'{{.data.password | base64decode}}'")
	color.Yellow("Press Ctrl+C to stop\n")

	// Context for cancellation
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Start port-forward with auto-restart
	go i.runPortForwardWithRetry(ctx, "argocd-server", "argocd", "8000:443")

	// Wait for interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	color.Yellow("\nShutting down port forwards...")
	cancel() // This will kill the port-forward commands

	return nil
}

func (i *Installer) runPortForwardWithRetry(ctx context.Context, svc, namespace, ports string) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			cmd := exec.CommandContext(ctx, "kubectl", "port-forward", "svc/"+svc, "-n", namespace, ports)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr

			color.Cyan("Starting port-forward: %s/%s -> %s", namespace, svc, ports)
			err := cmd.Run()

			if ctx.Err() != nil {
				return // Context cancelled, exit
			}

			if err != nil {
				color.Red("Port-forward %s failed: %v, restarting in 2s...", svc, err)
				time.Sleep(2 * time.Second)
			}
		}
	}
}

// --- Private helpers ---

func (i *Installer) ensureNamespace(ctx context.Context, namespace string) error {
	if namespace == "" {
		return nil
	}

	setupLog.Info("Ensuring namespace exists", "namespace", namespace)

	// Check if exists
	if err := exec.CommandContext(ctx, "kubectl", "get", "namespace", namespace).Run(); err == nil {
		setupLog.Info("Namespace already exists", "namespace", namespace)
		return nil
	}

	// Create
	if err := i.runKubectl(ctx, "create", "namespace", namespace); err != nil {
		return fmt.Errorf("failed to create namespace %s: %w", namespace, err)
	}

	setupLog.Info("Namespace created", "namespace", namespace)
	return nil
}

func (i *Installer) runKubectl(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kubectl %v failed: %w", args, err)
	}
	return nil
}

func (i *Installer) kubectlApplyURL(
	ctx context.Context, url, namespace string, serverSide, forceConflicts bool,
) error {
	args := []string{"apply"}
	if serverSide {
		args = append(args, "--server-side")
	}
	if forceConflicts {
		args = append(args, "--force-conflicts")
	}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	args = append(args, "-f", url)

	return i.runKubectl(ctx, args...)
}

func (i *Installer) kubectlApplyManifest(ctx context.Context, manifest, namespace string) error {
	args := []string{"apply", "-f", "-"}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdin = strings.NewReader(manifest)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kubectl %v failed: %w", args, err)
	}
	return nil
}
