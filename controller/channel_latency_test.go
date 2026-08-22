package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGetChannelLatencyGroupsChannelsAndCalculatesStats(t *testing.T) {
	originalDB := model.DB
	t.Cleanup(func() { model.DB = originalDB })

	db, err := gorm.Open(sqlite.Open("file:channel-latency-test?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}))
	model.DB = db

	require.NoError(t, db.Create([]*model.Channel{
		{Id: 1, Key: "key-1", Name: "fast", Group: "Plus,ChatGPT", Status: common.ChannelStatusEnabled, ResponseTime: 100, TestTime: 100},
		{Id: 2, Key: "key-2", Name: "slow", Group: "ChatGPT", Status: common.ChannelStatusEnabled, ResponseTime: 300, TestTime: 200},
		{Id: 3, Key: "key-3", Name: "untested", Group: "Plus", Status: common.ChannelStatusAutoDisabled},
	}).Error)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/channel/latency", nil)
	GetChannelLatency(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Groups []struct {
				Group                 string  `json:"group"`
				ChannelCount          int     `json:"channel_count"`
				EnabledCount          int     `json:"enabled_count"`
				TestedCount           int     `json:"tested_count"`
				AverageResponseTimeMs float64 `json:"average_response_time_ms"`
				MinResponseTimeMs     int     `json:"min_response_time_ms"`
				MaxResponseTimeMs     int     `json:"max_response_time_ms"`
			} `json:"groups"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Len(t, response.Data.Groups, 2)

	plus := response.Data.Groups[1]
	if response.Data.Groups[0].Group == "Plus" {
		plus = response.Data.Groups[0]
	}
	require.Equal(t, "Plus", plus.Group)
	require.Equal(t, 2, plus.ChannelCount)
	require.Equal(t, 1, plus.EnabledCount)
	require.Equal(t, 1, plus.TestedCount)
	require.Equal(t, 100.0, plus.AverageResponseTimeMs)
	require.Equal(t, 100, plus.MinResponseTimeMs)
	require.Equal(t, 100, plus.MaxResponseTimeMs)
}
