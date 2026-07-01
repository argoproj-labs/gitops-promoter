package settings

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInstanceIDsEqual(t *testing.T) {
	t.Parallel()

	wave0 := "wave-0"
	wave1 := "wave-1"

	require.True(t, InstanceIDsEqual(nil, nil))
	require.False(t, InstanceIDsEqual(nil, &wave0))
	require.False(t, InstanceIDsEqual(&wave0, nil))
	require.True(t, InstanceIDsEqual(&wave0, &wave0))
	require.False(t, InstanceIDsEqual(&wave0, &wave1))
}
