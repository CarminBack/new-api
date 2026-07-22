package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestChannelCircuitRedisIntegration(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR is not set")
	}
	originalRDB := common.RDB
	originalRedisEnabled := common.RedisEnabled
	originalNow := channelCircuitNow
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	client := redis.NewClient(&redis.Options{Addr: addr})
	require.NoError(t, client.Ping(context.Background()).Err())
	require.NoError(t, client.FlushDB(context.Background()).Err())
	common.RDB = client
	common.RedisEnabled = true
	channelCircuitNow = func() time.Time { return now }
	t.Cleanup(func() {
		_ = client.FlushDB(context.Background()).Err()
		_ = client.Close()
		common.RDB = originalRDB
		common.RedisEnabled = originalRedisEnabled
		channelCircuitNow = originalNow
	})

	for i := 0; i < channelCircuitPolicyFor(ChannelFailureUncertain).Threshold; i++ {
		RecordChannelCircuitFailure(nil, 8, "gpt-test", "/v1/responses", ChannelFailureUncertain)
	}
	require.False(t, AllowChannelCircuitAttempt(nil, 8, "gpt-test", "/v1/responses"))

	now = now.Add(5*time.Minute + time.Millisecond)
	probeCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.True(t, AllowChannelCircuitAttempt(probeCtx, 8, "gpt-test", "/v1/responses"))
	require.False(t, AllowChannelCircuitAttempt(nil, 8, "gpt-test", "/v1/responses"))
	RecordChannelCircuitSuccess(probeCtx, 8, "gpt-test", "/v1/responses")
	require.True(t, AllowChannelCircuitAttempt(nil, 8, "gpt-test", "/v1/responses"))
}

func resetMemoryChannelCircuitsForTest() {
	memoryChannelCircuits.Lock()
	memoryChannelCircuits.states = make(map[string]memoryChannelCircuitState)
	memoryChannelCircuits.Unlock()
}

