package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func failoverContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
	c.Set("group", "default")
	c.Set("using_group", "default")
	c.Set("original_model", "gpt-test")
	return c
}

func failoverChannels(t *testing.T) (*model.Channel, *model.Channel) {
	t.Helper()
	oldDB, oldCache := model.DB, common.MemoryCacheEnabled
	oldLogDB := model.LOG_DB
	oldMainType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	model.DB, common.MemoryCacheEnabled = db, false
	t.Cleanup(func() {
		model.DB, common.MemoryCacheEnabled = oldDB, oldCache
		model.LOG_DB = oldLogDB
		common.SetMainDatabaseType(oldMainType)
		common.SetLogDatabaseType(oldLogType)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	})
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	require.NoError(t, model.InitLogDB())
	channels := make([]*model.Channel, 0, 2)
	for index, priority := range []int64{10, 1} {
		weight := uint(1)
		baseURL := fmt.Sprintf("https://upstream-%d.example", index)
		channel := &model.Channel{Id: 820 + index, Type: 1, Key: "fixture", Models: "gpt-test", Group: "default", Status: common.ChannelStatusEnabled, Priority: &priority, Weight: &weight, BaseURL: &baseURL}
		require.NoError(t, db.Create(channel).Error)
		require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "gpt-test", ChannelId: channel.Id, Enabled: true, Priority: &priority, Weight: weight}).Error)
		channels = append(channels, channel)
	}
	return channels[0], channels[1]
}

func TestTextCompletedResponseCountsRecoveryAfterClientCloses(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(fmt.Sprint(completed), func(t *testing.T) {
			now := setupChannelHealthTest(t)
			high, _ := failoverChannels(t)
			identity := buildChannelHealthIdentity(high, 0, "gpt-test", "/v1/responses", *now)
			shard := channelHealthShardFor(identity.RouteKey)
			shard.Lock()
			state := getRouteHealthStateLocked(shard, identity, *now)
			startRouteRecoveryLocked(state, *now)
			shard.Unlock()
			for range 3 {
				c := failoverContext()
				ctx, cancel := context.WithCancel(c.Request.Context())
				c.Request = c.Request.WithContext(ctx)
				require.True(t, AllowChannelHealthAttempt(c, high, "gpt-test", "/v1/responses"))
				if completed {
					MarkRequestPolicySuccess(c, nil)
				}
				cancel()
				*now = now.Add(5 * time.Second)
				RecordChannelCircuitSuccess(c, high.Id, "gpt-test", "/v1/responses")
			}
			shard.Lock()
			capacity, inFlight := state.Capacity, state.InFlight
			shard.Unlock()
			require.Zero(t, inFlight)
			if completed {
				require.Equal(t, 2, capacity)
			} else {
				require.Equal(t, 1, capacity)
			}
		})
	}
}

func TestTextFailoverSuspectRouteYieldsBeforeCircuitOpens(t *testing.T) {
	now := setupChannelHealthTest(t)
	high, fallback := failoverChannels(t)
	for range 3 {
		c := failoverContext()
		require.True(t, AllowChannelHealthAttempt(c, high, "gpt-test", "/v1/responses"))
		RecordChannelCircuitFailure(c, high.Id, "gpt-test", "/v1/responses", ChannelFailureTransient)
	}
	c := failoverContext()
	selected, _, err := CacheGetRandomSatisfiedChannel(&RetryParam{Ctx: c, TokenGroup: "default", ModelName: "gpt-test", RequestPath: "/v1/responses"})
	require.NoError(t, err)
	require.Equal(t, fallback.Id, selected.Id)
	ReleaseCurrentChannelHealthReservation(c)
	targets := ClaimDueStandardChannelHealthProbes(1)
	require.Len(t, targets, 1)
	require.False(t, IsChannelHealthAvailable(high, "gpt-test", "/v1/responses"))
	CompleteChannelHealthProbe(targets[0], ChannelHealthProbeResult{Class: ChannelFailureTransient})
	*now = now.Add(2 * time.Minute)
	targets = ClaimDueStandardChannelHealthProbes(1)
	require.Len(t, targets, 1)
	CompleteChannelHealthProbe(targets[0], ChannelHealthProbeResult{Success: true})
	require.True(t, IsChannelHealthAvailable(high, "gpt-test", "/v1/responses"))
	require.False(t, IsChannelPriorityAffinityReady(high, "gpt-test", "/v1/responses"))
}

