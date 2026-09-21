package service

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPreviousRecoveryStageCompletionsCannotPromoteNextStage(t *testing.T) {
	now := setupChannelHealthTest(t)
	identity := buildChannelHealthIdentity(nil, 77, "model", "/v1/responses", *now)
	shard := channelHealthShardFor(identity.RouteKey)
	shard.Lock()
	state := getRouteHealthStateLocked(shard, identity, *now)
	startRouteRecoveryLocked(state, *now)
	state.Capacity = 4
	state.RecoveryTargetCapacity = 16
	shard.Unlock()
	stale, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.True(t, AllowChannelCircuitAttempt(stale, 77, "model", "/v1/responses"))
	staleFailure, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.True(t, AllowChannelCircuitAttempt(staleFailure, 77, "model", "/v1/responses"))
	*now = now.Add(channelHealthRecoveryStageDuration)
	for range channelHealthRecoverySuccessTarget {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		require.True(t, AllowChannelCircuitAttempt(c, 77, "model", "/v1/responses"))
		RecordChannelCircuitSuccess(c, 77, "model", "/v1/responses")
	}
	require.Equal(t, 8, routeStateForTest(77, "model", "/v1/responses").Capacity)
	RecordChannelCircuitSuccess(stale, 77, "model", "/v1/responses")
	RecordChannelCircuitFailure(staleFailure, 77, "model", "/v1/responses", ChannelFailureTransient)
	require.True(t, routeStateForTest(77, "model", "/v1/responses").OpenUntil.IsZero())
	require.Zero(t, routeStateForTest(77, "model", "/v1/responses").RecoverySuccesses)
	require.Zero(t, routeStateForTest(77, "model", "/v1/responses").InFlight)
}

func TestCanceledTextStreamCannotAdvanceRecovery(t *testing.T) {
	now := setupChannelHealthTest(t)
	identity := buildChannelHealthIdentity(nil, 77, "model", "/v1/responses", *now)
	shard := channelHealthShardFor(identity.RouteKey)
	shard.Lock()
	startRouteRecoveryLocked(getRouteHealthStateLocked(shard, identity, *now), *now)
	shard.Unlock()
	*now = now.Add(channelHealthRecoveryStageDuration)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{}`)).WithContext(parent)
	require.True(t, AllowChannelCircuitAttempt(c, 77, "model", "/v1/responses"))
	cancel()
	RecordChannelCircuitSuccess(c, 77, "model", "/v1/responses")
	state := routeStateForTest(77, "model", "/v1/responses")
	require.Zero(t, state.RecoverySuccesses)
	require.Zero(t, state.InFlight)
	require.Equal(t, 1, state.Capacity)
}

func TestImageProbeLeaseDoesNotHideDueTextWork(t *testing.T) {
	setupChannelHealthTest(t)
	scheduleRouteProbeForTest(t, nil, 71, "image-model", "/v1/images/generations", ChannelFailureTransient)
	scheduleRouteProbeForTest(t, nil, 72, "text-model", "/v1/responses", ChannelFailureTransient)
	require.True(t, HasDueChannelHealthProbeForImages(true))
	images := ClaimDueImageChannelHealthProbes(1)
	require.Len(t, images, 1)
	require.False(t, HasDueChannelHealthProbeForImages(true))
	require.True(t, HasDueChannelHealthProbeForImages(false))
	texts := ClaimDueStandardChannelHealthProbes(1)
	require.Len(t, texts, 1)
	CompleteChannelHealthProbe(texts[0], ChannelHealthProbeResult{Success: true})
	require.False(t, HasDueChannelHealthProbeForImages(false))
	// The image probe remains leased and does not participate in text completion.
	require.Empty(t, ClaimDueImageChannelHealthProbes(1))
}
