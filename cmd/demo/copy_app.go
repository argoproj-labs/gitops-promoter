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

func CopyEmbeddedDirToRepo(
	ctx context.Context,
	client *github.Client,
	destOwner, destRepo, destPath string,
) error {
	return fs.WalkDir(helmGuestbookFS, "helm_guestbook", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		content, err := helmGuestbookFS.ReadFile(path)
		if err != nil {
			return err
		}

		// Convert path: "helm_guestbook/Chart.yaml" → "apps/helm-guestbook/Chart.yaml"
		relativePath := strings.TrimPrefix(path, "helm_guestbook/")
		destFilePath := destPath + "/" + relativePath

		_, _, err = client.Repositories.CreateFile(ctx, destOwner, destRepo, destFilePath, &github.RepositoryContentFileOptions{
			Message: github.Ptr("Add " + destFilePath),
			Content: content,
		})
		if err != nil {
			return fmt.Errorf("failed to create %s: %w", destFilePath, err)
		}

		fmt.Printf("✓ %s\n", destFilePath)
		return nil
	})
}
