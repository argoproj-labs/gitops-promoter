package demo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func CreateOrUpdateSecret(ctx context.Context, clientset kubernetes.Interface, namespace, name string, data map[string]string, labels map[string]string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},

		Type:       corev1.SecretTypeOpaque,
		StringData: data,
	}

	// Try to get existing secret
	existing, err := clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		// Doesn't exist, create it
		_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create secret: %w", err)
		}
		fmt.Printf("Secret %s/%s created ✓\n", namespace, name)
	} else {
		// Exists, update it
		secret.ResourceVersion = existing.ResourceVersion
		_, err = clientset.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update secret: %w", err)
		}
		fmt.Printf("Secret %s/%s updated ✓\n", namespace, name)
	}

	return nil
}

func getKubeConfig() (*rest.Config, error) {
	// Try in-cluster config first (when running inside a pod)
	config, err := rest.InClusterConfig()
	if err == nil {
		return config, nil
	}

	// Fall back to kubeconfig file
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home, _ := os.UserHomeDir()
		kubeconfig = filepath.Join(home, ".kube", "config")
	}

	config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build config: %w", err)
	}

	return config, nil
}

func CreateNamespace(ctx context.Context, clientset kubernetes.Interface, namespace string) error {
	_, err := clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}, metav1.CreateOptions{})

	if err != nil {
		if errors.IsAlreadyExists(err) {
			// Namespace already exists, that's fine
			return nil
		}
		return fmt.Errorf("failed to create namespace %s: %w", namespace, err)
	}

	return nil
}

func createK8sClient() (kubernetes.Interface, error) {
	config, err := getKubeConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get kube config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}
	return clientset, nil
}
