package service

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func resetMemoryChannelCircuitsForTest() {
	resetMemoryChannelHealth()
}

func setupChannelHealthTest(t *testing.T) *time.Time {
	t.Helper()
	gin.SetMode(gin.TestMode)
	originalNow := channelCircuitNow
	originalPersistenceEnabled := channelHealthPersistenceEnabled
	now := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	channelCircuitNow = func() time.Time { return now }
	channelHealthPersistenceEnabled = false
	resetMemoryChannelHealth()
	resetChannelRetryBudgetForTest()
	t.Cleanup(func() {
		channelCircuitNow = originalNow
		channelHealthPersistenceEnabled = originalPersistenceEnabled
		resetMemoryChannelHealth()
		resetChannelRetryBudgetForTest()
	})
	return &now
}

func recordLegacyRouteOutcome(t *testing.T, channelID int, modelName string, requestPath string, class ChannelFailureClass) {
	t.Helper()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.True(t, AllowChannelCircuitAttempt(ctx, channelID, modelName, requestPath))
	if class == "success" {
		RecordChannelCircuitSuccess(ctx, channelID, modelName, requestPath)
		return
	}
	RecordChannelCircuitFailure(ctx, channelID, modelName, requestPath, class)
}

func routeStateForTest(channelID int, modelName string, requestPath string) channelRouteHealthState {
	identity := buildChannelHealthIdentity(nil, channelID, modelName, requestPath, channelCircuitNow())
	shard := channelHealthShardFor(identity.RouteKey)
	shard.Lock()
	defer shard.Unlock()
	if state := shard.Routes[identity.RouteKey]; state != nil {
		return *state
	}
	return channelRouteHealthState{}
}

func aggregateStateForTest(channelID int) channelAggregateHealthState {
	identity := buildChannelHealthIdentity(nil, channelID, "", "", channelCircuitNow())
	shard := channelAggregateHealthShardFor(identity.ChannelKey)
	shard.Lock()
	defer shard.Unlock()
	if state := shard.States[identity.ChannelKey]; state != nil {
		return *state
	}
	return channelAggregateHealthState{}
}

func scheduleRouteProbeForTest(t *testing.T, channel *model.Channel, channelID int, modelName string, requestPath string, class ChannelFailureClass) {
	t.Helper()
	if channel != nil {
		channelID = channel.Id
	}
	for i := 0; i < channelHealthSuspectMinimumSamples-1; i++ {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		if channel == nil {
			require.True(t, AllowChannelCircuitAttempt(ctx, channelID, modelName, requestPath))
		} else {
			require.True(t, AllowChannelHealthAttempt(ctx, channel, modelName, requestPath))
		}
		RecordChannelCircuitFailure(ctx, channelID, modelName, requestPath, class)
	}
	identity := buildChannelHealthIdentity(channel, channelID, modelName, requestPath, channelCircuitNow())
	shard := channelHealthShardFor(identity.RouteKey)
	shard.Lock()
	shard.Routes[identity.RouteKey].BurstFailureStartedAt = channelCircuitNow().Add(-channelHealthSuspectMinimumDuration)
	shard.Unlock()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	if channel == nil {
		require.True(t, AllowChannelCircuitAttempt(ctx, channelID, modelName, requestPath))
	} else {
		require.True(t, AllowChannelHealthAttempt(ctx, channel, modelName, requestPath))
	}
	RecordChannelCircuitFailure(ctx, channelID, modelName, requestPath, class)
}

func confirmLegacyRouteFailureForTest(t *testing.T, channelID int, modelName string, requestPath string, class ChannelFailureClass) {
	t.Helper()
	scheduleRouteProbeForTest(t, nil, channelID, modelName, requestPath, class)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.True(t, AllowChannelCircuitAttempt(ctx, channelID, modelName, requestPath))
	ReleaseChannelCircuitProbe(ctx, channelID, modelName, requestPath)
	targets := ClaimDueChannelHealthProbes(10)
	require.Len(t, targets, 1)
	CompleteChannelHealthProbe(targets[0], ChannelHealthProbeResult{Class: class, Reason: "confirmed failure", StatusCode: http.StatusServiceUnavailable})
}

func TestSparseTimeoutsDoNotOpenBusyRoute(t *testing.T) {
	setupChannelHealthTest(t)
	for i := 0; i < 86; i++ {
		recordLegacyRouteOutcome(t, 29, "gpt-5.6-sol", "/v1/responses", "success")
	}
	for i := 0; i < 5; i++ {
		recordLegacyRouteOutcome(t, 29, "gpt-5.6-sol", "/v1/responses", ChannelFailureUncertain)
	}

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.True(t, AllowChannelCircuitAttempt(ctx, 29, "gpt-5.6-sol", "/v1/responses"))
	require.False(t, HasDueChannelHealthProbe())
	ReleaseChannelCircuitProbe(ctx, 29, "gpt-5.6-sol", "/v1/responses")
}

