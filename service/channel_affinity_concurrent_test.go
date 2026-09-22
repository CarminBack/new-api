package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/samber/hot"
	"github.com/stretchr/testify/require"
)

func TestAffinityLateFallbackCannotOverwriteOrDeleteRecoveredBinding(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			setupChannelHealthTest(t)
			high, backup := failoverChannels(t)
			oldCache := getChannelAffinityCache()
			cfg := cachex.HybridCacheConfig[int]{Namespace: "new-api:channel_affinity:v1", RedisCodec: cachex.IntCodec{}, Memory: func() *hot.HotCache[string, int] { return hot.NewHotCache[string, int](hot.LRU, 128).Build() }}
			if backend == "redis" {
				srv := miniredis.RunT(t)
				cfg.Redis = redis.NewClient(&redis.Options{Addr: srv.Addr()})
				t.Cleanup(func() { cfg.Redis.Close() })
			}
			channelAffinityCache = cachex.NewHybridCache(cfg)
			t.Cleanup(func() { channelAffinityCache = oldCache })
			settings := operation_setting.GetChannelAffinitySetting()
			previous := *settings
			*settings = operation_setting.ChannelAffinitySetting{Enabled: true, DefaultTTLSeconds: 60, SwitchOnSuccess: true, Rules: []operation_setting.ChannelAffinityRule{{Name: "concurrent", ModelRegex: []string{".*"}, SessionMode: "prefer", IncludeRuleName: true, IncludeUsingGroup: true, KeySources: []operation_setting.ChannelAffinityKeySource{{Type: "request_header", Key: "X-Session"}}}}}
			t.Cleanup(func() { *settings = previous })
			request := func() *gin.Context {
				c := failoverContext()
				c.Request.Header.Set("X-Session", t.Name())
				GetPreferredChannelByAffinity(c, "gpt-test", "default")
				return c
			}
			seed := request()
			RecordChannelAffinity(seed, backup.Id)
			slow, fast := request(), request()
			RecordChannelAffinity(fast, high.Id)
			RecordChannelAffinity(slow, backup.Id)
			require.False(t, ClearCurrentChannelAffinityCache(slow), "late failure must preserve a newer binding")
			current, found := GetPreferredChannelByAffinity(request(), "gpt-test", "default")
			require.True(t, found)
			require.Equal(t, high.Id, current)
			// A failure of the binding actually observed by this request must still
			// permit failover; protecting late writers must not pin a broken primary.
			actualFailure := request()
			require.True(t, ClearCurrentChannelAffinityCache(actualFailure))
			RecordChannelAffinity(actualFailure, backup.Id)
			current, found = GetPreferredChannelByAffinity(request(), "gpt-test", "default")
			require.True(t, found)
			require.Equal(t, backup.Id, current)
		})
	}
}

func TestTextRetryNeedsTimeRemaining(t *testing.T) {
	old := common.TextRetryMinRemainingSeconds
	common.TextRetryMinRemainingSeconds = 5
	t.Cleanup(func() { common.TextRetryMinRemainingSeconds = old })
	for _, tc := range []struct {
		remaining time.Duration
		retry     bool
	}{{time.Second, false}, {time.Minute, true}, {0, true}} {
		c := failoverContext()
		if tc.remaining > 0 {
			ctx, cancel := context.WithTimeout(c.Request.Context(), tc.remaining)
			defer cancel()
			c.Request = c.Request.WithContext(ctx)
		}
		failure := types.NewErrorWithStatusCode(context.DeadlineExceeded, types.ErrorCodeDoRequestFailed, http.StatusBadGateway)
		d := DecideChannelFailureForModel(c, failure, "gpt-test", 3, false, true)
		require.Equal(t, tc.retry, d.Retry)
		if !tc.retry {
			require.Contains(t, d.Reason, "insufficient_time_remaining")
			require.True(t, d.CountForCircuit)
		}
	}
}