func TestTextFirstFailoverSurvivesExhaustedRetryBudget(t *testing.T) {
	setupChannelHealthTest(t)
	high, fallback := failoverChannels(t)
	for range 12 {
		require.True(t, allowChannelRetry("gpt-test", "/v1/responses", ChannelFailureTransient))
	}
	require.False(t, allowChannelRetry("gpt-test", "/v1/responses", ChannelFailureTransient))
	c := failoverContext()
	c.Set("channel_id", high.Id)
	RecordChannelPrimaryRequestFor(c, "gpt-test", "/v1/responses")
	require.True(t, AllowChannelRetryFor(c, "gpt-test", "/v1/responses", ChannelFailureTransient, high.Id))
	ExcludeChannelForRequest(c, high.Id)
	selected, _, err := CacheGetRandomSatisfiedChannel(&RetryParam{Ctx: c, TokenGroup: "default", ModelName: "gpt-test", RequestPath: "/v1/responses"})
	require.NoError(t, err)
	require.Equal(t, fallback.Id, selected.Id)
	ReleaseCurrentChannelHealthReservation(c)
	require.False(t, AllowChannelRetryFor(c, "gpt-test", "/v1/responses", ChannelFailureTransient, fallback.Id), "retry chains cannot consume first-failover reserve twice")
	for range 9 {
		require.True(t, allowFirstChannelFailover("gpt-test", "/v1/responses"))
	}
	require.False(t, allowFirstChannelFailover("gpt-test", "/v1/responses"), "reserve is bounded")
}

func TestTextFirstFailoverRequiresAvailableAlternate(t *testing.T) {
	setupChannelHealthTest(t)
	high, fallback := failoverChannels(t)
	for range 12 {
		require.True(t, allowChannelRetry("gpt-test", "/v1/responses", ChannelFailureTransient))
	}
	limited := failoverContext()
	require.True(t, AllowChannelHealthAttempt(limited, fallback, "gpt-test", "/v1/responses"))
	RecordChannelCircuitFailure(limited, fallback.Id, "gpt-test", "/v1/responses", ChannelFailureRateLimited)
	c := failoverContext()
	c.Set("channel_id", high.Id)
	require.False(t, AllowChannelRetryFor(c, "gpt-test", "/v1/responses", ChannelFailureTransient, high.Id), "cooling alternate cannot use the rescue reserve")
}

func TestTextRecoveryCanaryPreservesAffinityUntilStable(t *testing.T) {
	now := setupChannelHealthTest(t)
	high, fallback := failoverChannels(t)
	setting := operation_setting.GetChannelAffinitySetting()
	oldSetting := *setting
	*setting = operation_setting.ChannelAffinitySetting{Enabled: true, SessionMode: "prefer", DefaultTTLSeconds: 3600,
		Rules: []operation_setting.ChannelAffinityRule{{Name: "failover-test", ModelRegex: []string{".*"}, SessionMode: "prefer", IncludeRuleName: true,
			KeySources: []operation_setting.ChannelAffinityKeySource{{Type: "request_header", Key: "X-Affinity-Key"}}}}}
	t.Cleanup(func() { *setting = oldSetting })
	newRequest := func() *gin.Context {
		c := failoverContext()
		c.Request.Header.Set("X-Affinity-Key", t.Name())
		return c
	}
	c := newRequest()
	_, _ = GetPreferredChannelByAffinity(c, "gpt-test", "default")
	RecordChannelAffinity(c, fallback.Id)
	key, _, ok := getChannelAffinityContext(c)
	require.True(t, ok)
	t.Cleanup(func() { _, _ = getChannelAffinityCache().DeleteMany([]string{key}) })
	identity := buildChannelHealthIdentity(high, 0, "gpt-test", "/v1/responses", *now)
	shard := channelHealthShardFor(identity.RouteKey)
	shard.Lock()
	state := getRouteHealthStateLocked(shard, identity, *now)
	startRouteRecoveryLocked(state, *now)
	state.RecoveryTargetCapacity = 2
	shard.Unlock()
	setting.Rules[0].SessionMode = "strict"
	strictCtx := newRequest()
	strictSelected, _, strictErr := SelectChannelForRequest(strictCtx, "gpt-test", &RetryParam{Ctx: strictCtx, TokenGroup: "default", ModelName: "gpt-test", RequestPath: "/v1/responses"})
	require.Nil(t, strictErr)
	require.Equal(t, fallback.Id, strictSelected.Id, "strict sessions never supply cross-channel canaries")
	ReleaseCurrentChannelHealthReservation(strictCtx)
	setting.Rules[0].SessionMode = "prefer"
	for trial := 0; trial < 3; trial++ {
		*now = now.Add(10 * time.Second)
		c = newRequest()
		selected, _, err := SelectChannelForRequest(c, "gpt-test", &RetryParam{Ctx: c, TokenGroup: "default", ModelName: "gpt-test", RequestPath: "/v1/responses"})
		require.Nil(t, err)
		require.Equal(t, high.Id, selected.Id)
		require.True(t, c.GetBool(textRecoveryCanaryKey))
		RecordChannelCircuitSuccess(c, high.Id, "gpt-test", "/v1/responses")
		RecordChannelAffinity(c, high.Id)
		require.False(t, ClearCurrentChannelAffinityCache(c), "canary failure cannot evict the original session")
		cached, found := GetPreferredChannelByAffinity(newRequest(), "gpt-test", "default")
		require.True(t, found)
		require.Equal(t, fallback.Id, cached)
		for range 9 {
			c = newRequest()
			selected, _, err = SelectChannelForRequest(c, "gpt-test", &RetryParam{Ctx: c, TokenGroup: "default", ModelName: "gpt-test", RequestPath: "/v1/responses"})
			require.Nil(t, err)
			require.Equal(t, fallback.Id, selected.Id)
			ReleaseCurrentChannelHealthReservation(c)
		}
	}
	require.False(t, IsChannelPriorityAffinityReady(high, "gpt-test", "/v1/responses"))
	*now = now.Add(30 * time.Second)
	c = newRequest()
	selected, _, err := SelectChannelForRequest(c, "gpt-test", &RetryParam{Ctx: c, TokenGroup: "default", ModelName: "gpt-test", RequestPath: "/v1/responses"})
	require.Nil(t, err)
	require.Equal(t, high.Id, selected.Id)
	require.False(t, c.GetBool(textRecoveryCanaryKey))
	RecordChannelCircuitSuccess(c, high.Id, "gpt-test", "/v1/responses")
	RecordChannelAffinity(c, high.Id)
	cached, found := GetPreferredChannelByAffinity(newRequest(), "gpt-test", "default")
	require.True(t, found)
	require.Equal(t, high.Id, cached)
}