func TestSlowFailureTimerStartsAtFirstInfrastructureFailure(t *testing.T) {
	now := setupChannelHealthTest(t)
	*now = now.Add(24 * time.Hour)

	recordLegacyRouteOutcome(t, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	state := routeStateForTest(37, "gpt-test", "/v1/responses")
	require.Equal(t, *now, state.NoSuccessFailureAt)
	require.Equal(t, 1, state.FailuresSinceSuccess)
	require.False(t, HasDueChannelHealthProbe())

	*now = now.Add(channelHealthWindow - time.Millisecond)
	for i := 0; i < channelHealthSlowSingleRouteFailures-1; i++ {
		recordLegacyRouteOutcome(t, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	}
	require.False(t, HasDueChannelHealthProbe())

	*now = now.Add(time.Millisecond)
	recordLegacyRouteOutcome(t, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	require.True(t, HasDueChannelHealthProbe())
}

func TestAnyRealSuccessResetsChannelNoSuccessEvidence(t *testing.T) {
	now := setupChannelHealthTest(t)
	recordLegacyRouteOutcome(t, 29, "gpt-a", "/v1/responses", ChannelFailureTransient)
	recordLegacyRouteOutcome(t, 29, "gpt-b", "/v1/chat/completions", ChannelFailureUncertain)
	*now = now.Add(channelHealthWindow)

	recordLegacyRouteOutcome(t, 29, "gpt-c", "/v1/responses", "success")
	recordLegacyRouteOutcome(t, 29, "gpt-a", "/v1/responses", ChannelFailureTransient)
	recordLegacyRouteOutcome(t, 29, "gpt-b", "/v1/chat/completions", ChannelFailureTransient)
	recordLegacyRouteOutcome(t, 29, "gpt-a", "/v1/responses", ChannelFailureTransient)

	require.False(t, HasDueChannelHealthProbe())
	aggregate := aggregateStateForTest(29)
	require.Equal(t, 3, aggregate.FailuresSinceSuccess)
	require.Equal(t, *now, aggregate.NoSuccessFailureAt)
}

func TestMultiRouteNoSuccessSchedulesChannelProbe(t *testing.T) {
	now := setupChannelHealthTest(t)
	recordLegacyRouteOutcome(t, 29, "gpt-a", "/v1/responses", ChannelFailureTransient)
	recordLegacyRouteOutcome(t, 29, "gpt-b", "/v1/chat/completions", ChannelFailureUncertain)
	*now = now.Add(channelHealthWindow)
	recordLegacyRouteOutcome(t, 29, "gpt-a", "/v1/responses", ChannelFailureTransient)

	targets := ClaimDueChannelHealthProbes(10)
	require.Len(t, targets, 1)
	require.Equal(t, ChannelHealthProbeScopeChannel, targets[0].Scope)
	require.Equal(t, ChannelHealthProbeTypeInitial, targets[0].ProbeType)
}

func TestPoolAccountFailuresRequireSlowWindowBeforeRouteProbe(t *testing.T) {
	now := setupChannelHealthTest(t)
	recordLegacyRouteOutcome(t, 37, "gpt-test", "/v1/responses", ChannelFailurePoolAccount)
	for i := 0; i < channelHealthSlowSingleRouteFailures-2; i++ {
		recordLegacyRouteOutcome(t, 37, "gpt-test", "/v1/responses", ChannelFailurePoolAccount)
	}
	require.False(t, HasDueChannelHealthProbe())

	*now = now.Add(channelHealthWindow)
	recordLegacyRouteOutcome(t, 37, "gpt-test", "/v1/responses", ChannelFailurePoolAccount)
	targets := ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)
	require.Equal(t, ChannelHealthProbeScopeRoute, targets[0].Scope)

	CompleteChannelHealthProbe(targets[0], ChannelHealthProbeResult{
		Class:      ChannelFailurePoolAccount,
		Reason:     "insufficient account balance",
		StatusCode: http.StatusForbidden,
	})
	require.False(t, AllowChannelCircuitAttempt(nil, 37, "gpt-test", "/v1/responses"))
}

func TestPoolAccountFailuresAcrossRoutesScheduleChannelProbe(t *testing.T) {
	now := setupChannelHealthTest(t)
	recordLegacyRouteOutcome(t, 29, "gpt-a", "/v1/responses", ChannelFailurePoolAccount)
	recordLegacyRouteOutcome(t, 29, "gpt-b", "/v1/chat/completions", ChannelFailurePoolAccount)
	*now = now.Add(channelHealthWindow)
	recordLegacyRouteOutcome(t, 29, "gpt-a", "/v1/responses", ChannelFailurePoolAccount)

	targets := ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)
	require.Equal(t, ChannelHealthProbeScopeChannel, targets[0].Scope)
}

func TestPoolAccountVolumeDoesNotBypassSlowWindow(t *testing.T) {
	setupChannelHealthTest(t)
	for i := 0; i < channelHealthAggregateMinimumSamples; i++ {
		modelName := "gpt-a"
		requestPath := "/v1/responses"
		if i%2 == 1 {
			modelName = "gpt-b"
			requestPath = "/v1/chat/completions"
		}
		recordLegacyRouteOutcome(t, 29, modelName, requestPath, ChannelFailurePoolAccount)
	}
	recordLegacyRouteOutcome(t, 29, "gpt-c", "/v1/responses", ChannelFailureTransient)
	require.False(t, HasDueChannelHealthProbe())
}

func TestFiveFailuresDoNotOpenHighTrafficSingleRouteWithSuccesses(t *testing.T) {
	setupChannelHealthTest(t)
	for i := 0; i < 1000; i++ {
		recordLegacyRouteOutcome(t, 29, "gpt-test", "/v1/responses", "success")
	}
	for i := 0; i < channelHealthSlowSingleRouteFailures; i++ {
		recordLegacyRouteOutcome(t, 29, "gpt-test", "/v1/responses", ChannelFailureTransient)
	}

	require.False(t, HasDueChannelHealthProbe())
	state := routeStateForTest(29, "gpt-test", "/v1/responses")
	require.Equal(t, channelHealthSlowSingleRouteFailures, state.FailuresSinceSuccess)
	require.False(t, state.Suspect)
}

func TestBurstFailureRequiresTenSecondsOfSustainedFailures(t *testing.T) {
	now := setupChannelHealthTest(t)
	for i := 0; i < channelHealthSuspectMinimumSamples; i++ {
		recordLegacyRouteOutcome(t, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	}
	require.False(t, HasDueChannelHealthProbe())

	*now = now.Add(channelHealthSuspectMinimumDuration)
	recordLegacyRouteOutcome(t, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	require.True(t, HasDueChannelHealthProbe())
}

func TestAggregateFailureRateSchedulesChannelProbe(t *testing.T) {
	setupChannelHealthTest(t)
	for i := 0; i < 10; i++ {
		modelName := "gpt-a"
		requestPath := "/v1/responses"
		if i%2 == 1 {
			modelName = "gpt-b"
			requestPath = "/v1/chat/completions"
		}
		recordLegacyRouteOutcome(t, 29, modelName, requestPath, "success")
	}
	for i := 0; i < 40; i++ {
		modelName := "gpt-a"
		requestPath := "/v1/responses"
		if i%2 == 1 {
			modelName = "gpt-b"
			requestPath = "/v1/chat/completions"
		}
		recordLegacyRouteOutcome(t, 29, modelName, requestPath, ChannelFailureTransient)
	}

	targets := ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)
	require.Equal(t, ChannelHealthProbeScopeChannel, targets[0].Scope)
}

func TestAggregateTimeoutRateSchedulesChannelProbe(t *testing.T) {
	setupChannelHealthTest(t)
	record := func(modelName string, requestPath string, class ChannelFailureClass, statusCode int) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		require.True(t, AllowChannelCircuitAttempt(ctx, 29, modelName, requestPath))
		if class == "success" {
			RecordChannelCircuitSuccess(ctx, 29, modelName, requestPath)
			return
		}
		RecordChannelCircuitFailureDecision(ctx, 29, modelName, requestPath, ChannelFailureDecision{Class: class}, statusCode)
	}
	for i := 0; i < 5; i++ {
		record("gpt-a", "/v1/responses", "success", http.StatusOK)
	}
	for i := 0; i < 15; i++ {
		modelName := "gpt-a"
		requestPath := "/v1/responses"
		if i%2 == 1 {
			modelName = "gpt-b"
			requestPath = "/v1/chat/completions"
		}
		class := ChannelFailureTransient
		statusCode := http.StatusBadGateway
		if i < 10 {
			class = ChannelFailureUncertain
			statusCode = http.StatusGatewayTimeout
		}
		record(modelName, requestPath, class, statusCode)
	}

	targets := ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)
	require.Equal(t, ChannelHealthProbeScopeChannel, targets[0].Scope)
}

func TestTerminalRequestErrorsNeverScheduleHealthProbe(t *testing.T) {
	now := setupChannelHealthTest(t)
	for i := 0; i < 100; i++ {
		modelName := "gpt-a"
		requestPath := "/v1/responses"
		if i%2 == 1 {
			modelName = "gpt-b"
			requestPath = "/v1/chat/completions"
		}
		recordLegacyRouteOutcome(t, 29, modelName, requestPath, ChannelFailureTerminal)
	}
	*now = now.Add(channelHealthWindow)
	recordLegacyRouteOutcome(t, 29, "gpt-a", "/v1/responses", ChannelFailureTerminal)

	require.False(t, HasDueChannelHealthProbe())
	aggregate := aggregateStateForTest(29)
	require.Zero(t, aggregate.FailuresSinceSuccess)
}

func TestLateProbeResultFailsTripleValidationAfterRealSuccess(t *testing.T) {
	now := setupChannelHealthTest(t)
	for i := 0; i < channelHealthSuspectMinimumSamples-1; i++ {
		recordLegacyRouteOutcome(t, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	}
	*now = now.Add(channelHealthSuspectMinimumDuration)
	recordLegacyRouteOutcome(t, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	targets := ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)
	target := targets[0]
	require.NotZero(t, target.Revision)
	require.NotZero(t, target.ProbeID)
	require.Equal(t, ChannelHealthProbeTypeInitial, target.ProbeType)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.True(t, AllowChannelCircuitAttempt(ctx, 37, "gpt-test", "/v1/responses"))
	RecordChannelCircuitSuccess(ctx, 37, "gpt-test", "/v1/responses")
	CompleteChannelHealthProbe(target, ChannelHealthProbeResult{Class: ChannelFailureTransient})

	state := routeStateForTest(37, "gpt-test", "/v1/responses")
	require.True(t, state.OpenUntil.IsZero())
	require.False(t, state.ProbeInFlight)
	require.False(t, state.Suspect)
}

func TestExpiredProbeLeaseCanBeReclaimedAndOldResultIsIgnored(t *testing.T) {
	now := setupChannelHealthTest(t)
	scheduleRouteProbeForTest(t, nil, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	first := ClaimDueChannelHealthProbes(1)
	require.Len(t, first, 1)

	*now = now.Add(channelHealthProbeLease + time.Millisecond)
	second := ClaimDueChannelHealthProbes(1)
	require.Len(t, second, 1)
	require.NotEqual(t, first[0].ProbeID, second[0].ProbeID)
	require.Greater(t, second[0].Revision, first[0].Revision)

	CompleteChannelHealthProbe(first[0], ChannelHealthProbeResult{Class: ChannelFailureTransient})
	require.True(t, routeStateForTest(37, "gpt-test", "/v1/responses").OpenUntil.IsZero())
	CompleteChannelHealthProbe(second[0], ChannelHealthProbeResult{Success: true})
	require.False(t, routeStateForTest(37, "gpt-test", "/v1/responses").ProbeInFlight)
}

func TestImageProbeLeaseOutlivesImageProbeTimeout(t *testing.T) {
	now := setupChannelHealthTest(t)
	scheduleRouteProbeForTest(t, nil, 37, "image-model", "/v1/images/generations", ChannelFailureTransient)
	first := ClaimDueChannelHealthProbes(1)
	require.Len(t, first, 1)

	*now = now.Add(channelHealthProbeLease + time.Millisecond)
	require.Empty(t, ClaimDueChannelHealthProbes(1))
	*now = now.Add(channelHealthImageProbeLease - channelHealthProbeLease)
	second := ClaimDueChannelHealthProbes(1)
	require.Len(t, second, 1)
	require.NotEqual(t, first[0].ProbeID, second[0].ProbeID)
}

func TestSuccessfulRouteProbeResetsAggregateNoSuccessEvidence(t *testing.T) {
	setupChannelHealthTest(t)
	scheduleRouteProbeForTest(t, nil, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)

	before := aggregateStateForTest(37)
	require.Equal(t, channelHealthSuspectMinimumSamples, before.FailuresSinceSuccess)
	require.NotEmpty(t, before.FailedRoutesSinceSuccess)

	targets := ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)
	require.Equal(t, ChannelHealthProbeScopeRoute, targets[0].Scope)
	CompleteChannelHealthProbe(targets[0], ChannelHealthProbeResult{Success: true})

	after := aggregateStateForTest(37)
	require.Zero(t, after.FailuresSinceSuccess)
	require.Empty(t, after.FailedRoutesSinceSuccess)
	require.True(t, after.NoSuccessFailureAt.IsZero())
	require.Equal(t, channelCircuitNow(), after.LastSuccessAt)
	successes, _, _ := summarizeAggregateHealthLocked(&after, channelCircuitNow())
	require.Equal(t, 1, successes)
}

func TestRealSuccessInvalidatesInflightChannelProbe(t *testing.T) {
	now := setupChannelHealthTest(t)
	recordLegacyRouteOutcome(t, 29, "gpt-a", "/v1/responses", ChannelFailureTransient)
	recordLegacyRouteOutcome(t, 29, "gpt-b", "/v1/chat/completions", ChannelFailureTransient)
	*now = now.Add(channelHealthWindow)
	recordLegacyRouteOutcome(t, 29, "gpt-a", "/v1/responses", ChannelFailureTransient)
	targets := ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)
	require.Equal(t, ChannelHealthProbeScopeChannel, targets[0].Scope)

	recordLegacyRouteOutcome(t, 29, "gpt-c", "/v1/embeddings", "success")
	CompleteChannelHealthProbe(targets[0], ChannelHealthProbeResult{Class: ChannelFailureTransient})

	aggregate := aggregateStateForTest(29)
	require.True(t, aggregate.OpenUntil.IsZero())
	require.False(t, aggregate.ProbeInFlight)
	require.False(t, aggregate.Suspect)
}

