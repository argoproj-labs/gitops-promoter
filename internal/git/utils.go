package git

import (
	"github.com/go-logr/logr"
	"golang.org/x/net/context"
	"strings"
)

func ContainsYamlFileSuffix(ctx context.Context, files []string) bool {
	logger := logr.FromContextOrDiscard(ctx)
	for _, file := range files {
		if strings.HasSuffix(file, ".yaml") || strings.HasSuffix(file, ".yml") {
			logger.V(4).Info("YAML file changed", "file", file)
			return true
		}
	}
	return false
}
