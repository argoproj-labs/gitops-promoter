package demo

import (
	"bufio"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
	"k8s.io/client-go/kubernetes"
)

type Credentials struct {
	Token          string
	AppID          string
	PrivateKey     string
	PrivateKeyPath string
}

func promptForCredentials() (*Credentials, error) {
	// Prompt for token (hidden input)
	token, err := promptForToken()
	if err != nil {
		return nil, fmt.Errorf("failed to read token: %w", err)
	}

	// Prompt for Application ID
	appID, err := promptForAppID()
	if err != nil {
		return nil, fmt.Errorf("failed to read app ID: %w", err)
	}
	fmt.Printf("Application ID: %s\n", appID)

	// Prompt for private key path
	privateKeyPath, err := promptForPrivateKeyPath()
	if err != nil {
		return nil, fmt.Errorf("failed to read private key path: %w", err)
	}

	// Read the private key content
	privateKeyContent, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key: %w", err)
	}

	// Validate the PEM file
	if err := ValidatePrivateKeyPEM(privateKeyPath); err != nil {
		return nil, fmt.Errorf("invalid private key file: %w", err)
	}

	return &Credentials{
		Token:          token,
		AppID:          appID,
		PrivateKey:     string(privateKeyContent),
		PrivateKeyPath: privateKeyPath,
	}, nil
}

func promptForToken() (string, error) {
	fmt.Print("Enter your GitHub personal access token: ")

	// Read password without echoing
	tokenBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println() // newline after hidden input
	if err != nil {
		// Fallback for non-terminal (e.g., piped input)
		reader := bufio.NewReader(os.Stdin)
		token, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(token), nil
	}

	return string(tokenBytes), nil
}

func promptForAppID() (string, error) {
	fmt.Print("Enter your GitHub application ID: ")
	reader := bufio.NewReader(os.Stdin)
	appID, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(appID), nil
}

func promptForPrivateKeyPath() (string, error) {
	fmt.Print("Enter path to your GitHub App private key (.pem): ")
	reader := bufio.NewReader(os.Stdin)
	privateKeyPath, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read private key path: %w", err)
	}
	fmt.Printf("Private key path: %s\n", privateKeyPath)
	return strings.TrimSpace(privateKeyPath), nil
}

// NewK8sClient creates a Kubernetes clientset
func NewK8sClient() (*kubernetes.Clientset, error) {
	config, err := getKubeConfig()
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	return clientset, nil
}

func ValidatePrivateKeyPEM(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return fmt.Errorf("file does not contain valid PEM data")
	}

	// Check for expected types
	switch block.Type {
	case "RSA PRIVATE KEY":
		_, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		_, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		_, err = x509.ParseECPrivateKey(block.Bytes)
	default:
		return fmt.Errorf("unexpected PEM type: %s (expected private key)", block.Type)
	}

	if err != nil {
		return fmt.Errorf("failed to parse private key: %w", err)
	}

	return nil
}