func TestPersistentRouteProbeAndOpenStateSurviveRestart(t *testing.T) {
	now := setupChannelHealthTest(t)
	originalDB := model.DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalDatabaseType := common.MainDatabaseType()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.ChannelHealthState{}))
	model.DB = db
	common.MemoryCacheEnabled = false
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	channelHealthPersistenceEnabled = true
	t.Cleanup(func() {
		model.DB = originalDB
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		common.SetMainDatabaseType(originalDatabaseType)
	})

	channel := &model.Channel{
		Id: 37, Name: "persistent-test", Type: constant.ChannelTypeOpenAI, Key: "test-key",
		Models: "gpt-test", Status: common.ChannelStatusEnabled, CreatedTime: now.Add(-time.Hour).Unix(),
	}
	require.NoError(t, db.Create(channel).Error)
	scheduleRouteProbeForTest(t, channel, 0, "gpt-test", "/v1/responses", ChannelFailureTransient)
	first := ClaimDueChannelHealthProbes(1)
	require.Len(t, first, 1)
	require.Equal(t, ChannelHealthProbeTypeInitial, first[0].ProbeType)

	resetMemoryChannelHealth()
	require.NoError(t, RestorePersistentChannelHealth())
	*now = now.Add(11 * time.Second)
	restored := ClaimDueChannelHealthProbes(1)
	require.Len(t, restored, 1)
	require.Equal(t, ChannelHealthProbeTypeInitial, restored[0].ProbeType)
	CompleteChannelHealthProbe(restored[0], ChannelHealthProbeResult{Class: ChannelFailureTransient, Reason: "upstream 503"})
	require.False(t, AllowChannelHealthAttempt(nil, channel, "gpt-test", "/v1/responses"))

	resetMemoryChannelHealth()
	require.NoError(t, RestorePersistentChannelHealth())
	require.False(t, AllowChannelHealthAttempt(nil, channel, "gpt-test", "/v1/responses"))
	*now = now.Add(channelHealthOpenFor + 11*time.Second)
	recovery := ClaimDueChannelHealthProbes(1)
	require.Len(t, recovery, 1)
	require.Equal(t, ChannelHealthProbeTypeRecovery, recovery[0].ProbeType)

	resetMemoryChannelHealth()
	require.NoError(t, RestorePersistentChannelHealth())
	*now = now.Add(11 * time.Second)
	recovery = ClaimDueChannelHealthProbes(1)
	require.Len(t, recovery, 1)
	require.Equal(t, ChannelHealthProbeTypeRecovery, recovery[0].ProbeType)
	CompleteChannelHealthProbe(recovery[0], ChannelHealthProbeResult{Success: true})

	channel29 := &model.Channel{
		Id: 29, Name: "aggregate-persistent-test", Type: constant.ChannelTypeOpenAI, Key: "test-key-29",
		Models: "gpt-a,gpt-b", Status: common.ChannelStatusEnabled, CreatedTime: now.Add(-time.Hour).Unix(),
	}
	require.NoError(t, db.Create(channel29).Error)
	for _, route := range []struct{ modelName, requestPath string }{
		{"gpt-a", "/v1/responses"},
		{"gpt-b", "/v1/chat/completions"},
	} {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		require.True(t, AllowChannelHealthAttempt(ctx, channel29, route.modelName, route.requestPath))
		RecordChannelCircuitFailure(ctx, channel29.Id, route.modelName, route.requestPath, ChannelFailureTransient)
	}
	*now = now.Add(channelHealthWindow)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.True(t, AllowChannelHealthAttempt(ctx, channel29, "gpt-a", "/v1/responses"))
	RecordChannelCircuitFailure(ctx, channel29.Id, "gpt-a", "/v1/responses", ChannelFailureTransient)
	channelProbe := ClaimDueChannelHealthProbes(1)
	require.Len(t, channelProbe, 1)
	require.Equal(t, ChannelHealthProbeScopeChannel, channelProbe[0].Scope)

	resetMemoryChannelHealth()
	require.NoError(t, RestorePersistentChannelHealth())
	*now = now.Add(11 * time.Second)
	channelProbe = ClaimDueChannelHealthProbes(1)
	require.Len(t, channelProbe, 1)
	require.Equal(t, ChannelHealthProbeScopeChannel, channelProbe[0].Scope)
	require.Equal(t, ChannelHealthProbeTypeInitial, channelProbe[0].ProbeType)
	CompleteChannelHealthProbe(channelProbe[0], ChannelHealthProbeResult{Success: true})

	ctx, _ = gin.CreateTestContext(httptest.NewRecorder())
	require.True(t, AllowChannelHealthAttempt(ctx, channel29, "gpt-a", "/v1/responses"))
	RecordChannelCircuitSuccess(ctx, channel29.Id, "gpt-a", "/v1/responses")
	SuspendChannelHealth(channel29)
	resetMemoryChannelHealth()
	require.True(t, ScheduleManualChannelRecovery(channel29))
	manualRecovery := ClaimDueChannelHealthProbes(1)
	require.Len(t, manualRecovery, 1)
	require.Equal(t, ChannelHealthProbeScopeChannel, manualRecovery[0].Scope)
	require.Equal(t, ChannelHealthProbeTypeRecovery, manualRecovery[0].ProbeType)
}

func TestManualRecoveryWithoutObservedTrafficUsesConfiguredModel(t *testing.T) {
	setupChannelHealthTest(t)
	testModel := "gpt-5.6-terra"
	channel := &model.Channel{
		Id:          37,
		Type:        constant.ChannelTypeOpenAI,
		Key:         "test-key",
		Models:      "fallback-model",
		TestModel:   &testModel,
		CreatedTime: channelCircuitNow().Add(-time.Hour).Unix(),
	}

	require.True(t, ScheduleManualChannelRecovery(channel))
	require.False(t, AllowChannelHealthAttempt(nil, channel, testModel, "/v1/chat/completions"))
	targets := ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)
	require.Equal(t, ChannelHealthProbeScopeChannel, targets[0].Scope)
	require.Equal(t, ChannelHealthProbeTypeRecovery, targets[0].ProbeType)
	require.Equal(t, testModel, targets[0].ModelName)
	require.Equal(t, "/v1/chat/completions", targets[0].RequestPath)
}

