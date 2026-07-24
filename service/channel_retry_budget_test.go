package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestChannelRetryBudgetTracksTenPercentOfPrimaryTraffic(t *testing.T) {
	setupChannelHealthTest(t)
	for i := 0; i < 100; i++ {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		RecordChannelPrimaryRequest(ctx)
		RecordChannelPrimaryRequest(ctx)
	}
	for i := 0; i < 10; i++ {
		require.True(t, AllowChannelRetry())
	}
	require.False(t, AllowChannelRetry())
}

func TestChannelRetryBudgetHasSmallBurstAndExpires(t *testing.T) {
	now := setupChannelHealthTest(t)
	for i := 0; i < channelRetryBudgetMinimumBurst; i++ {
		require.True(t, AllowChannelRetry())
	}
	require.False(t, AllowChannelRetry())

	*now = now.Add(channelRetryBudgetWindow + time.Second)
	require.True(t, AllowChannelRetry())
}
