package demo

import (
	"context"
	"embed"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/go-github/v71/github"
)

//go:embed manifests/*.yaml
var manifestsFS embed.FS

// Replacements holds all the placeholder values
type Replacements struct {
	RepoName   string
	Username   string
	AppID      string
	PrivateKey string
}

// UploadManifests uploads manifest files to a GitHub repository with placeholder replacements
func UploadManifests(
	ctx context.Context,
	client *github.Client,
	repo *github.Repository,
	replacements Replacements,
) error {
	owner := repo.GetOwner().GetLogin()
	repoName := repo.GetName()

	placeholders := map[string]string{
		"<REPO_NAME>":           replacements.RepoName,
		"<GITHUB_ORG_USERNAME>": replacements.Username,
		"<your-app-id>":         replacements.AppID,
		"<your-private-key>":    replacements.PrivateKey,
	}

	entries, err := manifestsFS.ReadDir("manifests")
	if err != nil {
		return fmt.Errorf("failed to read manifests directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()
		filePath := filepath.Join("manifests", filename)

		content, err := manifestsFS.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", filename, err)
		}

		contentStr := string(content)
		for placeholder, value := range placeholders {
			contentStr = strings.ReplaceAll(contentStr, placeholder, value)
		}

		targetPath := "helm-guestbook-promotion/" + filename
		fmt.Printf("Creating %s...\n", targetPath)

		// Check if file already exists
		existingFile, _, _, _ := client.Repositories.GetContents(ctx, owner, repoName, targetPath, nil)

		opts := &github.RepositoryContentFileOptions{
			Message: github.Ptr("Add " + filename),
			Content: []byte(contentStr),
		}

		if existingFile != nil {
			// File exists - update it
			opts.SHA = existingFile.SHA
			_, _, err = client.Repositories.UpdateFile(ctx, owner, repoName, targetPath, opts)
			if err != nil {
				return fmt.Errorf("failed to update %s: %w", targetPath, err)
			}
			fmt.Printf("Updated %s ✓\n", targetPath)
		} else {
			// File doesn't exist - create it
			_, _, err = client.Repositories.CreateFile(ctx, owner, repoName, targetPath, opts)
			if err != nil {
				return fmt.Errorf("failed to create %s: %w", targetPath, err)
			}
			fmt.Printf("Created %s ✓\n", targetPath)
		}
	}

	return nil
}