func TestCompleteFailureRequiresActiveProbeBeforeOpeningRoute(t *testing.T) {
	setupChannelHealthTest(t)
	confirmLegacyRouteFailureForTest(t, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	require.False(t, AllowChannelCircuitAttempt(nil, 37, "gpt-test", "/v1/responses"))
	require.True(t, AllowChannelCircuitAttempt(nil, 37, "other-model", "/v1/responses"))
}

func TestDeterministicInitialProbeFailureDoesNotOpenRoute(t *testing.T) {
	now := setupChannelHealthTest(t)
	scheduleRouteProbeForTest(t, nil, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	targets := ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)

	CompleteChannelHealthProbe(targets[0], ChannelHealthProbeResult{
		Class:      ChannelFailureTerminal,
		Reason:     "deterministic_request",
		StatusCode: http.StatusBadRequest,
	})

	state := routeStateForTest(37, "gpt-test", "/v1/responses")
	require.True(t, state.OpenUntil.IsZero())
	require.True(t, state.Suspect)
	require.Equal(t, now.Add(channelHealthOpenFor), state.ProbeDue)
	require.True(t, AllowChannelCircuitAttempt(nil, 37, "gpt-test", "/v1/responses"))
	require.False(t, HasDueChannelHealthProbe())
}

func TestRateLimitedInitialProbeFailureDoesNotOpenRoute(t *testing.T) {
	now := setupChannelHealthTest(t)
	scheduleRouteProbeForTest(t, nil, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	targets := ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)

	CompleteChannelHealthProbe(targets[0], ChannelHealthProbeResult{
		Class:      ChannelFailureRateLimited,
		Reason:     "upstream_rate_limited",
		StatusCode: http.StatusTooManyRequests,
	})

	state := routeStateForTest(37, "gpt-test", "/v1/responses")
	require.True(t, state.OpenUntil.IsZero())
	require.True(t, state.Suspect)
	require.Equal(t, now.Add(channelHealthOpenFor), state.ProbeDue)
	require.True(t, AllowChannelCircuitAttempt(nil, 37, "gpt-test", "/v1/responses"))
}

func TestPoolAccountProbeNeedsPoolAccountTrigger(t *testing.T) {
	now := setupChannelHealthTest(t)
	scheduleRouteProbeForTest(t, nil, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	targets := ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)

	CompleteChannelHealthProbe(targets[0], ChannelHealthProbeResult{
		Class:      ChannelFailurePoolAccount,
		Reason:     "insufficient account balance",
		StatusCode: http.StatusForbidden,
	})

	state := routeStateForTest(37, "gpt-test", "/v1/responses")
	require.True(t, state.OpenUntil.IsZero())
	require.True(t, state.Suspect)
	require.Equal(t, now.Add(channelHealthOpenFor), state.ProbeDue)
}

func TestInconclusiveRecoveryProbeKeepsRouteOpen(t *testing.T) {
	now := setupChannelHealthTest(t)
	confirmLegacyRouteFailureForTest(t, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	*now = now.Add(channelHealthOpenFor + time.Millisecond)
	targets := ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)
	require.Equal(t, ChannelHealthProbeTypeRecovery, targets[0].ProbeType)

	CompleteChannelHealthProbe(targets[0], ChannelHealthProbeResult{
		Class:      ChannelFailureTerminal,
		Reason:     "deterministic_request",
		StatusCode: http.StatusBadRequest,
	})

	state := routeStateForTest(37, "gpt-test", "/v1/responses")
	require.Equal(t, now.Add(channelHealthOpenFor), state.OpenUntil)
	require.False(t, AllowChannelCircuitAttempt(nil, 37, "gpt-test", "/v1/responses"))
}

func TestDeterministicInitialProbeFailureDoesNotOpenChannel(t *testing.T) {
	now := setupChannelHealthTest(t)
	recordLegacyRouteOutcome(t, 29, "gpt-a", "/v1/responses", ChannelFailureTransient)
	recordLegacyRouteOutcome(t, 29, "gpt-b", "/v1/chat/completions", ChannelFailureTransient)
	*now = now.Add(channelHealthWindow)
	recordLegacyRouteOutcome(t, 29, "gpt-a", "/v1/responses", ChannelFailureTransient)
	targets := ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)
	require.Equal(t, ChannelHealthProbeScopeChannel, targets[0].Scope)

	CompleteChannelHealthProbe(targets[0], ChannelHealthProbeResult{
		Class:      ChannelFailureTerminal,
		Reason:     "deterministic_request",
		StatusCode: http.StatusBadRequest,
	})

	state := aggregateStateForTest(29)
	require.True(t, state.OpenUntil.IsZero())
	require.True(t, state.Suspect)
	require.Equal(t, now.Add(channelHealthOpenFor), state.ProbeDue)
	require.True(t, AllowChannelCircuitAttempt(nil, 29, "gpt-test", "/v1/responses"))
}

func TestInconclusiveRecoveryProbeKeepsChannelOpen(t *testing.T) {
	now := setupChannelHealthTest(t)
	recordLegacyRouteOutcome(t, 29, "gpt-a", "/v1/responses", ChannelFailureTransient)
	recordLegacyRouteOutcome(t, 29, "gpt-b", "/v1/chat/completions", ChannelFailureTransient)
	*now = now.Add(channelHealthWindow)
	recordLegacyRouteOutcome(t, 29, "gpt-a", "/v1/responses", ChannelFailureTransient)
	targets := ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)
	CompleteChannelHealthProbe(targets[0], ChannelHealthProbeResult{Class: ChannelFailureTransient})

	*now = now.Add(channelHealthOpenFor + time.Millisecond)
	targets = ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)
	require.Equal(t, ChannelHealthProbeTypeRecovery, targets[0].ProbeType)
	CompleteChannelHealthProbe(targets[0], ChannelHealthProbeResult{
		Class:      ChannelFailureTerminal,
		Reason:     "deterministic_request",
		StatusCode: http.StatusBadRequest,
	})

	state := aggregateStateForTest(29)
	require.Equal(t, now.Add(channelHealthOpenFor), state.OpenUntil)
	require.False(t, AllowChannelCircuitAttempt(nil, 29, "gpt-test", "/v1/responses"))
}

func TestLateInflightFailureDoesNotExtendOpenWindow(t *testing.T) {
	now := setupChannelHealthTest(t)
	contexts := make([]*gin.Context, channelHealthSuspectMinimumSamples+1)
	for i := range contexts {
		contexts[i], _ = gin.CreateTestContext(httptest.NewRecorder())
		require.True(t, AllowChannelCircuitAttempt(contexts[i], 37, "gpt-test", "/v1/responses"))
	}
	for i := 0; i < channelHealthSuspectMinimumSamples; i++ {
		if i == channelHealthSuspectMinimumSamples-1 {
			identity := buildChannelHealthIdentity(nil, 37, "gpt-test", "/v1/responses", *now)
			shard := channelHealthShardFor(identity.RouteKey)
			shard.Lock()
			shard.Routes[identity.RouteKey].BurstFailureStartedAt = now.Add(-channelHealthSuspectMinimumDuration)
			shard.Unlock()
		}
		RecordChannelCircuitFailure(contexts[i], 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	}
	targets := ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)
	CompleteChannelHealthProbe(targets[0], ChannelHealthProbeResult{Class: ChannelFailureTransient})
	openedUntil := routeStateForTest(37, "gpt-test", "/v1/responses").OpenUntil

	*now = now.Add(30 * time.Second)
	RecordChannelCircuitFailure(contexts[len(contexts)-1], 37, "gpt-test", "/v1/responses", ChannelFailureTransient)

	require.Equal(t, openedUntil, routeStateForTest(37, "gpt-test", "/v1/responses").OpenUntil)
}

