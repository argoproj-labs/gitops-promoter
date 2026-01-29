package demo

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"strings"

	"github.com/google/go-github/v71/github"
)

//go:embed all:helm_guestbook
var helmGuestbookFS embed.FS

// CopyEmbeddedDirToRepo copies the embedded helm_guestbook directory to a GitHub repository
func CopyEmbeddedDirToRepo(
	ctx context.Context,
	client *github.Client,
	destOwner, destRepo, destPath string,
) error {
	err := fs.WalkDir(helmGuestbookFS, "helm_guestbook", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk error at %s: %w", path, walkErr)
		}
		if d.IsDir() {
			return nil
		}

		content, readErr := helmGuestbookFS.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("failed to read embedded file %s: %w", path, readErr)
		}

		// Convert path: "helm_guestbook/Chart.yaml" → "helm-guestbook/Chart.yaml"
		relativePath := strings.TrimPrefix(path, "helm_guestbook/")
		destFilePath := destPath + "/" + relativePath

		// Check if file already exists
		existing, _, _, _ := client.Repositories.GetContents(ctx, destOwner, destRepo, destFilePath, nil)

		opts := &github.RepositoryContentFileOptions{
			Content: content,
		}

		var opErr error
		if existing != nil {
			// File exists - update it
			opts.Message = github.Ptr("Update " + destFilePath)
			opts.SHA = existing.SHA
			_, _, opErr = client.Repositories.UpdateFile(ctx, destOwner, destRepo, destFilePath, opts)
		} else {
			// File doesn't exist - create it
			opts.Message = github.Ptr("Add " + destFilePath)
			_, _, opErr = client.Repositories.CreateFile(ctx, destOwner, destRepo, destFilePath, opts)
		}

		if opErr != nil {
			return fmt.Errorf("failed to create/update %s: %w", destFilePath, opErr)
		}

		fmt.Printf("✓ %s\n", destFilePath)
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to walk embedded directory: %w", err)
	}
	return nil
}
