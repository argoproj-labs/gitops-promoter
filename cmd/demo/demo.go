package demo

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/google/go-github/v71/github"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	"golang.org/x/term"
)

// Config structure for config.yaml
type Config struct {
	ArgoCD struct {
		Upstream string `yaml:"upstream"`
	} `yaml:"argocd"`
	GitOpsPromoter struct {
		Upstream string `yaml:"upstream"`
	} `yaml:"gitops-promoter"`
}

func NewDemoCommand() *cobra.Command {
	var repoName string
	var private bool

	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Setup a new gitops-promoter demo repository",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			// Prompt for token (hidden input)
			token, err := promptForToken()
			if err != nil {
				return fmt.Errorf("failed to read token: %w", err)
			}

			// Prompt for Application ID
			appID, err := promptForAppID()
			if err != nil {
				return fmt.Errorf("failed to read app ID: %w", err)
			}
			fmt.Printf("Application ID: %s\n", appID)

			// Prompt for Installation ID
			// 0. Install ArgoCD
			if err := InstallArgoCD(ctx); err != nil {
				return fmt.Errorf("failed to install ArgoCD: %w", err)
			}

			// 1. Install GitOps Promoter
			if err := InstallGitOpsPromoter(ctx); err != nil {
				return fmt.Errorf("failed to install GitOps Promoter: %w", err)
			}

			// 2. Patch argocd to add extension
			if err := PatchArgoCD(ctx); err != nil {
				return fmt.Errorf("failed to patch ArgoCD: %w", err)
			}

			// 2. GitHub client
			ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
			tc := oauth2.NewClient(ctx, ts)
			client := github.NewClient(tc)

			// Get current user
			user, _, err := client.Users.Get(ctx, "")
			if err != nil {
				return fmt.Errorf("failed to get current user: %w", err)
			}
			username := user.GetLogin()
			fmt.Printf("Current github user: %s\n", username)

			// 3. Create the repository
			fmt.Printf("Creating repository %s/%s...\n", username, repoName)
			repo, _, err := client.Repositories.Create(ctx, "", &github.Repository{
				Name:        github.String(repoName),
				Description: github.String("GitOps Promoter demo repository"),
				Private:     github.Bool(private),
				AutoInit:    github.Bool(true), // Creates with README
			})
			if err != nil {
				// Check if repo already exists
				if strings.Contains(err.Error(), "already exists") {
					fmt.Println("Repository already exists, fetching...")
					repo, _, err = client.Repositories.Get(ctx, username, repoName)
					if err != nil {
						return fmt.Errorf("failed to get existing repository: %w", err)
					}
				} else {
					return fmt.Errorf("failed to create repository: %w", err)
				}
			}

			fmt.Printf("Repository available at: %s\n", repo.GetHTMLURL())

			// 4. Create files in the repo
			if err := UploadManifests(ctx, client, repo, Replacements{
				RepoName: repoName,
				Username: username,
				AppID:    appID,
			}); err != nil {
				return fmt.Errorf("failed to upload manifests: %w", err)
			}

			fmt.Println("Setup complete!")
			return nil
		},
	}

	cmd.Flags().StringVar(&repoName, "name", "gitops-promoter-examples", "Name of the repository to create")
	cmd.Flags().BoolVar(&private, "private", false, "Create a private repository")

	return cmd
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
