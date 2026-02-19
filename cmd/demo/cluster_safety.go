package demo

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	allowRemoteEnvVar = "GITOPS_PROMOTER_DEMO_ALLOW_REMOTE"
)

// localClusterPatterns defines patterns that indicate a local development cluster
var localClusterPatterns = []string{
	"kind-",
	"minikube",
	"docker-desktop",
	"docker-for-desktop",
	"rancher-desktop",
	"k3d-",
	"colima",
	"localhost",
	"127.0.0.1",
}

// clusterInfo holds information about the current Kubernetes cluster
type clusterInfo struct {
	context string
	isLocal bool
}

// detectCluster checks if the current Kubernetes context appears to be a local development cluster
func detectCluster() (*clusterInfo, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		&clientcmd.ConfigOverrides{},
	).RawConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	currentContext := config.CurrentContext
	if currentContext == "" {
		return nil, errors.New("no current context set")
	}

	info := &clusterInfo{context: currentContext}

	// Check context name
	if matchesLocalPattern(currentContext) {
		info.isLocal = true
		return info, nil
	}

	// Check cluster server URL
	if ctx, ok := config.Contexts[currentContext]; ok {
		if cluster, ok := config.Clusters[ctx.Cluster]; ok {
			if matchesLocalPattern(cluster.Server) {
				info.isLocal = true
				return info, nil
			}
		}
	}

	return info, nil
}

// matchesLocalPattern checks if the given string matches any local cluster pattern
func matchesLocalPattern(s string) bool {
	lower := strings.ToLower(s)
	for _, pattern := range localClusterPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// validateClusterSafety ensures the CLI is running against a local cluster
func validateClusterSafety() error {
	info, err := detectCluster()
	if err != nil {
		return fmt.Errorf("failed to check cluster: %w", err)
	}

	if info.isLocal {
		color.Green("✓ Local cluster detected: %s\n", info.context)
		return nil
	}

	return handleNonLocalCluster(info.context)
}

// handleNonLocalCluster warns about non-local clusters and checks for override
func handleNonLocalCluster(context string) error {
	color.Red(`
⚠️  WARNING: Non-local cluster detected!
Current context: %s

This demo CLI is intended for local development clusters only.
Detected contexts like: kind-*, minikube, docker-desktop, k3d-*

To proceed anyway, set environment variable:
  export %s=true
`, context, allowRemoteEnvVar)

	if os.Getenv(allowRemoteEnvVar) != "true" {
		return errors.New("refusing to run on non-local cluster")
	}

	color.Yellow("⚠️  Proceeding with remote cluster (override enabled)\n")
	return nil
}
