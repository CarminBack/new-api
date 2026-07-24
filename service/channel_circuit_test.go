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
	now := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	channelCircuitNow = func() time.Time { return now }
	resetMemoryChannelHealth()
	resetChannelRetryBudgetForTest()
	t.Cleanup(func() {
		channelCircuitNow = originalNow
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

func TestWilsonLowerBoundMatchesHistoricalScenarios(t *testing.T) {
	require.Less(t, wilsonLowerBound(5, 91), channelHealthWarningLowerBound)
	require.Greater(t, wilsonLowerBound(4, 4), channelHealthOpenLowerBound)
	require.Less(t, wilsonLowerBound(3, 3), channelHealthOpenLowerBound)
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
	require.False(t, routeHealthWarning(29, "gpt-5.6-sol", "/v1/responses"))
	ReleaseChannelCircuitProbe(ctx, 29, "gpt-5.6-sol", "/v1/responses")
}

func TestCompleteFailureOpensRouteAfterConfidenceIsSufficient(t *testing.T) {
	setupChannelHealthTest(t)
	for i := 0; i < 3; i++ {
		recordLegacyRouteOutcome(t, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.True(t, AllowChannelCircuitAttempt(ctx, 37, "gpt-test", "/v1/responses"))
	RecordChannelCircuitFailure(ctx, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	require.False(t, AllowChannelCircuitAttempt(nil, 37, "gpt-test", "/v1/responses"))
	require.True(t, AllowChannelCircuitAttempt(nil, 37, "other-model", "/v1/responses"))
}

func TestHalfOpenProbeIsSingleAndRecoversGradually(t *testing.T) {
	now := setupChannelHealthTest(t)
	for i := 0; i < 4; i++ {
		recordLegacyRouteOutcome(t, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	}
	*now = now.Add(channelHealthOpenFor + time.Millisecond)
	probeCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.True(t, AllowChannelCircuitAttempt(probeCtx, 37, "gpt-test", "/v1/responses"))
	require.False(t, AllowChannelCircuitAttempt(nil, 37, "gpt-test", "/v1/responses"))

	RecordChannelCircuitSuccess(probeCtx, 37, "gpt-test", "/v1/responses")
	state := routeStateForTest(37, "gpt-test", "/v1/responses")
	require.Equal(t, channelHealthRecoveryCapacity, state.Capacity)
	require.True(t, AllowChannelCircuitAttempt(nil, 37, "gpt-test", "/v1/responses"))
}

func TestFailedHalfOpenProbeReopensForTwoMinutes(t *testing.T) {
	now := setupChannelHealthTest(t)
	for i := 0; i < 4; i++ {
		recordLegacyRouteOutcome(t, 37, "gpt-test", "/v1/responses", ChannelFailureUncertain)
	}
	*now = now.Add(channelHealthOpenFor + time.Millisecond)
	probeCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.True(t, AllowChannelCircuitAttempt(probeCtx, 37, "gpt-test", "/v1/responses"))
	RecordChannelCircuitFailure(probeCtx, 37, "gpt-test", "/v1/responses", ChannelFailureUncertain)
	require.False(t, AllowChannelCircuitAttempt(nil, 37, "gpt-test", "/v1/responses"))

	*now = now.Add(channelHealthOpenFor + time.Millisecond)
	require.True(t, AllowChannelCircuitAttempt(nil, 37, "gpt-test", "/v1/responses"))
}

func TestTwoIndependentUnhealthyRoutesOpenWholeChannel(t *testing.T) {
	setupChannelHealthTest(t)
	for _, route := range []struct {
		model string
		path  string
	}{{"gpt-a", "/v1/responses"}, {"gpt-b", "/v1/chat/completions"}} {
		for i := 0; i < 4; i++ {
			recordLegacyRouteOutcome(t, 8, route.model, route.path, ChannelFailureUncertain)
		}
	}
	require.False(t, AllowChannelCircuitAttempt(nil, 8, "healthy-model", "/v1/embeddings"))
	require.True(t, AllowChannelCircuitAttempt(nil, 9, "healthy-model", "/v1/embeddings"))
}

func TestRateLimitReducesCapacityOncePerBucketWithoutOpening(t *testing.T) {
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
	identity := buildChannelHealthIdentity(channel, 0, "gpt-test", "/v1/responses", channelCircuitNow())
	memoryChannelHealth.Keys.RLock()
	keyState := memoryChannelHealth.Keys.States[keyHealthRouteKey(identity, "key-a")]
	memoryChannelHealth.Keys.RUnlock()
	require.Equal(t, 102, keyState.Capacity)
	require.Equal(t, 0, keyState.InFlight)
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

func TestSolCapabilityIsolationUsesKeyAndConfigurationFingerprint(t *testing.T) {
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

	excluded := ChannelHealthKeyExclusions(channel, "gpt-5.6-sol", "/v1/responses", nil)
	require.Contains(t, excluded, 0)
	require.NotContains(t, excluded, 1)

	changedURL := "https://replacement.example.com"
	channel.BaseURL = &changedURL
	require.Empty(t, ChannelHealthKeyExclusions(channel, "gpt-5.6-sol", "/v1/responses", nil))
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
	for i := 0; i < 4; i++ {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		require.True(t, AllowChannelHealthAttempt(ctx, high, "gpt-test", "/v1/responses"))
		RecordChannelCircuitFailure(ctx, high.Id, "gpt-test", "/v1/responses", ChannelFailureTransient)
	}

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
