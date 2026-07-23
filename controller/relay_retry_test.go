package controller

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
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

func TestApplySolCapabilityRetryBudget(t *testing.T) {
	decision := service.ChannelFailureDecision{
		Class:  service.ChannelFailureKeyCapability,
		Retry:  true,
		Reason: "sol_key_capability",
	}

	decision, used := applySolCapabilityRetryBudget(decision, 0, 2)
	require.True(t, decision.Retry)
	require.Equal(t, 1, used)

	decision, used = applySolCapabilityRetryBudget(decision, used, 2)
	require.True(t, decision.Retry)
	require.Equal(t, 2, used)

	decision, used = applySolCapabilityRetryBudget(decision, used, 2)
	require.False(t, decision.Retry)
	require.Equal(t, 2, used)
	require.Contains(t, decision.Reason, "sol_capability_budget_exhausted")
}