func TestActiveProbeIsSingleAndRecoversAtFullCapacity(t *testing.T) {
	now := setupChannelHealthTest(t)
	confirmLegacyRouteFailureForTest(t, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	*now = now.Add(channelHealthOpenFor + time.Millisecond)
	require.False(t, AllowChannelCircuitAttempt(nil, 37, "gpt-test", "/v1/responses"))
	targets := ClaimDueChannelHealthProbes(10)
	require.Len(t, targets, 1)
	require.Empty(t, ClaimDueChannelHealthProbes(10))
	CompleteChannelHealthProbe(targets[0], ChannelHealthProbeResult{Success: true})

	state := routeStateForTest(37, "gpt-test", "/v1/responses")
	require.Equal(t, channelHealthEstablishedCapacity, state.Capacity)
	require.Zero(t, state.RecoveryTargetCapacity)
	require.True(t, AllowChannelCircuitAttempt(nil, 37, "gpt-test", "/v1/responses"))
}

func TestFailedActiveProbeReopensForTwoMinutes(t *testing.T) {
	now := setupChannelHealthTest(t)
	confirmLegacyRouteFailureForTest(t, 37, "gpt-test", "/v1/responses", ChannelFailureUncertain)
	*now = now.Add(channelHealthOpenFor + time.Millisecond)
	require.False(t, AllowChannelCircuitAttempt(nil, 37, "gpt-test", "/v1/responses"))
	targets := ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)
	CompleteChannelHealthProbe(targets[0], ChannelHealthProbeResult{Class: ChannelFailureUncertain})
	require.False(t, AllowChannelCircuitAttempt(nil, 37, "gpt-test", "/v1/responses"))

	*now = now.Add(channelHealthOpenFor + time.Millisecond)
	require.False(t, AllowChannelCircuitAttempt(nil, 37, "gpt-test", "/v1/responses"))
	require.Len(t, ClaimDueChannelHealthProbes(1), 1)
}

func TestRealSuccessCancelsPendingProbe(t *testing.T) {
	setupChannelHealthTest(t)
	scheduleRouteProbeForTest(t, nil, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	require.True(t, HasDueChannelHealthProbe())

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.True(t, AllowChannelCircuitAttempt(ctx, 37, "gpt-test", "/v1/responses"))
	RecordChannelCircuitSuccess(ctx, 37, "gpt-test", "/v1/responses")

	require.False(t, HasDueChannelHealthProbe())
	require.Empty(t, ClaimDueChannelHealthProbes(1))
	require.True(t, routeStateForTest(37, "gpt-test", "/v1/responses").OpenUntil.IsZero())
}

func TestReleasedProbeCanBeClaimedAgain(t *testing.T) {
	setupChannelHealthTest(t)
	scheduleRouteProbeForTest(t, nil, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	targets := ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)
	require.False(t, HasDueChannelHealthProbe())

	ReleaseChannelHealthProbe(targets[0])
	require.True(t, HasDueChannelHealthProbe())
	nextTargets := ClaimDueChannelHealthProbes(1)
	require.Len(t, nextTargets, 1)
	ReleaseChannelHealthProbe(targets[0])
	require.True(t, routeStateForTest(37, "gpt-test", "/v1/responses").ProbeInFlight)
	CompleteChannelHealthProbe(nextTargets[0], ChannelHealthProbeResult{Success: true})
}

func TestExpiredOpenRouteIsNotRemovedBeforeProbe(t *testing.T) {
	now := setupChannelHealthTest(t)
	confirmLegacyRouteFailureForTest(t, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	identity := buildChannelHealthIdentity(nil, 37, "gpt-test", "/v1/responses", *now)
	shard := channelHealthShardFor(identity.RouteKey)

	*now = now.Add(channelHealthStateTTL + time.Minute)
	shard.Lock()
	cleanupRouteHealthShardLocked(shard, *now)
	_, exists := shard.Routes[identity.RouteKey]
	shard.Unlock()

	require.True(t, exists)
	require.True(t, HasDueChannelHealthProbe())
}

func TestManualRecoveryInvalidatesOlderProbeResult(t *testing.T) {
	now := setupChannelHealthTest(t)
	channel := &model.Channel{
		Id:          37,
		Type:        constant.ChannelTypeOpenAI,
		Key:         "test-key",
		Models:      "gpt-test",
		CreatedTime: now.Add(-time.Hour).Unix(),
	}
	scheduleRouteProbeForTest(t, channel, 0, "gpt-test", "/v1/responses", ChannelFailureTransient)
	targets := ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)
	CompleteChannelHealthProbe(targets[0], ChannelHealthProbeResult{Class: ChannelFailureTransient})

	*now = now.Add(channelHealthOpenFor + time.Millisecond)
	staleTargets := ClaimDueChannelHealthProbes(1)
	require.Len(t, staleTargets, 1)
	_, err := RecoverChannelHealth(channel, ChannelHealthRecoveryRequest{
		Scope:       ChannelHealthRecoveryScopeRoute,
		ModelName:   "gpt-test",
		RequestPath: "/v1/responses",
	})
	require.NoError(t, err)
	CompleteChannelHealthProbe(staleTargets[0], ChannelHealthProbeResult{Class: ChannelFailureTransient})

	state := routeStateForTestWithChannel(channel, "gpt-test", "/v1/responses")
	require.False(t, state.OpenUntil.IsZero())
	require.False(t, state.ProbeInFlight)
	require.Equal(t, ChannelHealthProbeTypeRecovery, state.ProbeType)
	recoveryTargets := ClaimDueChannelHealthProbes(1)
	require.Len(t, recoveryTargets, 1)
	require.Equal(t, ChannelHealthProbeTypeRecovery, recoveryTargets[0].ProbeType)
}

func TestSustainedRateLimitRequiresTwoMinutesBeforeProbe(t *testing.T) {
	now := setupChannelHealthTest(t)
	for i := 0; i < 21; i++ {
		recordLegacyRouteOutcome(t, 37, "gpt-test", "/v1/responses", ChannelFailureRateLimited)
		if i < 20 {
			*now = now.Add(6 * time.Second)
		}
	}

	require.True(t, HasDueChannelHealthProbe())
	require.True(t, AllowChannelCircuitAttempt(nil, 37, "gpt-test", "/v1/responses"))
}

func TestUnsupportedPathNeverSchedulesActiveProbe(t *testing.T) {
	setupChannelHealthTest(t)
	for i := 0; i < channelHealthSlowSingleRouteFailures; i++ {
		recordLegacyRouteOutcome(t, 37, "video-model", "/v1/videos", ChannelFailureTransient)
	}

	require.False(t, HasDueChannelHealthProbe())
	require.True(t, AllowChannelCircuitAttempt(nil, 37, "video-model", "/v1/videos"))
}

func TestRecoveryUsesNormalBreakerThresholds(t *testing.T) {
	now := setupChannelHealthTest(t)
	confirmLegacyRouteFailureForTest(t, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	targets := ClaimDueChannelHealthProbes(1)
	require.Empty(t, targets)

	state := routeStateForTest(37, "gpt-test", "/v1/responses")
	*now = state.OpenUntil.Add(time.Millisecond)
	targets = ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)
	CompleteChannelHealthProbe(targets[0], ChannelHealthProbeResult{Success: true})

	for i := 0; i < 2; i++ {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		require.True(t, AllowChannelCircuitAttempt(ctx, 37, "gpt-test", "/v1/responses"))
		RecordChannelCircuitFailure(ctx, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	}
	require.True(t, routeStateForTest(37, "gpt-test", "/v1/responses").OpenUntil.IsZero())
	require.False(t, HasDueChannelHealthProbe())
	require.True(t, AllowChannelCircuitAttempt(nil, 37, "gpt-test", "/v1/responses"))
}

func TestChannelHealthSnapshotAndRouteRecovery(t *testing.T) {
	setupChannelHealthTest(t)
	channel := &model.Channel{
		Id:          29,
		Type:        constant.ChannelTypeOpenAI,
		Key:         "test-key",
		Models:      "gpt-5.6-sol",
		CreatedTime: channelCircuitNow().Add(-time.Hour).Unix(),
	}

	scheduleRouteProbeForTest(t, channel, 0, "gpt-5.6-sol", "/v1/responses", ChannelFailureTransient)
	targets := ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)
	CompleteChannelHealthProbe(targets[0], ChannelHealthProbeResult{
		Class:      ChannelFailureTransient,
		Reason:     "upstream_503",
		StatusCode: http.StatusServiceUnavailable,
	})

	snapshots := GetChannelAdaptiveHealthSnapshots(false)
	require.Len(t, snapshots, 1)
	require.Equal(t, channel.Id, snapshots[0].ChannelID)
	require.Len(t, snapshots[0].Routes, 1)
	route := snapshots[0].Routes[0]
	require.Equal(t, ChannelHealthStateCircuitOpen, route.State)
	require.Equal(t, "gpt-5.6-sol", route.ModelName)
	require.Equal(t, "/v1/responses", route.RequestPath)
	require.Equal(t, ChannelFailureTransient, route.LastFailureClass)
	require.Equal(t, "upstream_503", route.LastFailureReason)
	require.Equal(t, http.StatusServiceUnavailable, route.LastFailureStatusCode)

	result, err := RecoverChannelHealth(channel, ChannelHealthRecoveryRequest{
		Scope:       ChannelHealthRecoveryScopeRoute,
		ModelName:   "gpt-5.6-sol",
		RequestPath: "/v1/responses",
	})
	require.NoError(t, err)
	require.Positive(t, result.ChangedItems)
	require.Equal(t, channelHealthEstablishedCapacity, result.Capacity)
	require.False(t, AllowChannelHealthAttempt(nil, channel, "gpt-5.6-sol", "/v1/responses"))

	snapshots = GetChannelAdaptiveHealthSnapshots(false)
	require.Len(t, snapshots, 1)
	require.Equal(t, ChannelHealthStateCircuitOpen, snapshots[0].Routes[0].State)

	targets = ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)
	require.Equal(t, ChannelHealthProbeTypeRecovery, targets[0].ProbeType)
	CompleteChannelHealthProbe(targets[0], ChannelHealthProbeResult{Success: true})
	require.True(t, AllowChannelHealthAttempt(nil, channel, "gpt-5.6-sol", "/v1/responses"))
	require.Equal(t, channelHealthEstablishedCapacity, routeStateForTestWithChannel(channel, "gpt-5.6-sol", "/v1/responses").Capacity)
}

