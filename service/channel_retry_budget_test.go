package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestChannelRetryBudgetTracksRouteAndGlobalBudget(t *testing.T) {
	setupChannelHealthTest(t)
	for i := 0; i < 50; i++ {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		RecordChannelPrimaryRequestFor(ctx, "gpt-test", "/v1/chat/completions")
		ctx, _ = gin.CreateTestContext(httptest.NewRecorder())
		RecordChannelPrimaryRequestFor(ctx, "other-model", "/v1/chat/completions")
	}
	for i := 0; i < 10; i++ {
		require.True(t, AllowChannelRetryFor(nil, "gpt-test", "/v1/chat/completions", ChannelFailureTransient, 1))
	}
	for i := 0; i < 10; i++ {
		require.True(t, AllowChannelRetryFor(nil, "other-model", "/v1/chat/completions", ChannelFailureTransient, 2))
	}
	for i := 0; i < 2; i++ {
		require.True(t, AllowChannelRetryFor(nil, "gpt-test", "/v1/chat/completions", ChannelFailureTransient, 1))
	}
	for i := 0; i < 10; i++ {
		if !AllowChannelRetryFor(nil, "gpt-test", "/v1/chat/completions", ChannelFailureTransient, 1) {
			return
		}
	}
	t.Fatal("expected route retry budget to be exhausted")
}

func TestChannelRetryBudgetHasSmallBurstAndExpires(t *testing.T) {
	now := setupChannelHealthTest(t)
	for i := 0; i < channelRetryBudgetMinimumBurst; i++ {
		require.True(t, AllowChannelRetryFor(nil, "gpt-test", "/v1/chat/completions", ChannelFailureTransient, 1))
	}
	for i := 0; i < 2; i++ {
		require.True(t, AllowChannelRetryFor(nil, "gpt-test", "/v1/chat/completions", ChannelFailureTransient, 1))
	}
	require.False(t, AllowChannelRetryFor(nil, "gpt-test", "/v1/chat/completions", ChannelFailureTransient, 1))

	*now = now.Add(channelRetryBudgetWindow + time.Second)
	require.True(t, AllowChannelRetryFor(nil, "gpt-test", "/v1/chat/completions", ChannelFailureTransient, 1))
}
