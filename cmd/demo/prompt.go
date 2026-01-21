package demo

import (
	"bufio"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/goccy/go-yaml"
)

// Credentials holds all user-provided authentication info
type Credentials struct {
	Token          string
	AppID          string
	PrivateKey     string
	PrivateKeyPath string
}

// CredentialsProvider defines the interface for obtaining credentials
type CredentialsProvider interface {
	GetCredentials() (*Credentials, error)
}

// InteractivePrompter prompts the user for credentials via terminal
type InteractivePrompter struct {
	reader io.Reader
	writer io.Writer
}

// NewInteractivePrompter creates a new interactive prompter
func NewInteractivePrompter() *InteractivePrompter {
	return &InteractivePrompter{
		reader: os.Stdin,
		writer: os.Stdout,
	}
}

// WithReader sets a custom reader (useful for testing)
func (p *InteractivePrompter) WithReader(r io.Reader) *InteractivePrompter {
	p.reader = r
	return p
}

// WithWriter sets a custom writer (useful for testing)
func (p *InteractivePrompter) WithWriter(w io.Writer) *InteractivePrompter {
	p.writer = w
	return p
}

// GetCredentials implements CredentialsProvider
func (p *InteractivePrompter) GetCredentials() (*Credentials, error) {
	token, err := p.promptHidden("Enter your GitHub personal access token: ")
	if err != nil {
		return nil, fmt.Errorf("failed to read token: %w", err)
	}

	appID, err := p.prompt("Enter your GitHub application ID: ")
	if err != nil {
		return nil, fmt.Errorf("failed to read app ID: %w", err)
	}
	_, _ = fmt.Fprintf(p.writer, "Application ID: %s\n", appID)

	privateKeyPath, err := p.prompt("Enter path to your GitHub App private key (.pem): ")
	if err != nil {
		return nil, fmt.Errorf("failed to read private key path: %w", err)
	}

	// Validate and read private key
	validator := &PEMValidator{}
	if err := validator.Validate(privateKeyPath); err != nil {
		return nil, fmt.Errorf("invalid private key file: %w", err)
	}

	privateKeyContent, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key: %w", err)
	}

	return &Credentials{
		Token:          token,
		AppID:          appID,
		PrivateKey:     string(privateKeyContent),
		PrivateKeyPath: privateKeyPath,
	}, nil
}

func (p *InteractivePrompter) prompt(message string) (string, error) {
	_, _ = fmt.Fprint(p.writer, message)
	reader := bufio.NewReader(p.reader)
	input, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read input: %w", err)
	}
	return strings.TrimSpace(input), nil
}

func (p *InteractivePrompter) promptHidden(message string) (string, error) {
	var password string
	prompt := &survey.Password{
		Message: message,
	}
	err := survey.AskOne(prompt, &password)
	if err != nil {
		return "", fmt.Errorf("failed to read password: %w", err)
	}
	return password, nil
}

// FileCredentialsProvider loads credentials from a config file
type FileCredentialsProvider struct {
	configPath string
}

// NewFileCredentialsProvider creates a provider that loads from a file
func NewFileCredentialsProvider(path string) *FileCredentialsProvider {
	return &FileCredentialsProvider{configPath: path}
}

// GetCredentials implements CredentialsProvider
func (p *FileCredentialsProvider) GetCredentials() (*Credentials, error) {
	// Load from YAML file (implement based on your config format)
	return loadCredentialsFromFile(p.configPath)
}

// PEMValidator validates PEM files
type PEMValidator struct{}

// Validate checks if a file contains a valid private key
func (v *PEMValidator) Validate(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return errors.New("file does not contain valid PEM data")
	}

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

func loadCredentialsFromFile(path string) (*Credentials, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var credentials Credentials
	if err := yaml.Unmarshal(content, &credentials); err != nil {
		return nil, fmt.Errorf("failed to parse credentials: %w", err)
	}

	return &credentials, nil
}
