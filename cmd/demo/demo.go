package demo

import (
	"context"
	"fmt"
	"strings"

	"github.com/fatih/color"
	"github.com/google/go-github/v71/github"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	ctrl "sigs.k8s.io/controller-runtime"
)

var setupLog = ctrl.Log.WithName("setup")

// ArgoCDConfig holds ArgoCD configuration
type ArgoCDConfig struct {
	Upstream string `yaml:"upstream"`
}

// GitOpsPromoterConfig holds GitOps Promoter configuration
type GitOpsPromoterConfig struct {
	Upstream string `yaml:"upstream"`
}

// Config structure for config.yaml
type Config struct {
	ArgoCD         ArgoCDConfig         `yaml:"argocd"`
	GitOpsPromoter GitOpsPromoterConfig `yaml:"gitops-promoter"`
}

// NewDemoCommand creates a new demo command for setting up a gitops-promoter demo repository
func NewDemoCommand() *cobra.Command {
	var repoName string
	var private bool

	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Setup a new gitops-promoter demo repository",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			// Prompt for credentials
			credentials, err := NewInteractivePrompter().GetCredentials()
			if err != nil {
				return fmt.Errorf("failed to prompt for credentials: %w", err)
			}

			ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: credentials.Token})
			tc := oauth2.NewClient(ctx, ts)
			client := github.NewClient(tc)

			// Get current user
			user, _, err := client.Users.Get(ctx, "")
			if err != nil {
				return fmt.Errorf("failed to get current user: %w", err)
			}
			username := user.GetLogin()
			color.Green("Current github user: %s\n", username)

			if err := setupCluster(ctx); err != nil {
				return fmt.Errorf("failed to setup cluster: %w", err)
			}

			// 3. Create the repository
			color.Green("Creating repository %s/%s...\n", username, repoName)
			repo, _, err := client.Repositories.Create(ctx, "", &github.Repository{
				Name:        github.Ptr(repoName),
				Description: github.Ptr("GitOps Promoter demo repository"),
				Private:     github.Ptr(private),
				AutoInit:    github.Ptr(true), // Creates with README
			})
			if err != nil {
				// Check if repo already exists
				if !strings.Contains(err.Error(), "already exists") {
					return fmt.Errorf("failed to create repository: %w", err)
				}
				setupLog.Info("Repository already exists, fetching...")
				repo, _, err = client.Repositories.Get(ctx, username, repoName)
				if err != nil {
					return fmt.Errorf("failed to get existing repository: %w", err)
				}
			}

			color.Green("Repository available at: %s\n", repo.GetHTMLURL())

			// 4. Create files in the repo
			if err := UploadManifests(ctx, client, repo, Replacements{
				RepoName: repoName,
				Username: username,
				AppID:    credentials.AppID,
			}); err != nil {
				return fmt.Errorf("failed to upload manifests: %w", err)
			}

			// Create promotion strategy secret
			k8sClient, err := createK8sClient()
			if err != nil {
				return fmt.Errorf("failed to create k8s client: %w", err)
			}

			// create helm-guestbook-ps ns
			err = CreateNamespace(ctx, k8sClient, "helm-guestbook-ps")
			if err != nil {
				return fmt.Errorf("failed to create namespace: %w", err)
			}

			secretData := map[string]string{"githubAppPrivateKey": credentials.PrivateKey}
			err = CreateOrUpdateSecret(
				ctx, k8sClient, "helm-guestbook-ps", "github-demo-secret",
				secretData, map[string]string{},
			)
			if err != nil {
				return fmt.Errorf("failed to create promotion strategy github app secret: %w", err)
			}

			// Create repo secrets
			if err := CreateRepoSecrets(ctx, k8sClient, credentials, username, repoName); err != nil {
				return err
			}
			color.Green("Repo secrets created!")

			// Create base app
			if err := ApplyBaseApp(ctx); err != nil {
				return err
			}
			color.Green("Base app applied!")

			// Copy helm-guestbook directory to the repo
			err = CopyEmbeddedDirToRepo(ctx, client, username, repoName, "helm-guestbook")
			if err != nil {
				return fmt.Errorf("failed to copy directory: %w", err)
			}

			color.Green("Setup complete!")
			return nil

			// Update requeue interval

		},
	}

	cmd.Flags().StringVar(&repoName, "name", "gitops-promoter-examples", "Name of the repository to create")
	cmd.Flags().BoolVar(&private, "private", true, "Create a private repository")

	return cmd
}