func TestChannelSelectionSkipsOpenCircuit(t *testing.T) {
	originalDB := model.DB
	originalLogDB := model.LOG_DB
	originalMainDatabaseType := common.MainDatabaseType()
	originalLogDatabaseType := common.LogDatabaseType()
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalRedisEnabled := common.RedisEnabled
	common.MemoryCacheEnabled = false
	common.RedisEnabled = false
	resetMemoryChannelCircuitsForTest()
	t.Cleanup(func() {
		model.DB = originalDB
		model.LOG_DB = originalLogDB
		common.SetMainDatabaseType(originalMainDatabaseType)
		common.SetLogDatabaseType(originalLogDatabaseType)
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		common.RedisEnabled = originalRedisEnabled
		resetMemoryChannelCircuitsForTest()
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
	for _, channel := range []*model.Channel{
		{Id: 37, Key: "high", Group: "default", Status: common.ChannelStatusEnabled, Priority: &highPriority, Weight: &weight},
		{Id: 54, Key: "fallback", Group: "default", Status: common.ChannelStatusEnabled, Priority: &lowPriority, Weight: &weight},
	} {
		require.NoError(t, db.Create(channel).Error)
		require.NoError(t, db.Create(&model.Ability{
			Group: "default", Model: "gpt-test", ChannelId: channel.Id,
			Enabled: true, Priority: channel.Priority, Weight: weight,
		}).Error)
	}

	for i := 0; i < channelCircuitPolicyFor(ChannelFailureTransient).Threshold; i++ {
		RecordChannelCircuitFailure(nil, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	selected, _, err := CacheGetRandomSatisfiedChannel(&RetryParam{
		Ctx: ctx, TokenGroup: "default", ModelName: "gpt-test", RequestPath: "/v1/responses",
	})
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, 54, selected.Id)
}

func TestChannelCircuitOpensProbesAndRecovers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalRedisEnabled := common.RedisEnabled
	originalNow := channelCircuitNow
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	common.RedisEnabled = false
	channelCircuitNow = func() time.Time { return now }
	resetMemoryChannelCircuitsForTest()
	t.Cleanup(func() {
		common.RedisEnabled = originalRedisEnabled
		channelCircuitNow = originalNow
		resetMemoryChannelCircuitsForTest()
	})

	for i := 0; i < 4; i++ {
		RecordChannelCircuitFailure(nil, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	}
	require.True(t, AllowChannelCircuitAttempt(nil, 37, "gpt-test", "/v1/responses"))

	RecordChannelCircuitFailure(nil, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	require.False(t, AllowChannelCircuitAttempt(nil, 37, "gpt-test", "/v1/responses"))
	require.True(t, AllowChannelCircuitAttempt(nil, 37, "other-model", "/v1/responses"), "a model-specific failure must not block other models")

	now = now.Add(2*time.Minute + time.Millisecond)
	probeCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.True(t, AllowChannelCircuitAttempt(probeCtx, 37, "gpt-test", "/v1/responses"))
	require.False(t, AllowChannelCircuitAttempt(nil, 37, "gpt-test", "/v1/responses"), "only one half-open probe is allowed")

	RecordChannelCircuitSuccess(probeCtx, 37, "gpt-test", "/v1/responses")
	require.True(t, AllowChannelCircuitAttempt(nil, 37, "gpt-test", "/v1/responses"))
}

func TestFailedHalfOpenProbeReopensCircuitImmediately(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalRedisEnabled := common.RedisEnabled
	originalNow := channelCircuitNow
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	common.RedisEnabled = false
	channelCircuitNow = func() time.Time { return now }
	resetMemoryChannelCircuitsForTest()
	t.Cleanup(func() {
		common.RedisEnabled = originalRedisEnabled
		channelCircuitNow = originalNow
		resetMemoryChannelCircuitsForTest()
	})

	for i := 0; i < channelCircuitPolicyFor(ChannelFailureTransient).Threshold; i++ {
		RecordChannelCircuitFailure(nil, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	}
	now = now.Add(2*time.Minute + time.Millisecond)
	probeCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.True(t, AllowChannelCircuitAttempt(probeCtx, 37, "gpt-test", "/v1/responses"))
	RecordChannelCircuitFailure(probeCtx, 37, "gpt-test", "/v1/responses", ChannelFailureTransient)
	require.False(t, AllowChannelCircuitAttempt(nil, 37, "gpt-test", "/v1/responses"))
}

func TestUncertainFailuresOpenCircuitAtLowerThreshold(t *testing.T) {
	originalRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	resetMemoryChannelCircuitsForTest()
	t.Cleanup(func() {
		common.RedisEnabled = originalRedisEnabled
		resetMemoryChannelCircuitsForTest()
	})

	RecordChannelCircuitFailure(nil, 8, "gpt-test", "/v1/responses", ChannelFailureUncertain)
	require.True(t, AllowChannelCircuitAttempt(nil, 8, "gpt-test", "/v1/responses"))
	RecordChannelCircuitFailure(nil, 8, "gpt-test", "/v1/responses", ChannelFailureUncertain)
	require.False(t, AllowChannelCircuitAttempt(nil, 8, "gpt-test", "/v1/responses"))
}

func TestChannelRouteAttemptsAreBoundedAndExcludeSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalNow := channelCircuitNow
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	channelCircuitNow = func() time.Time { return now }
	t.Cleanup(func() { channelCircuitNow = originalNow })

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	for i := 0; i < maxLoggedChannelAttempts+2; i++ {
		BeginChannelRouteAttempt(ctx, 10+i, i)
		now = now.Add(25 * time.Millisecond)
		FinishChannelRouteAttempt(ctx, http.StatusServiceUnavailable, ChannelFailureDecision{
			Class:  ChannelFailureTransient,
			Reason: "retryable_status",
			Retry:  true,
		})
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
