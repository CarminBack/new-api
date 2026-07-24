package openai

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service/relayconvert"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	var instructions string
	require.NoError(t, common.Unmarshal(request.Instructions, &instructions))
	assert.Equal(t, "When producing the final textual output, return a valid json object.", instructions)
}
