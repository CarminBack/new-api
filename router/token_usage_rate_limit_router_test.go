package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Canvas calls the token usage endpoints from one server IP for every user.
// The critical limit must count per token so one user cannot starve the rest.
func TestTokenUsageRoutesRateLimitPerTokenNotSharedIP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousDB, previousRedis := model.DB, common.RedisEnabled
	previousEnable, previousNum, previousDuration := common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	// A named shared-cache database: plain ":memory:" gives each pooled connection its own empty database.
	database, err := gorm.Open(sqlite.Open("file:token_usage_rate_limit?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(&model.User{}, &model.Token{}))
	previousLogDB, previousMaster := model.LOG_DB, common.IsMasterNode
	model.DB, common.RedisEnabled, common.IsMasterNode = database, false, false
	// InitLogDB reuses DB and initialises the dialect-specific column names (for example `key`).
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitLogDB())
	common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = true, 2, 60
	t.Cleanup(func() {
		model.DB, model.LOG_DB, common.RedisEnabled, common.IsMasterNode = previousDB, previousLogDB, previousRedis, previousMaster
		common.SetDatabaseTypes(previousMain, previousLog)
		if sqlDB, err := database.DB(); err == nil {
			_ = sqlDB.Close()
		}
		common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = previousEnable, previousNum, previousDuration
	})

	user := model.User{Username: "usage-limit", Status: common.UserStatusEnabled, Quota: 100, AffCode: "usage-limit-aff"}
	require.NoError(t, database.Create(&user).Error)
	keys := []string{"usagelimitfirsttoken0000000000000000000000000000", "usagelimitsecondtoken000000000000000000000000000"}
	for _, key := range keys {
		require.NoError(t, database.Create(&model.Token{UserId: user.Id, Key: key, Status: common.TokenStatusEnabled, Name: key[:12], ExpiredTime: -1}).Error)
	}

	engine := gin.New()
	SetApiRouter(engine)
	call := func(key string) int {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/usage/token/balance", nil)
		request.RemoteAddr = "203.0.113.77:5000"
		if key != "" {
			request.Header.Set("Authorization", "Bearer sk-"+key)
		}
		engine.ServeHTTP(recorder, request)
		return recorder.Code
	}

	assert.Equal(t, http.StatusOK, call(keys[0]))
	assert.Equal(t, http.StatusOK, call(keys[0]))
	assert.Equal(t, http.StatusTooManyRequests, call(keys[0]))
	// Same IP, different token: its own budget.
	assert.Equal(t, http.StatusOK, call(keys[1]))
	// Rejected keys are refused by authentication and do not spend any token budget.
	for range 3 {
		assert.Equal(t, http.StatusUnauthorized, call("usagelimitunknowntoken00000000000000000000000000"))
	}
	assert.Equal(t, http.StatusOK, call(keys[1]))
}
