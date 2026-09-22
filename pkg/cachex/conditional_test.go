package cachex

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

func TestConditionalCacheProtectsChangedBinding(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			cfg := HybridCacheConfig[int]{Namespace: "conditional-test", RedisCodec: IntCodec{}}
			var srv *miniredis.Miniredis
			if backend == "redis" {
				srv = miniredis.RunT(t)
				cfg.Redis = redis.NewClient(&redis.Options{Addr: srv.Addr()})
				t.Cleanup(func() { require.NoError(t, cfg.Redis.Close()) })
			}
			cache := NewHybridCache(cfg)
			require.NoError(t, cache.SetWithTTL("binding", 1, time.Minute))
			updated, err := cache.SetIfUnchangedWithTTL("binding", 1, true, 2, time.Minute)
			require.NoError(t, err)
			require.True(t, updated)
			updated, err = cache.SetIfUnchangedWithTTL("binding", 1, true, 3, time.Minute)
			require.NoError(t, err)
			require.False(t, updated)
			deleted, err := cache.DeleteIfUnchanged("binding", 1)
			require.NoError(t, err)
			require.False(t, deleted)
			current, found, err := cache.Get("binding")
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, 2, current)
			var winners atomic.Int32
			var wg sync.WaitGroup
			for i := range 20 {
				wg.Go(func() {
					changed, e := cache.SetIfUnchangedWithTTL("binding", 2, true, i+10, time.Minute)
					if e != nil {
						t.Error(e)
					}
					if changed {
						winners.Add(1)
					}
				})
			}
			wg.Wait()
			require.Equal(t, int32(1), winners.Load())
			current, _, err = cache.Get("binding")
			require.NoError(t, err)
			deleted, err = cache.DeleteIfUnchanged("binding", current)
			require.NoError(t, err)
			require.True(t, deleted)
			updated, err = cache.SetIfUnchangedWithTTL("binding", 0, false, 7, 30*time.Millisecond)
			require.NoError(t, err)
			require.True(t, updated)
			if srv != nil {
				srv.FastForward(time.Second)
			}
			require.Eventually(t, func() bool {
				_, found, _ := cache.Get("binding")
				return !found
			}, time.Second, 10*time.Millisecond)
		})
	}
}
