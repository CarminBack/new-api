package openai

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service/relayconvert"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatCompletionsJSONModeGetsInstructionForOpenAIChatChannel(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model: "gpt-5.6-luna",
		Messages: []dto.Message{
			{Role: "user", Content: "Return an object with field answer equal 42."},
		},
		ResponseFormat: &dto.ResponseFormat{Type: "json_object"},
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	require.NoError(t, err)
	got, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Len(t, got.Messages, 2)
	assert.Equal(t, "developer", got.Messages[0].Role)
	assert.Equal(t, "When producing the final textual output, return a valid json object.", got.Messages[0].Content)
}

func TestChatCompletionsJSONModeKeepsCompliantMessages(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model: "gpt-5.6-luna",
		Messages: []dto.Message{
			{Role: "user", Content: "Return valid JSON with field answer equal 42."},
		},
		ResponseFormat: &dto.ResponseFormat{Type: "json_object"},
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	require.NoError(t, err)
	got, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Len(t, got.Messages, 1)
	assert.Equal(t, "Return valid JSON with field answer equal 42.", got.Messages[0].Content)
}

func TestChatCompletionsJSONModeDoesNotAffectOtherRequests(t *testing.T) {
	tests := []struct {
		name        string
		channelType int
		format      string
	}{
		{name: "json schema", channelType: constant.ChannelTypeOpenAI, format: "json_schema"},
		{name: "other channel", channelType: constant.ChannelTypeOpenRouter, format: "json_object"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := &dto.GeneralOpenAIRequest{
				Model: "gpt-5.6-luna",
				Messages: []dto.Message{
					{Role: "user", Content: "Return an object with field answer equal 42."},
				},
				ResponseFormat: &dto.ResponseFormat{Type: tt.format},
			}
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: tt.channelType}}

			converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
			require.NoError(t, err)
			got, ok := converted.(*dto.GeneralOpenAIRequest)
			require.True(t, ok)
			require.Len(t, got.Messages, 1)
			assert.Equal(t, "Return an object with field answer equal 42.", got.Messages[0].Content)
		})
	}
}

func TestChatCompletionsJSONModeGetsInstructionForOpenAIResponsesChannel(t *testing.T) {
	chatRequest := &dto.GeneralOpenAIRequest{
		Model: "gpt-5.6-luna",
		Messages: []dto.Message{
			{Role: "user", Content: "Return an object with the result."},
		},
		ResponseFormat: &dto.ResponseFormat{Type: "json_object"},
	}
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI}

	result, err := relayconvert.ConvertRequestByID(
		nil,
		info,
		relayconvert.ConverterOpenAIChatToOpenAIResponses,
		chatRequest,
	)
	require.NoError(t, err)
	responsesRequest, ok := result.Value.(*dto.OpenAIResponsesRequest)
	require.True(t, ok)

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, nil, *responsesRequest)
	require.NoError(t, err)
	request, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok)

	assert.Contains(t, strings.ToLower(string(request.Input)), "json")
	assert.Empty(t, request.Instructions)
}
