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

	return cmd.Run()
}