func TestTextFailoverPrefersIndependentHealthyHost(t *testing.T) {
	setupChannelHealthTest(t)
	high, independent := failoverChannels(t)
	priority, weight := int64(5), uint(1)
	alias := &model.Channel{Id: 822, Type: 1, Key: "fixture", Models: "gpt-test", Group: "default", Status: common.ChannelStatusEnabled, Priority: &priority, Weight: &weight, BaseURL: high.BaseURL}
	require.NoError(t, model.DB.Create(alias).Error)
	require.NoError(t, model.DB.Create(&model.Ability{Group: "default", Model: "gpt-test", ChannelId: alias.Id, Enabled: true, Priority: &priority, Weight: weight}).Error)
	c := failoverContext()
	c.Set("channel_id", high.Id)
	require.True(t, AllowChannelRetryFor(c, "gpt-test", "/v1/responses", ChannelFailureTransient, high.Id))
	ExcludeChannelForRequest(c, high.Id)
	selected, _, err := CacheGetRandomSatisfiedChannel(&RetryParam{Ctx: c, TokenGroup: "default", ModelName: "gpt-test", RequestPath: "/v1/responses"})
	require.NoError(t, err)
	require.Equal(t, independent.Id, selected.Id)
	ReleaseCurrentChannelHealthReservation(c)
}

func TestTextConcurrentRateLimitCannotShortenRetryAfter(t *testing.T) {
	now := setupChannelHealthTest(t)
	high, _ := failoverChannels(t)
	first, late := failoverContext(), failoverContext()
	require.True(t, AllowChannelHealthAttempt(first, high, "gpt-test", "/v1/responses"))
	require.True(t, AllowChannelHealthAttempt(late, high, "gpt-test", "/v1/responses"))
	CaptureTextRetryAfter(first, http.Header{"Retry-After": []string{"60"}})
	RecordChannelCircuitFailure(first, high.Id, "gpt-test", "/v1/responses", ChannelFailureRateLimited)
	RecordChannelCircuitFailure(late, high.Id, "gpt-test", "/v1/responses", ChannelFailureRateLimited)
	*now = now.Add(5 * time.Second)
	require.False(t, IsChannelHealthAvailable(high, "gpt-test", "/v1/responses"))
	*now = now.Add(55 * time.Second)
	require.True(t, IsChannelHealthAvailable(high, "gpt-test", "/v1/responses"))
}

func TestTextRateLimitCooldownIsScopedAndBounded(t *testing.T) {
	now := setupChannelHealthTest(t)
	high, _ := failoverChannels(t)
	for _, value := range []string{"60", now.Add(time.Minute).UTC().Format(http.TimeFormat), "999999999", "bad"} {
		c := failoverContext()
		CaptureTextRetryAfter(c, http.Header{"Retry-After": []string{value}})
		require.True(t, AllowChannelHealthAttempt(c, high, "gpt-test", "/v1/responses"))
		RecordChannelCircuitFailure(c, high.Id, "gpt-test", "/v1/responses", ChannelFailureRateLimited)
		require.False(t, IsChannelHealthAvailable(high, "gpt-test", "/v1/responses"))
		require.True(t, IsChannelHealthAvailable(high, "other-model", "/v1/responses"))
		*now = now.Add(2 * time.Minute)
		require.True(t, IsChannelHealthAvailable(high, "gpt-test", "/v1/responses"))
		CaptureTextRetryAfter(c, nil)
		value, _ := c.Get(textRetryAfterKey)
		require.Equal(t, time.Duration(0), value)
	}
}
