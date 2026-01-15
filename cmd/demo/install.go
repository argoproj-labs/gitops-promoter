package demo

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/fatih/color"
	"github.com/goccy/go-yaml"
)

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

	EnsureNamespace(ctx, "argocd")
	url := config.ArgoCD.Upstream
	// Run kubectl apply
	fmt.Printf("Installing ArgoCD from %s...\n", url)
	args := []string{"apply", "--server-side", "--force-conflicts", "-n", "argocd", "-f", url}

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kubectl apply failed: %w", err)
	}

	color.Green("ArgoCD installed ✓")
	return nil
}

func InstallGitOpsPromoter(ctx context.Context) error {
	content, err := os.ReadFile("cmd/demo/config/config.yaml")
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(content, &config); err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	EnsureNamespace(ctx, "gitops-promoter")
	url := config.GitOpsPromoter.Upstream
	fmt.Printf("Installing GitOps Promoter from %s...\n", url)
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

func EnsureNamespace(ctx context.Context, namespace string) error {
	if namespace == "" {
		return nil
	}

	fmt.Printf("Ensuring namespace %s exists...\n", namespace)

	// Check if namespace exists
	checkCmd := exec.CommandContext(ctx, "kubectl", "get", "namespace", namespace)
	if err := checkCmd.Run(); err == nil {
		fmt.Printf("Namespace %s already exists ✓\n", namespace)
		return nil
	}

	// Create namespace
	createCmd := exec.CommandContext(ctx, "kubectl", "create", "namespace", namespace)
	createCmd.Stdout = os.Stdout
	createCmd.Stderr = os.Stderr

	if err := createCmd.Run(); err != nil {
		return fmt.Errorf("failed to create namespace %s: %w", namespace, err)
	}

	fmt.Printf("Namespace %s created ✓\n", namespace)
	return nil
}

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
