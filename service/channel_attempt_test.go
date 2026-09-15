package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConsumeLogUseTimeSecondsExcludesFailedChannels(t *testing.T) {
	originalNow := channelCircuitNow
	t.Cleanup(func() { channelCircuitNow = originalNow })
	start := time.Unix(1700000000, 0)
	now := start
	channelCircuitNow = func() time.Time { return now }
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	BeginChannelRouteAttempt(ctx, 1, 0)
	now = start.Add(30 * time.Second)
	FinishChannelRouteAttempt(ctx, 503, ChannelFailureDecision{Retry: true})
	// Local routing delay and the failed attempt must not enter the successful duration.
	now = now.Add(2 * time.Second)
	BeginChannelRouteAttempt(ctx, 2, 0)
	now = now.Add(10 * time.Second)
	assert.Equal(t, int64(10), consumeLogUseTimeSeconds(ctx, start))
	attempts := GetChannelRouteAttempts(ctx, true)
	require.Len(t, attempts, 2)
	assert.Equal(t, int64(30000), attempts[0].DurationMS)
	assert.Equal(t, int64(10000), attempts[1].DurationMS)
}

func TestConsumeLogUseTimeSecondsCurrentAttempt(t *testing.T) {
	originalNow := channelCircuitNow
	t.Cleanup(func() { channelCircuitNow = originalNow })
	start := time.Unix(1700000000, 0)
	now := start
	channelCircuitNow = func() time.Time { return now }
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	BeginChannelRouteAttempt(ctx, 1, 0)
	now = start.Add(1900 * time.Millisecond)
	assert.Equal(t, int64(1), consumeLogUseTimeSeconds(ctx, start))
	// Retrying the same channel/key also measures only the latest attempt.
	FinishChannelRouteAttempt(ctx, 503, ChannelFailureDecision{Retry: true})
	BeginChannelRouteAttempt(ctx, 1, 0)
	now = now.Add(500 * time.Millisecond)
	assert.Zero(t, consumeLogUseTimeSeconds(ctx, start))
}

func TestConsumeLogUseTimeSecondsWithoutTracking(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	start := time.Now().Add(-10 * time.Second)
	before := time.Now().Unix() - start.Unix()
	actual := consumeLogUseTimeSeconds(ctx, start)
	after := time.Now().Unix() - start.Unix()
	assert.GreaterOrEqual(t, actual, before)
	assert.LessOrEqual(t, actual, after)
}
