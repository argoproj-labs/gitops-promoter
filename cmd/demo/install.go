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

func setupCluster(ctx context.Context) error {
	// Install ArgoCD
	if err := InstallArgoCD(ctx); err != nil {
		return fmt.Errorf("failed to install ArgoCD: %w", err)
	}

	// Install GitOps Promoter
	if err := InstallGitOpsPromoter(ctx); err != nil {
		return fmt.Errorf("failed to install GitOps Promoter: %w", err)
	}

	// Patch ArgoCD to add extension
	if err := PatchArgoCD(ctx); err != nil {
		return fmt.Errorf("failed to patch ArgoCD: %w", err)
	}

	return nil
}

// InstallArgoCD installs ArgoCD into the cluster using the configured upstream manifest
func InstallArgoCD(ctx context.Context) error {
	// Read config file
	content, err := os.ReadFile("cmd/demo/config/config.yaml")
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	// Parse YAML
	var config Config
	if err := yaml.Unmarshal(content, &config); err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	if err := EnsureNamespace(ctx, "argocd"); err != nil {
		return fmt.Errorf("failed to ensure argocd namespace: %w", err)
	}

	url := config.ArgoCD.Upstream

	// Run kubectl apply
	color.Green("Installing ArgoCD from %s...\n", url)
	args := []string{"apply", "--server-side", "-n", "argocd", "-f", url}

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kubectl apply failed: %w", err)
	}

	color.Green("ArgoCD installed ✓")
	return nil
}

// InstallGitOpsPromoter installs the GitOps Promoter controller into the cluster
func InstallGitOpsPromoter(ctx context.Context) error {
	content, err := os.ReadFile("cmd/demo/config/config.yaml")
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(content, &config); err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	if err := EnsureNamespace(ctx, "gitops-promoter"); err != nil {
		return fmt.Errorf("failed to ensure gitops-promoter namespace: %w", err)
	}

	url := config.GitOpsPromoter.Upstream
	color.Green("Installing GitOps Promoter from %s...\n", url)
	args := []string{"apply", "-f", url}

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kubectl apply failed: %w", err)
	}

	color.Green("GitOps Promoter installed ✓")
	return nil
}

// EnsureNamespace creates a namespace if it doesn't already exist
func EnsureNamespace(ctx context.Context, namespace string) error {
	if namespace == "" {
		return nil
	}

	setupLog.Info("Ensuring namespace exists", "namespace", namespace)

	// Check if namespace exists
	checkCmd := exec.CommandContext(ctx, "kubectl", "get", "namespace", namespace)
	if err := checkCmd.Run(); err == nil {
		setupLog.Info("Namespace already exists", "namespace", namespace)
		return nil
	}

	// Create namespace
	createCmd := exec.CommandContext(ctx, "kubectl", "create", "namespace", namespace)
	createCmd.Stdout = os.Stdout
	createCmd.Stderr = os.Stderr

	if err := createCmd.Run(); err != nil {
		return fmt.Errorf("failed to create namespace %s: %w", namespace, err)
	}

	setupLog.Info("Namespace created", "namespace", namespace)
	return nil
}

// PatchArgoCD patches the ArgoCD server deployment to enable the extension
func PatchArgoCD(ctx context.Context) error {
	patch := "cmd/demo/config/argocd-extension.yaml"

	args := []string{"patch", "deployment", "argocd-server", "-n", "argocd", "--patch-file", patch}
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kubectl patch failed: %w", err)
	}

	color.Green("ArgoCD patched successfully ✓")
	return nil
}

// ApplyBaseApp applies the embedded ArgoCD base application
func ApplyBaseApp(ctx context.Context) error {
	if err := KubectlApply(ctx, string(appYAML), "argocd"); err != nil {
		return fmt.Errorf("failed to apply base app: %w", err)
	}
	return nil
}

// KubectlApply applies a YAML manifest string using kubectl
func KubectlApply(ctx context.Context, manifest string, namespace string) error {
	args := []string{"apply", "-f", "-"}
	if namespace != "" {
		args = append([]string{"-n", namespace}, args...)
	}

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdin = strings.NewReader(manifest)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kubectl apply failed: %w", err)
	}
	return nil
}