func TestChannelHealthSnapshotAndKeyRecoveryDoNotExposeKey(t *testing.T) {
	setupChannelHealthTest(t)
	channel := &model.Channel{
		Id:          50,
		Type:        constant.ChannelTypeOpenAI,
		Key:         "secret-key-that-must-not-be-returned",
		Models:      "gpt-test",
		CreatedTime: channelCircuitNow().Add(-time.Hour).Unix(),
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.True(t, AllowChannelHealthAttempt(ctx, channel, "gpt-test", "/v1/chat/completions"))
	common.SetContextKey(ctx, constant.ContextKeyChannelKey, channel.Key)
	require.True(t, AcquireChannelHealthKey(ctx, channel.Key))
	RecordChannelCircuitFailureDecision(ctx, channel.Id, "gpt-test", "/v1/chat/completions", ChannelFailureDecision{
		Class:  ChannelFailureChannelFatal,
		Reason: "credential_rejected",
	}, http.StatusUnauthorized)

	snapshots := GetChannelAdaptiveHealthSnapshots(false)
	require.Len(t, snapshots, 1)
	require.Len(t, snapshots[0].Keys, 1)
	require.Equal(t, 0, snapshots[0].Keys[0].KeyIndex)
	require.Equal(t, ChannelHealthStateIsolated, snapshots[0].Keys[0].State)
	encoded, err := common.Marshal(snapshots)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), channel.Key)

	keyIndex := 0
	result, err := RecoverChannelHealth(channel, ChannelHealthRecoveryRequest{
		Scope:    ChannelHealthRecoveryScopeKey,
		KeyIndex: &keyIndex,
	})
	require.NoError(t, err)
	require.Positive(t, result.ChangedItems)
	require.Empty(t, GetChannelAdaptiveHealthSnapshots(false))
}

func TestChannelHealthSnapshotAndRecoveryAreSafeDuringRouteCompletion(t *testing.T) {
	setupChannelHealthTest(t)
	channel := &model.Channel{
		Id:          63,
		Type:        constant.ChannelTypeOpenAI,
		Key:         "test-key",
		Models:      "gpt-test",
		CreatedTime: channelCircuitNow().Add(-time.Hour).Unix(),
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.True(t, AllowChannelHealthAttempt(ctx, channel, "gpt-test", "/v1/responses"))

	start := make(chan struct{})
	var completed sync.WaitGroup
	completed.Add(3)
	go func() {
		defer completed.Done()
		<-start
		RecordChannelCircuitSuccess(ctx, channel.Id, "gpt-test", "/v1/responses")
	}()
	go func() {
		defer completed.Done()
		<-start
		_, _ = RecoverChannelHealth(channel, ChannelHealthRecoveryRequest{
			Scope:       ChannelHealthRecoveryScopeRoute,
			ModelName:   "gpt-test",
			RequestPath: "/v1/responses",
		})
	}()
	go func() {
		defer completed.Done()
		<-start
		_ = GetChannelAdaptiveHealthSnapshots(true)
	}()
	close(start)
	completed.Wait()

	state := routeStateForTestWithChannel(channel, "gpt-test", "/v1/responses")
	require.Zero(t, state.InFlight)
	require.GreaterOrEqual(t, state.Capacity, channelHealthMinCapacity)
	require.LessOrEqual(t, state.Capacity, channelHealthMaxCapacity)
}

func TestRateLimitedActiveProbeKeepsRouteOpen(t *testing.T) {
	now := setupChannelHealthTest(t)
	confirmLegacyRouteFailureForTest(t, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	*now = now.Add(channelHealthOpenFor + time.Millisecond)
	targets := ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)
	CompleteChannelHealthProbe(targets[0], ChannelHealthProbeResult{Class: ChannelFailureRateLimited, StatusCode: http.StatusTooManyRequests})

	state := routeStateForTest(37, "gpt-test", "/v1/responses")
	require.False(t, state.OpenUntil.IsZero())
	require.False(t, state.ProbeInFlight)
	require.False(t, AllowChannelCircuitAttempt(nil, 37, "gpt-test", "/v1/responses"))
}

func TestTwoIndependentUnhealthyRoutesOpenWholeChannel(t *testing.T) {
	setupChannelHealthTest(t)
	for _, route := range []struct {
		model string
		path  string
	}{{"gpt-a", "/v1/responses"}, {"gpt-b", "/v1/chat/completions"}} {
		confirmLegacyRouteFailureForTest(t, 8, route.model, route.path, ChannelFailureUncertain)
	}
	require.False(t, AllowChannelCircuitAttempt(nil, 8, "healthy-model", "/v1/embeddings"))
	require.True(t, AllowChannelCircuitAttempt(nil, 9, "healthy-model", "/v1/embeddings"))
}

func TestOneRecoveredRouteReleasesAggregateChannelBlock(t *testing.T) {
	now := setupChannelHealthTest(t)
	for _, modelName := range []string{"gpt-a", "gpt-b"} {
		confirmLegacyRouteFailureForTest(t, 8, modelName, "/v1/responses", ChannelFailureTransient)
	}
	require.False(t, AllowChannelCircuitAttempt(nil, 8, "healthy-model", "/v1/embeddings"))

	*now = now.Add(channelHealthOpenFor + time.Millisecond)
	targets := ClaimDueChannelHealthProbes(10)
	require.Len(t, targets, 1)
	CompleteChannelHealthProbe(targets[0], ChannelHealthProbeResult{Success: true})

	require.True(t, AllowChannelCircuitAttempt(nil, 8, "another-model", "/v1/chat/completions"))
}

func TestSingleRateLimitDoesNotReducePooledKeyOrRouteCapacity(t *testing.T) {
	setupChannelHealthTest(t)
	channel := &model.Channel{Id: 29, Key: "key-a", CreatedTime: channelCircuitNow().Add(-time.Hour).Unix()}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.True(t, AllowChannelHealthAttempt(ctx, channel, "gpt-test", "/v1/responses"))
	require.True(t, AcquireChannelHealthKey(ctx, "key-a"))
	common.SetContextKey(ctx, constant.ContextKeyChannelKey, "key-a")
	RecordChannelCircuitFailure(ctx, channel.Id, "gpt-test", "/v1/responses", ChannelFailureRateLimited)

	state := routeStateForTestWithChannel(channel, "gpt-test", "/v1/responses")
	require.Equal(t, channelHealthEstablishedCapacity, state.Capacity)
	require.True(t, state.OpenUntil.IsZero())
	identity := buildChannelHealthIdentity(channel, 0, "gpt-test", "/v1/responses", channelCircuitNow())
	memoryChannelHealth.Keys.RLock()
	keyState := memoryChannelHealth.Keys.States[keyHealthRouteKey(identity, "key-a")]
	memoryChannelHealth.Keys.RUnlock()
	require.Equal(t, channelHealthEstablishedCapacity, keyState.Capacity)
	require.Equal(t, 0, keyState.InFlight)
}

func TestBurstRateLimitDoesNotReduceRouteCapacity(t *testing.T) {
	setupChannelHealthTest(t)
	channel := &model.Channel{Id: 29, Key: "key-a", CreatedTime: channelCircuitNow().Add(-time.Hour).Unix()}
	for i := 0; i < 5; i++ {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		require.True(t, AllowChannelHealthAttempt(ctx, channel, "gpt-test", "/v1/responses"))
		require.True(t, AcquireChannelHealthKey(ctx, "key-a"))
		common.SetContextKey(ctx, constant.ContextKeyChannelKey, "key-a")
		RecordChannelCircuitFailure(ctx, channel.Id, "gpt-test", "/v1/responses", ChannelFailureRateLimited)
	}
	state := routeStateForTestWithChannel(channel, "gpt-test", "/v1/responses")
	require.Equal(t, channelHealthEstablishedCapacity, state.Capacity)
	require.True(t, state.OpenUntil.IsZero())
	require.False(t, HasDueChannelHealthProbe())
	identity := buildChannelHealthIdentity(channel, 0, "gpt-test", "/v1/responses", channelCircuitNow())
	memoryChannelHealth.Keys.RLock()
	keyState := memoryChannelHealth.Keys.States[keyHealthRouteKey(identity, "key-a")]
	memoryChannelHealth.Keys.RUnlock()
	require.Equal(t, channelHealthEstablishedCapacity, keyState.Capacity)
}

func TestAdmissionSkipsFullRouteAndReleasesCapacity(t *testing.T) {
	setupChannelHealthTest(t)
	channel := &model.Channel{Id: 29, Key: "key-a", CreatedTime: channelCircuitNow().Add(-time.Hour).Unix()}
	identity := buildChannelHealthIdentity(channel, 0, "gpt-test", "/v1/responses", channelCircuitNow())
	shard := channelHealthShardFor(identity.RouteKey)
	shard.Lock()
	state := getRouteHealthStateLocked(shard, identity, channelCircuitNow())
	state.Capacity = 2
	shard.Unlock()

	first, _ := gin.CreateTestContext(httptest.NewRecorder())
	second, _ := gin.CreateTestContext(httptest.NewRecorder())
	third, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.True(t, AllowChannelHealthAttempt(first, channel, "gpt-test", "/v1/responses"))
	require.True(t, AllowChannelHealthAttempt(second, channel, "gpt-test", "/v1/responses"))
	require.False(t, AllowChannelHealthAttempt(third, channel, "gpt-test", "/v1/responses"))

	ReleaseChannelCircuitProbe(first, channel.Id, "gpt-test", "/v1/responses")
	require.True(t, AllowChannelHealthAttempt(third, channel, "gpt-test", "/v1/responses"))
}

func TestReleaseCurrentReservationClearsRouteAndKeyInFlight(t *testing.T) {
	setupChannelHealthTest(t)
	channel := &model.Channel{Id: 29, Key: "key-a", CreatedTime: channelCircuitNow().Add(-time.Hour).Unix()}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	require.True(t, AllowChannelHealthAttempt(ctx, channel, "gpt-test", "/v1/responses"))
	require.True(t, AcquireChannelHealthKey(ctx, "key-a"))
	ReleaseCurrentChannelHealthReservation(ctx)

	require.Equal(t, 0, routeStateForTestWithChannel(channel, "gpt-test", "/v1/responses").InFlight)
	identity := buildChannelHealthIdentity(channel, 0, "gpt-test", "/v1/responses", channelCircuitNow())
	memoryChannelHealth.Keys.RLock()
	keyState := memoryChannelHealth.Keys.States[keyHealthRouteKey(identity, "key-a")]
	memoryChannelHealth.Keys.RUnlock()
	require.Equal(t, 0, keyState.InFlight)

	// A second cleanup is a no-op and cannot decrement a later reservation.
	ReleaseCurrentChannelHealthReservation(ctx)
	require.Equal(t, 0, routeStateForTestWithChannel(channel, "gpt-test", "/v1/responses").InFlight)
}

func TestConcurrentAdmissionNeverExceedsRouteCapacity(t *testing.T) {
	setupChannelHealthTest(t)
	channel := &model.Channel{Id: 29, Key: "key-a", CreatedTime: channelCircuitNow().Add(-time.Hour).Unix()}
	identity := buildChannelHealthIdentity(channel, 0, "gpt-test", "/v1/responses", channelCircuitNow())
	shard := channelHealthShardFor(identity.RouteKey)
	shard.Lock()
	state := getRouteHealthStateLocked(shard, identity, channelCircuitNow())
	state.Capacity = 32
	shard.Unlock()

	const callers = 200
	release := make(chan struct{})
	var attempted sync.WaitGroup
	var completed sync.WaitGroup
	var admitted atomic.Int64
	attempted.Add(callers)
	completed.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer completed.Done()
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			allowed := AllowChannelHealthAttempt(ctx, channel, "gpt-test", "/v1/responses")
			if allowed {
				admitted.Add(1)
			}
			attempted.Done()
			if !allowed {
				return
			}
			<-release
			RecordChannelCircuitSuccess(ctx, channel.Id, "gpt-test", "/v1/responses")
		}()
	}
	attempted.Wait()
	require.Equal(t, int64(32), admitted.Load())
	close(release)
	completed.Wait()
	require.Equal(t, 0, routeStateForTestWithChannel(channel, "gpt-test", "/v1/responses").InFlight)
}

