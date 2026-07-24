package codex

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service/relayconvert"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const expectedJSONModeInstruction = "When producing the final textual output, return a valid json object."

func TestConvertOpenAIResponsesRequestNormalizesJSONModeInstruction(t *testing.T) {
	tests := []struct {
		name         string
		text         json.RawMessage
		instructions json.RawMessage
		input        json.RawMessage
		want         string
	}{
		{
			name: "injects missing json instruction",
			text: mustCodexRawMessage(t, map[string]any{
				"format": map[string]any{"type": "json_object"},
			}),
			input: mustCodexRawMessage(t, []map[string]any{
				{"role": "user", "content": "Return an object with the result."},
			}),
			want: expectedJSONModeInstruction,
		},
		{
			name: "preserves existing instructions",
			text: mustCodexRawMessage(t, map[string]any{
				"format": map[string]any{"type": "json_object"},
			}),
			instructions: mustCodexRawMessage(t, "Keep the answer concise."),
			input:        mustCodexRawMessage(t, "Return an object with the result."),
			want:         expectedJSONModeInstruction + "\nKeep the answer concise.",
		},
		{
			name: "keeps existing json instruction unchanged",
			text: mustCodexRawMessage(t, map[string]any{
				"format": map[string]any{"type": "json_object"},
			}),
			instructions: mustCodexRawMessage(t, "Return valid JSON."),
			input:        mustCodexRawMessage(t, "Return an object with the result."),
			want:         "Return valid JSON.",
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
			want: "",
		},
		{
			name: "does not change json schema mode",
			text: mustCodexRawMessage(t, map[string]any{
				"format": map[string]any{"type": "json_schema"},
			}),
			input: mustCodexRawMessage(t, "Return an object with the result."),
			want:  "",
		},
		{
			name:  "does not change ordinary responses request",
			input: mustCodexRawMessage(t, "Return an object with the result."),
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
			assert.Equal(t, tt.want, instructions)
		})
	}
}

func TestChatCompletionsJSONModeGetsCodexInstructionAfterConversion(t *testing.T) {
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

	var instructions string
	require.NoError(t, common.Unmarshal(request.Instructions, &instructions))
	assert.Equal(t, expectedJSONModeInstruction, instructions)
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
	var instructions string
	require.NoError(t, common.Unmarshal(got.Instructions, &instructions))
	assert.Equal(t, expectedJSONModeInstruction, instructions)
}

func mustCodexRawMessage(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := common.Marshal(value)
	require.NoError(t, err)
	return raw
}
