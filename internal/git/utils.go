package git

import (
	"context"
	"strings"

	"github.com/go-logr/logr"
)

// containsYamlFileSuffix Check if the provided list of files contains any file with a .yaml or .yml suffix.
// TODO: This is temporary check we should add some path globbing support to the specs
func containsYamlFileSuffix(ctx context.Context, files []string) bool {
	logger := logr.FromContextOrDiscard(ctx)
	for _, file := range files {
		if strings.HasSuffix(file, ".yaml") || strings.HasSuffix(file, ".yml") {
			logger.V(4).Info("YAML file changed", "file", file)
			return true
		}
	}
	return false
}
