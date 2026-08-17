package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestChannelHealthProbeEndpointTypeForImagePaths(t *testing.T) {
	for _, path := range []string{
		"/v1/images/generations",
		"/v1/images/edits",
		"/v1/images/variations",
	} {
		require.Equal(t, string(constant.EndpointTypeImageGeneration), channelHealthProbeEndpointType(path))
	}
}

func TestValidateChannelHealthRecoveryPayload(t *testing.T) {
	keyIndex := 2
	tests := []struct {
		name    string
		payload channelHealthRecoveryPayload
		wantErr bool
	}{
		{name: "channel", payload: channelHealthRecoveryPayload{Scope: service.ChannelHealthRecoveryScopeChannel}},
		{name: "route", payload: channelHealthRecoveryPayload{Scope: service.ChannelHealthRecoveryScopeRoute, ModelName: "gpt-test", RequestPath: "/v1/responses"}},
		{name: "route missing path", payload: channelHealthRecoveryPayload{Scope: service.ChannelHealthRecoveryScopeRoute, ModelName: "gpt-test"}, wantErr: true},
		{name: "key", payload: channelHealthRecoveryPayload{Scope: service.ChannelHealthRecoveryScopeKey, KeyIndex: &keyIndex}},
		{name: "key missing index", payload: channelHealthRecoveryPayload{Scope: service.ChannelHealthRecoveryScopeKey}, wantErr: true},
		{name: "unknown", payload: channelHealthRecoveryPayload{Scope: "all"}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateChannelHealthRecoveryPayload(test.payload)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestChannelHealthSummaryCountsChannelsOnce(t *testing.T) {
	summary := channelHealthSummary{}
	updateChannelHealthSummary(&summary, channelHealthItem{
		Adaptive: service.ChannelAdaptiveHealthSnapshot{
			Routes: []service.ChannelRouteHealthSnapshot{
				{State: service.ChannelHealthStateCircuitOpen},
				{State: service.ChannelHealthStateHalfOpen},
				{State: service.ChannelHealthStateRecovering},
			},
		},
	})

	require.Equal(t, 1, summary.CircuitOpenChannels)
	require.Equal(t, 2, summary.CircuitOpenRoutes)
	require.Zero(t, summary.RecoveringChannels)
	require.Equal(t, 1, summary.RecoveringRoutes)

	updateChannelHealthSummary(&summary, channelHealthItem{
		Adaptive: service.ChannelAdaptiveHealthSnapshot{
			Routes: []service.ChannelRouteHealthSnapshot{{State: service.ChannelHealthStateRecovering}},
		},
	})
	require.Equal(t, 1, summary.RecoveringChannels)
}

func TestGetChannelHealthReturnsAutoDisabledMetadataWithoutKeys(t *testing.T) {
	gatewayDB := model.DB
	memoryCacheEnabled := common.MemoryCacheEnabled
	t.Cleanup(func() {
		model.DB = gatewayDB
		common.MemoryCacheEnabled = memoryCacheEnabled
	})

	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}))
	model.DB = db
	common.MemoryCacheEnabled = false

	autoDisabled := &model.Channel{
		Id:          29,
		Type:        1,
		Key:         "secret-key",
		Name:        "primary",
		Status:      common.ChannelStatusAutoDisabled,
		Models:      "gpt-test",
		Group:       "default",
		ChannelInfo: model.ChannelInfo{},
	}
	autoDisabled.SetOtherInfo(map[string]interface{}{
		"status_reason": "credential rejected",
		"status_time":   int64(1_700_000_000),
	})
	require.NoError(t, db.Create(autoDisabled).Error)
	require.NoError(t, db.Create(&model.Channel{
		Id: 30, Type: 1, Key: "healthy-secret", Name: "healthy", Status: common.ChannelStatusEnabled, Models: "gpt-test", Group: "default",
	}).Error)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/channel/health", nil)
	GetChannelHealth(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "secret-key")
	require.NotContains(t, recorder.Body.String(), "healthy-secret")
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Items []channelHealthItem `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Len(t, response.Data.Items, 1)
	require.Equal(t, autoDisabled.Id, response.Data.Items[0].ChannelID)
	require.Equal(t, "credential rejected", response.Data.Items[0].StatusReason)

	var rawResponse map[string]interface{}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &rawResponse))
	data, ok := rawResponse["data"].(map[string]interface{})
	require.True(t, ok)
	items, ok := data["items"].([]interface{})
	require.True(t, ok)
	require.Len(t, items, 1)
	item, ok := items[0].(map[string]interface{})
	require.True(t, ok)
	require.IsType(t, []interface{}{}, item["persistent_keys"])
	adaptive, ok := item["adaptive"].(map[string]interface{})
	require.True(t, ok)
	require.IsType(t, []interface{}{}, adaptive["routes"])
	require.IsType(t, []interface{}{}, adaptive["keys"])
}
