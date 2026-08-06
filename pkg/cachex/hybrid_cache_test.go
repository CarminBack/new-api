package cachex

import (
	"testing"
	"time"

	"github.com/samber/hot"
	"github.com/stretchr/testify/require"
)

func TestSetIfUnchangedWithTTLRejectsStaleWriter(t *testing.T) {
	cache := NewHybridCache[int](HybridCacheConfig[int]{
		Namespace: "test:conditional",
		Memory: func() *hot.HotCache[string, int] {
			return hot.NewHotCache[string, int](hot.LRU, 10).Build()
		},
	})
	require.NoError(t, cache.SetWithTTL("affinity", 2, time.Minute))

	updated, err := cache.SetIfUnchangedWithTTL("affinity", 2, true, 1, time.Minute)
	require.NoError(t, err)
	require.True(t, updated)

	updated, err = cache.SetIfUnchangedWithTTL("affinity", 2, true, 3, time.Minute)
	require.NoError(t, err)
	require.False(t, updated)
	value, found, err := cache.Get("affinity")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, 1, value)
}
