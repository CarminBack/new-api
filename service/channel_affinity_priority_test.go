package service

import (
	"fmt"
	"net/http/httptest"
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

func TestPriorityAffinityRecoveryAndStaleWriteProtection(t *testing.T) {
	setupChannelHealthTest(t)
	oldDB := model.DB
	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	setting := operation_setting.GetChannelAffinitySetting()
	require.NotNil(t, setting)
	oldEnabled := setting.Enabled
	setting.Enabled = true
	t.Cleanup(func() {
		model.DB = oldDB
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
		if oldMemoryCacheEnabled && oldDB != nil {
			model.InitChannelCache()
		}
		setting.Enabled = oldEnabled
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	model.DB = db
	common.MemoryCacheEnabled = true

	highPriority := int64(10)
	lowPriority := int64(1)
	weight := uint(1)
	high := &model.Channel{Id: 101, Key: "high", Status: common.ChannelStatusEnabled, Priority: &highPriority, Weight: &weight, Group: "default,fallback-group", Models: "gpt-test"}
	low := &model.Channel{Id: 102, Key: "low", Status: common.ChannelStatusEnabled, Priority: &lowPriority, Weight: &weight, Group: "default,primary-group", Models: "gpt-test"}
	for _, channel := range []*model.Channel{high, low} {
		require.NoError(t, db.Create(channel).Error)
		require.NoError(t, db.Create(&model.Ability{
			Group: "default", Model: "gpt-test", ChannelId: channel.Id, Enabled: true,
			Priority: channel.Priority, Weight: weight,
		}).Error)
	}
	require.NoError(t, db.Create(&model.Ability{
		Group: "primary-group", Model: "gpt-test", ChannelId: low.Id, Enabled: true, Priority: low.Priority, Weight: weight,
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group: "fallback-group", Model: "gpt-test", ChannelId: high.Id, Enabled: true, Priority: high.Priority, Weight: weight,
	}).Error)
	model.InitChannelCache()

	cacheKeySuffix := fmt.Sprintf("priority-recovery-%d", time.Now().UnixNano())
	cacheKey := channelAffinityCacheNamespace + ":" + cacheKeySuffix
	cache := getChannelAffinityCache()
	require.NoError(t, cache.SetWithTTL(cacheKeySuffix, low.Id, time.Minute))
	t.Cleanup(func() { _, _ = cache.DeleteMany([]string{cacheKey}) })

	identity := buildChannelHealthIdentity(high, 0, "gpt-test", "/v1/responses", channelCircuitNow())
	shard := channelHealthShardFor(identity.RouteKey)
	shard.Lock()
	state := getRouteHealthStateLocked(shard, identity, channelCircuitNow())
	state.CapacityBeforeOpen = channelHealthEstablishedCapacity
	startRouteRecoveryLocked(state, channelCircuitNow())
	shard.Unlock()

	priority, found, err := HighestRoutableChannelPriority("default", &RetryParam{ModelName: "gpt-test", RequestPath: "/v1/responses"})
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, highPriority, priority, "recovering high-priority route receives bounded canaries")
	group, found, err := FirstRoutableChannelGroup([]string{"primary-group", "fallback-group"}, &RetryParam{ModelName: "gpt-test", RequestPath: "/v1/responses"})
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "primary-group", group, "priority comparison cannot cross auto-group ordering")

	requestContext := affinityRecordContextForTest(cacheKey, low.Id)
	RecordChannelAffinity(requestContext, high.Id)
	require.Equal(t, low.Id, affinityCacheValueForTest(t, cache, cacheKey), "unconfirmed recovery must not claim affinity")

	for range channelHealthRecoverySuccessTarget {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		require.True(t, AllowChannelHealthAttempt(ctx, high, "gpt-test", "/v1/responses"))
		RecordChannelCircuitSuccess(ctx, high.Id, "gpt-test", "/v1/responses")
	}
	require.True(t, IsChannelPriorityAffinityReady(high, "gpt-test", "/v1/responses"))
	RecordChannelAffinity(requestContext, high.Id)
	require.Equal(t, high.Id, affinityCacheValueForTest(t, cache, cacheKey))

	staleLowRequest := affinityRecordContextForTest(cacheKey, low.Id)
	firstBusy, _ := gin.CreateTestContext(httptest.NewRecorder())
	secondBusy, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.True(t, AllowChannelHealthAttempt(firstBusy, high, "gpt-test", "/v1/responses"))
	require.True(t, AllowChannelHealthAttempt(secondBusy, high, "gpt-test", "/v1/responses"))
	require.False(t, IsChannelHealthAvailable(high, "gpt-test", "/v1/responses"))
	RecordChannelAffinity(staleLowRequest, low.Id)
	require.Equal(t, high.Id, affinityCacheValueForTest(t, cache, cacheKey), "capacity saturation cannot permit a stale downgrade")
	ReleaseCurrentChannelHealthReservation(firstBusy)
	ReleaseCurrentChannelHealthReservation(secondBusy)

	RecordChannelAffinity(staleLowRequest, low.Id)
	require.Equal(t, high.Id, affinityCacheValueForTest(t, cache, cacheKey), "stale low-priority completion cannot overwrite recovered affinity")

	failedHighRequest := affinityRecordContextForTest(cacheKey, high.Id)
	RecordChannelAffinity(failedHighRequest, low.Id)
	require.Equal(t, low.Id, affinityCacheValueForTest(t, cache, cacheKey), "a request that started on the high-priority channel may fall back")
}

func affinityRecordContextForTest(cacheKey string, originalChannelID int) *gin.Context {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	setChannelAffinityContext(ctx, channelAffinityMeta{
		CacheKey: cacheKey, TTLSeconds: 60, RuleName: "priority-test", UsingGroup: "default",
		ModelName: "gpt-test", RequestPath: "/v1/responses", OriginalChannelID: originalChannelID,
	})
	return ctx
}

func affinityCacheValueForTest(t *testing.T, cache interface {
	Get(string) (int, bool, error)
}, key string) int {
	t.Helper()
	value, found, err := cache.Get(key)
	require.NoError(t, err)
	require.True(t, found)
	return value
}
