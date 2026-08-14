package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertOpenAIResponsesRequestNormalizesJSONModeInput(t *testing.T) {
	tests := []struct {
		name               string
		text               json.RawMessage
		instructions       json.RawMessage
		input              json.RawMessage
		wantInputJSON      bool
		wantInputUnchanged bool
		wantInstructions   string
	}{
		{
			name: "injects missing json instruction",
			text: mustCodexRawMessage(t, map[string]any{
				"format": map[string]any{"type": "json_object"},
			}),
			input: mustCodexRawMessage(t, []map[string]any{
				{"role": "user", "content": "Return an object with the result."},
			}),
			wantInputJSON: true,
		},
		{
			name: "preserves existing instructions",
			text: mustCodexRawMessage(t, map[string]any{
				"format": map[string]any{"type": "json_object"},
			}),
			instructions:     mustCodexRawMessage(t, "Keep the answer concise."),
			input:            mustCodexRawMessage(t, "Return an object with the result."),
			wantInputJSON:    true,
			wantInstructions: "Keep the answer concise.",
		},
		{
			name: "injects input even when instructions mention json",
			text: mustCodexRawMessage(t, map[string]any{
				"format": map[string]any{"type": "json_object"},
			}),
			instructions:     mustCodexRawMessage(t, "Return valid JSON."),
			input:            mustCodexRawMessage(t, "Return an object with the result."),
			wantInputJSON:    true,
			wantInstructions: "Return valid JSON.",
		},
		{
			name: "accepts json keyword in nested input text",
			text: mustCodexRawMessage(t, map[string]any{
				"format": map[string]any{"type": "json_object"},
			}),
			input: mustCodexRawMessage(t, []map[string]any{
				{
					"role": "user",
					"content": []map[string]any{
						{"type": "input_text", "text": "Return a json object."},
					},
				},
			}),
			wantInputJSON:      true,
			wantInputUnchanged: true,
		},
		{
			name: "does not change json schema mode",
			text: mustCodexRawMessage(t, map[string]any{
				"format": map[string]any{"type": "json_schema"},
			}),
			input:              mustCodexRawMessage(t, "Return an object with the result."),
			wantInputUnchanged: true,
		},
		{
			name:               "does not change ordinary responses request",
			input:              mustCodexRawMessage(t, "Return an object with the result."),
			wantInputUnchanged: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalInput := append(json.RawMessage(nil), tt.input...)
			adaptor := &Adaptor{}
			converted, err := adaptor.ConvertOpenAIResponsesRequest(nil, nil, dto.OpenAIResponsesRequest{
				Model:        "gpt-5.6-luna",
				Text:         tt.text,
				Instructions: tt.instructions,
				Input:        tt.input,
			})

			require.NoError(t, err)
			request, ok := converted.(dto.OpenAIResponsesRequest)
			require.True(t, ok)

			var instructions string
			require.NoError(t, common.Unmarshal(request.Instructions, &instructions))
			assert.Equal(t, tt.wantInstructions, instructions)
			assert.Equal(t, tt.wantInputJSON, strings.Contains(strings.ToLower(string(request.Input)), "json"))
			if tt.wantInputUnchanged {
				assert.JSONEq(t, string(originalInput), string(request.Input))
			}
		})
	}
}

func TestChatCompletionsJSONModeGetsCodexInputAfterConversion(t *testing.T) {
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
	assert.JSONEq(t, `""`, string(request.Instructions))
}

func TestConvertOpenAIResponsesRequestPreservesToolsWhenInjectingJSONInstruction(t *testing.T) {
	tools := mustCodexRawMessage(t, []map[string]any{
		{
			"type": "function",
			"name": "lookup_order",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{"id": map[string]any{"type": "string"}},
			},
		},
	})
	request := dto.OpenAIResponsesRequest{
		Model: "gpt-5.6-luna",
		Text: mustCodexRawMessage(t, map[string]any{
			"format": map[string]any{"type": "json_object"},
		}),
		Input: mustCodexRawMessage(t, "Look up the requested order."),
		Tools: tools,
	}

	converted, err := (&Adaptor{}).ConvertOpenAIResponsesRequest(nil, nil, request)
	require.NoError(t, err)
	got, ok := converted.(dto.OpenAIResponsesRequest)
	require.True(t, ok)

	assert.JSONEq(t, string(tools), string(got.Tools))
	assert.Contains(t, strings.ToLower(string(got.Input)), "json")
	assert.JSONEq(t, `""`, string(got.Instructions))
}

func mustCodexRawMessage(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := common.Marshal(value)
	require.NoError(t, err)
	return raw
}

func TestGetRequestURLAlphaSearch(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:    constant.ChannelTypeCodex,
			ChannelBaseUrl: "https://chatgpt.com",
		},
		RelayMode: relayconstant.RelayModeAlphaSearch,
	}

	url, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://chatgpt.com/backend-api/codex/alpha/search", url)
}
