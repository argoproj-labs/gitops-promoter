package fake

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPortFromEnv_override(t *testing.T) {
	t.Setenv("PROMOTER_API_METRICS_GIT_PORT", "5103")
	require.Equal(t, 5103, portFromEnv("PROMOTER_API_METRICS_GIT_PORT", 5001))
}

func TestPortFromEnv_fallback(t *testing.T) {
	t.Setenv("PROMOTER_API_METRICS_GIT_PORT", "")
	require.Equal(t, 5001, portFromEnv("PROMOTER_API_METRICS_GIT_PORT", 5001))
}

func TestPortFromEnv_invalid(t *testing.T) {
	t.Setenv("PROMOTER_API_METRICS_GIT_PORT", "nope")
	require.Equal(t, 5001, portFromEnv("PROMOTER_API_METRICS_GIT_PORT", 5001))
}
