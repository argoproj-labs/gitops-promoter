package demo

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/fatih/color"
	"github.com/google/go-github/v71/github"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
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

			// Prompt for credentials
			credentials, err := promptForCredentials()
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
				AppID:    credentials.AppID,
			}); err != nil {
				return fmt.Errorf("failed to upload manifests: %w", err)
			}

			// Create promotion strategy secret
			k8sClient, err := NewK8sClient()
			if err != nil {
				return fmt.Errorf("failed to create k8s client: %w", err)
			}

			// create helm-guestbook-ps ns
			err = CreateNamespace(ctx, k8sClient, "helm-guestbook-ps")
			if err != nil {
				return fmt.Errorf("failed to create namespace: %w", err)
			}

			// err = CreateOrUpdateSecret(ctx, k8sClient, "default", "github-demo-secret", map[string]string{"githubAppPrivateKey": credentials.PrivateKey}, map[string]string{})

			// if err != nil {
			// 	return fmt.Errorf("failed to create promotion strategy github app secret: %w", err)
			// }

			err = CreateOrUpdateSecret(ctx, k8sClient, "helm-guestbook-ps", "github-demo-secret", map[string]string{"githubAppPrivateKey": credentials.PrivateKey}, map[string]string{})

			if err != nil {
				return fmt.Errorf("failed to create promotion strategy github app secret: %w", err)
			}

			// Create a secret read and write for the hydrator
			data := map[string]string{"githubAppPrivateKey": credentials.PrivateKey, "githubAppID": credentials.AppID, "type": "git", "url": fmt.Sprintf("https://github.com/%s/%s", username, repoName)}
			labels := map[string]string{"argocd.argoproj.io/secret-type": "repository-write"}
			err = CreateOrUpdateSecret(ctx, k8sClient, "argocd", "repo-write-promoter", data, labels)
			if err != nil {
				return fmt.Errorf("failed to create repo write secret: %w", err)
			}

			// Create a secret read
			data = map[string]string{"password": credentials.Token, "type": "git", "url": fmt.Sprintf("https://github.com/%s/%s", username, repoName), "username": username}
			labels = map[string]string{"argocd.argoproj.io/secret-type": "repository"}
			err = CreateOrUpdateSecret(ctx, k8sClient, "argocd", "repo-read-promoter", data, labels)
			if err != nil {
				return fmt.Errorf("failed to create repo read secret: %w", err)
			}

			// Create base app
			err = KubectlApplyFile(ctx, "cmd/demo/app/app.yaml", "argocd")
			if err != nil {
				return fmt.Errorf("failed to apply apps.yaml: %w", err)
			}

			color.Green("Setup complete!")
			return nil
		},
	}

	cmd.Flags().StringVar(&repoName, "name", "gitops-promoter-examples", "Name of the repository to create")
	cmd.Flags().BoolVar(&private, "private", true, "Create a private repository")

	return cmd
}

// KubectlApplyFile applies a YAML file using kubectl
func KubectlApplyFile(ctx context.Context, filePath string, namespace string) error {
	args := []string{"apply", "-f", filePath}
	if namespace != "" {
		args = append([]string{"-n", namespace}, args...)
	}

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kubectl apply failed: %w", err)
	}

	return nil
}