func routeStateForTestWithChannel(channel *model.Channel, modelName string, requestPath string) channelRouteHealthState {
	identity := buildChannelHealthIdentity(channel, 0, modelName, requestPath, channelCircuitNow())
	shard := channelHealthShardFor(identity.RouteKey)
	shard.Lock()
	defer shard.Unlock()
	if state := shard.Routes[identity.RouteKey]; state != nil {
		return *state
	}
	return channelRouteHealthState{}
}

func TestSingleSolPoolCapabilityFailureDoesNotIsolateKey(t *testing.T) {
	setupChannelHealthTest(t)
	baseURL := "https://first.example.com"
	channel := &model.Channel{
		Id:      29,
		Key:     "key-a\nkey-b",
		BaseURL: &baseURL,
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true,
		},
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.True(t, AllowChannelHealthAttempt(ctx, channel, "gpt-5.6-sol", "/v1/responses"))
	common.SetContextKey(ctx, constant.ContextKeyChannelKey, "key-a")
	RecordChannelCircuitFailure(ctx, channel.Id, "gpt-5.6-sol", "/v1/responses", ChannelFailureKeyCapability)

	require.Empty(t, ChannelHealthKeyExclusions(channel, "gpt-5.6-sol", "/v1/responses", nil))
	require.Equal(t, channelHealthEstablishedCapacity, routeStateForTestWithChannel(channel, "gpt-5.6-sol", "/v1/responses").Capacity)
}

func TestPoolCapabilityFailuresDoNotOpenRoute(t *testing.T) {
	setupChannelHealthTest(t)
	channel := &model.Channel{Id: 29, Key: "key-a", CreatedTime: channelCircuitNow().Add(-time.Hour).Unix()}
	for i := 0; i < 97; i++ {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		require.True(t, AllowChannelHealthAttempt(ctx, channel, "gpt-5.6-sol", "/v1/responses"))
		RecordChannelCircuitSuccess(ctx, channel.Id, "gpt-5.6-sol", "/v1/responses")
	}
	for i := 0; i < 3; i++ {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		require.True(t, AllowChannelHealthAttempt(ctx, channel, "gpt-5.6-sol", "/v1/responses"))
		RecordChannelCircuitFailure(ctx, channel.Id, "gpt-5.6-sol", "/v1/responses", ChannelFailureKeyCapability)
	}
	require.True(t, AllowChannelHealthAttempt(nil, channel, "gpt-5.6-sol", "/v1/responses"))

	setupChannelHealthTest(t)
	for i := 0; i < channelHealthSlowSingleRouteFailures; i++ {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		require.True(t, AllowChannelHealthAttempt(ctx, channel, "gpt-5.6-sol", "/v1/responses"))
		RecordChannelCircuitFailure(ctx, channel.Id, "gpt-5.6-sol", "/v1/responses", ChannelFailureKeyCapability)
	}
	require.False(t, HasDueChannelHealthProbe())
	require.True(t, AllowChannelHealthAttempt(nil, channel, "gpt-5.6-sol", "/v1/responses"))
	require.True(t, AllowChannelHealthAttempt(nil, channel, "gpt-5.6-terra", "/v1/responses"))
}

func TestPoolFailuresNeverOpenWholeChannel(t *testing.T) {
	setupChannelHealthTest(t)
	for _, modelName := range []string{"gpt-a", "gpt-b"} {
		for i := 0; i < 4; i++ {
			recordLegacyRouteOutcome(t, 8, modelName, "/v1/responses", ChannelFailurePoolAccount)
		}
	}
	require.True(t, AllowChannelCircuitAttempt(nil, 8, "healthy-model", "/v1/embeddings"))
}

