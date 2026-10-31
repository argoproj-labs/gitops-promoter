package exprcache

import (
	"sync"
	"testing"

	"github.com/expr-lang/expr"
	"github.com/stretchr/testify/require"
)

func TestGetOrCompileCachesByKey(t *testing.T) {
	t.Parallel()
	c := New(2)

	p1, err := c.GetOrCompile(Key{Prefix: "a", Expression: "1 + 1"})
	require.NoError(t, err)
	p2, err := c.GetOrCompile(Key{Prefix: "a", Expression: "1 + 1"})
	require.NoError(t, err)
	require.Same(t, p1, p2, "same key returns the cached program")

	p3, err := c.GetOrCompile(Key{Prefix: "b", Expression: "1 + 1"}, expr.AsInt())
	require.NoError(t, err)
	require.NotSame(t, p1, p3, "prefix separates entries compiled with different options")

	_, err = c.GetOrCompile(Key{Prefix: "a", Expression: "1 +"})
	require.ErrorContains(t, err, "failed to compile")
}

func TestLRUEviction(t *testing.T) {
	t.Parallel()
	c := New(2)
	_, err := c.GetOrCompile(Key{Prefix: "p", Expression: "1"})
	require.NoError(t, err)
	_, err = c.GetOrCompile(Key{Prefix: "p", Expression: "2"})
	require.NoError(t, err)
	_, err = c.GetOrCompile(Key{Prefix: "p", Expression: "3"})
	require.NoError(t, err)

	_, hit := c.Get(Key{Prefix: "p", Expression: "1"})
	require.False(t, hit, "least recently used entry is evicted")
	_, hit = c.Get(Key{Prefix: "p", Expression: "3"})
	require.True(t, hit)
}

func TestNewDefaultsCapacity(t *testing.T) {
	t.Parallel()
	require.NotNil(t, New(0))
	require.NotNil(t, New(-1))
}

func TestConcurrentAccess(t *testing.T) {
	t.Parallel()
	c := New(4)
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := Key{Prefix: "p", Expression: []string{"1", "2", "3", "4", "5"}[i%5]}
			_, err := c.GetOrCompile(key)
			require.NoError(t, err)
			c.Get(key)
		}()
	}
	wg.Wait()
}
