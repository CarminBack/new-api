package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestShouldRetryTaskRelaySkipsForbidden(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)

	retry := shouldRetryTaskRelay(ctx, 19, &dto.TaskError{StatusCode: http.StatusForbidden}, 5)

	require.False(t, retry)
}

func TestAllowsUncertainCrossChannelRetry(t *testing.T) {
	tests := []struct {
		name    string
		mode    int
		request dto.Request
		want    bool
	}{
		{name: "chat completions", mode: relayconstant.RelayModeChatCompletions, request: &dto.GeneralOpenAIRequest{}, want: true},
		{name: "claude messages", mode: relayconstant.RelayModeUnknown, request: &dto.ClaudeRequest{}, want: true},
		{name: "embeddings", mode: relayconstant.RelayModeEmbeddings, request: &dto.EmbeddingRequest{}, want: true},
		{name: "responses without tools", mode: relayconstant.RelayModeResponses, request: &dto.OpenAIResponsesRequest{}, want: true},
		{name: "responses function tool", mode: relayconstant.RelayModeResponses, request: &dto.OpenAIResponsesRequest{Tools: json.RawMessage(`[{"type":"function"}]`)}, want: true},
		{name: "responses image generation tool", mode: relayconstant.RelayModeResponses, request: &dto.OpenAIResponsesRequest{Tools: json.RawMessage(`[{"type":"image_generation"}]`)}, want: false},
		{name: "responses malformed tools", mode: relayconstant.RelayModeResponses, request: &dto.OpenAIResponsesRequest{Tools: json.RawMessage(`{`)}, want: false},
		{name: "image generation", mode: relayconstant.RelayModeImagesGenerations, request: &dto.ImageRequest{}, want: false},
		{name: "responses compact", mode: relayconstant.RelayModeResponsesCompact, request: &dto.OpenAIResponsesCompactionRequest{}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{RelayMode: tt.mode}
			if tt.name == "claude messages" {
				info.RelayFormat = types.RelayFormatClaude
			}
			require.Equal(t, tt.want, allowsUncertainCrossChannelRetry(info, tt.request))
		})
	}
}

func TestPoolFailuresRetryOnAnotherChannel(t *testing.T) {
	for _, class := range []service.ChannelFailureClass{
		service.ChannelFailureTransient,
		service.ChannelFailureUncertain,
		service.ChannelFailureRateLimited,
		service.ChannelFailureKeyCapability,
		service.ChannelFailurePoolAccount,
	} {
		require.True(t, shouldExcludeChannelForRetry(class), class)
	}
	require.False(t, shouldExcludeChannelForRetry(service.ChannelFailureChannelFatal))
	require.False(t, shouldExcludeChannelForRetry(service.ChannelFailureTerminal))
}

func TestRelayAutoDisableOnlyAllowsExplicitGatewayFailure(t *testing.T) {
	for _, class := range []service.ChannelFailureClass{
		service.ChannelFailureTerminal,
		service.ChannelFailureTransient,
		service.ChannelFailureUncertain,
		service.ChannelFailureRateLimited,
		service.ChannelFailureKeyCapability,
		service.ChannelFailurePoolAccount,
	} {
		require.False(t, allowsRelayAutoDisable(class), class)
	}
	require.True(t, allowsRelayAutoDisable(service.ChannelFailureChannelFatal))
}

func TestPoolRetryExcludesWholeChannel(t *testing.T) {
	param := &service.RetryParam{}
	channel := &model.Channel{
		Id:   29,
		Key:  "key-a\nkey-b",
		Keys: []string{"key-a", "key-b"},
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true,
		},
	}
	param.MarkAttempted(channel.Id, 0)
	if shouldExcludeChannelForRetry(service.ChannelFailureKeyCapability) {
		param.ExcludeChannel(channel)
	}

	require.Equal(t, map[int]struct{}{0: {}, 1: {}}, param.ExcludedKeys(channel.Id))
}

func TestGetChannelHydratesInitialMultiKeyChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalDB := model.DB
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		model.DB = originalDB
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
	})

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}))
	model.DB = db
	channel := &model.Channel{
		Id:     37,
		Key:    "key-a\nkey-b",
		Status: common.ChannelStatusEnabled,
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: 2,
		},
	}
	require.NoError(t, db.Create(channel).Error)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyChannelId, channel.Id)
	selected, apiErr := getChannel(ctx, &relaycommon.RelayInfo{}, &service.RetryParam{})

	require.Nil(t, apiErr)
	require.True(t, selected.ChannelInfo.IsMultiKey)
	require.Len(t, selected.GetKeys(), 2)
	param := &service.RetryParam{}
	param.ExcludeChannel(selected)
	require.Equal(t, map[int]struct{}{0: {}, 1: {}}, param.ExcludedKeys(channel.Id))
}