func TestChangedChannelKeepsSlowStartCapacity(t *testing.T) {
	setupChannelHealthTest(t)
	firstURL := "https://first.example.com"
	channel := &model.Channel{Id: 29, Key: "key-a", BaseURL: &firstURL, CreatedTime: channelCircuitNow().Add(-time.Hour).Unix()}
	firstIdentity := buildChannelHealthIdentity(channel, 0, "gpt-test", "/v1/responses", channelCircuitNow())
	require.Equal(t, channelHealthEstablishedCapacity, firstIdentity.InitialCapacity)

	replacementURL := "https://replacement.example.com"
	channel.BaseURL = &replacementURL
	changedIdentity := buildChannelHealthIdentity(channel, 0, "gpt-test", "/v1/responses", channelCircuitNow())
	require.Equal(t, channelHealthNewCapacity, changedIdentity.InitialCapacity)
	require.Equal(t, channelHealthNewCapacity, buildChannelHealthIdentity(channel, 0, "gpt-test", "/v1/responses", channelCircuitNow()).InitialCapacity)
}

func TestUpstreamAffectingSettingsChangeChannelFingerprint(t *testing.T) {
	setupChannelHealthTest(t)
	baseURL := "https://first.example.com"
	base := model.Channel{
		Id:          29,
		Type:        constant.ChannelTypeOpenAI,
		Key:         "key-a",
		BaseURL:     &baseURL,
		Models:      "gpt-test",
		CreatedTime: channelCircuitNow().Add(-time.Hour).Unix(),
	}

	tests := []struct {
		name   string
		mutate func(*model.Channel)
	}{
		{"organization", func(channel *model.Channel) { channel.OpenAIOrganization = common.GetPointer("org-b") }},
		{"test model", func(channel *model.Channel) { channel.TestModel = common.GetPointer("gpt-probe") }},
		{"provider other", func(channel *model.Channel) { channel.Other = `{"default":"us-east-1"}` }},
		{"setting", func(channel *model.Channel) { channel.Setting = common.GetPointer(`{"force_format":true}`) }},
		{"parameter override", func(channel *model.Channel) { channel.ParamOverride = common.GetPointer(`{"temperature":0}`) }},
		{"header override", func(channel *model.Channel) { channel.HeaderOverride = common.GetPointer(`{"X-Test":"changed"}`) }},
		{"other settings", func(channel *model.Channel) { channel.OtherSettings = `{"api_version":"v2"}` }},
		{"status mapping", func(channel *model.Channel) { channel.StatusCodeMapping = common.GetPointer(`{"503":502}`) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := base
			before := channelConfigFingerprint(&channel)
			tt.mutate(&channel)
			require.NotEqual(t, before, channelConfigFingerprint(&channel))
		})
	}
}

func TestObservedChannelConfigsExpireAfterInactivity(t *testing.T) {
	now := setupChannelHealthTest(t)
	stale := &model.Channel{Id: 29, Key: "stale-key", CreatedTime: now.Add(-time.Hour).Unix()}
	retained := &model.Channel{Id: 37, Key: "retained-key", CreatedTime: now.Add(-time.Hour).Unix()}
	buildChannelHealthIdentity(stale, 0, "gpt-test", "/v1/responses", *now)
	buildChannelHealthIdentity(retained, 0, "gpt-test", "/v1/responses", *now)

	*now = now.Add(channelHealthStateTTL - time.Minute)
	buildChannelHealthIdentity(retained, 0, "gpt-test", "/v1/responses", *now)
	*now = now.Add(2 * time.Minute)
	trigger := &model.Channel{Id: 54, Key: "trigger-key", CreatedTime: now.Add(-time.Hour).Unix()}
	buildChannelHealthIdentity(trigger, 0, "gpt-test", "/v1/responses", *now)

	memoryChannelHealth.Configs.RLock()
	defer memoryChannelHealth.Configs.RUnlock()
	_, staleExists := memoryChannelHealth.Configs.Observed[stale.Id]
	_, retainedExists := memoryChannelHealth.Configs.Observed[retained.Id]
	_, triggerExists := memoryChannelHealth.Configs.Observed[trigger.Id]
	require.False(t, staleExists)
	require.True(t, retainedExists)
	require.True(t, triggerExists)
}

func TestDisabledKeyDoesNotHideRemainingHealthyKey(t *testing.T) {
	setupChannelHealthTest(t)
	channel := &model.Channel{
		Id:  29,
		Key: "disabled-key\nhealthy-key",
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:         true,
			MultiKeyStatusList: map[int]int{0: common.ChannelStatusManuallyDisabled, 1: common.ChannelStatusEnabled},
		},
	}
	identity := buildChannelHealthIdentity(channel, 0, "gpt-test", "/v1/responses", channelCircuitNow())
	setChannelKeyHealth(identity, "disabled-key", true, time.Minute, channelCircuitNow())
	require.True(t, hasHealthyChannelKey(channel, identity, channelCircuitNow()))
}

func TestChannelSelectionSkipsOpenRoute(t *testing.T) {
	setupChannelHealthTest(t)
	originalDB := model.DB
	originalLogDB := model.LOG_DB
	originalMainDatabaseType := common.MainDatabaseType()
	originalLogDatabaseType := common.LogDatabaseType()
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		model.DB = originalDB
		model.LOG_DB = originalLogDB
		common.SetMainDatabaseType(originalMainDatabaseType)
		common.SetLogDatabaseType(originalLogDatabaseType)
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	require.NoError(t, model.InitLogDB())

	highPriority := int64(10)
	lowPriority := int64(1)
	weight := uint(1)
	high := &model.Channel{Id: 37, Key: "high", Group: "default", Models: "gpt-test", Status: common.ChannelStatusEnabled, Priority: &highPriority, Weight: &weight}
	fallback := &model.Channel{Id: 54, Key: "fallback", Group: "default", Models: "gpt-test", Status: common.ChannelStatusEnabled, Priority: &lowPriority, Weight: &weight}
	for _, channel := range []*model.Channel{high, fallback} {
		require.NoError(t, db.Create(channel).Error)
		require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "gpt-test", ChannelId: channel.Id, Enabled: true, Priority: channel.Priority, Weight: weight}).Error)
	}
	scheduleRouteProbeForTest(t, high, 0, "gpt-test", "/v1/responses", ChannelFailureTransient)
	targets := ClaimDueChannelHealthProbes(1)
	require.Len(t, targets, 1)
	CompleteChannelHealthProbe(targets[0], ChannelHealthProbeResult{Class: ChannelFailureTransient})

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	selected, _, err := CacheGetRandomSatisfiedChannel(&RetryParam{Ctx: ctx, TokenGroup: "default", ModelName: "gpt-test", RequestPath: "/v1/responses"})
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, fallback.Id, selected.Id)
}

func TestChannelRouteAttemptsAreBoundedAndExcludeSecrets(t *testing.T) {
	setupChannelHealthTest(t)
	now := channelCircuitNow()
	channelCircuitNow = func() time.Time { return now }

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	for i := 0; i < maxLoggedChannelAttempts+2; i++ {
		BeginChannelRouteAttempt(ctx, 10+i, i)
		now = now.Add(25 * time.Millisecond)
		FinishChannelRouteAttempt(ctx, http.StatusServiceUnavailable, ChannelFailureDecision{Class: ChannelFailureTransient, Reason: "retryable_status", Retry: true})
	}

	attempts := GetChannelRouteAttempts(ctx, false)
	require.Len(t, attempts, maxLoggedChannelAttempts)
	require.Equal(t, int64(25), attempts[0].DurationMS)
	require.Equal(t, http.StatusServiceUnavailable, attempts[0].StatusCode)
	require.Equal(t, 0, attempts[0].KeyIndex)

	adminInfo := make(map[string]interface{})
	AppendChannelRouteAttemptsAdminInfo(ctx, adminInfo, false)
	require.Equal(t, attempts, adminInfo["route_attempts"])
}

func BenchmarkChannelHealthSuccessfulRequest(b *testing.B) {
	gin.SetMode(gin.TestMode)
	resetMemoryChannelHealth()
	channel := &model.Channel{Id: 29, Key: "key-a", CreatedTime: time.Now().Add(-time.Hour).Unix()}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			if !AllowChannelHealthAttempt(ctx, channel, "gpt-test", "/v1/responses") {
				continue
			}
			_ = ChannelHealthKeyExclusions(channel, "gpt-test", "/v1/responses", nil)
			RecordChannelCircuitSuccess(ctx, channel.Id, "gpt-test", "/v1/responses")
		}
	})
}
