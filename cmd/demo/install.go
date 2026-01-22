package demo

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"strings"

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
	return i.kubectlApplyURL(ctx, i.config.ArgoCD.Upstream, "argocd", true)
}

// InstallGitOpsPromoter installs the GitOps Promoter controller
func (i *Installer) InstallGitOpsPromoter(ctx context.Context) error {
	if err := i.ensureNamespace(ctx, "promoter-system"); err != nil {
		return err
	}

	color.Green("Installing GitOps Promoter from %s...\n", i.config.GitOpsPromoter.Upstream)
	if err := i.kubectlApplyURL(ctx, i.config.GitOpsPromoter.Upstream, "", false); err != nil {
		return err
	}

	color.Green("GitOps Promoter installed ✓")
	return nil
}

// PatchArgoCD patches the ArgoCD server deployment
func (i *Installer) PatchArgoCD(ctx context.Context) error {
	patchFile := "cmd/demo/config/argocd-extension.yaml"
	args := []string{"patch", "deployment", "argocd-server", "-n", "argocd", "--patch-file", patchFile}

	if err := i.runKubectl(ctx, args...); err != nil {
		return fmt.Errorf("kubectl patch failed: %w", err)
	}

	color.Green("ArgoCD patched successfully ✓")
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
func (i *Installer) ApplyBaseApp(ctx context.Context) error {
	return i.kubectlApplyManifest(ctx, string(appYAML), "argocd")
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

func (i *Installer) kubectlApplyURL(ctx context.Context, url, namespace string, serverSide bool) error {
	args := []string{"apply"}
	if serverSide {
		args = append(args, "--server-side")
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
