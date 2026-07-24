package openai

import (
	"bufio"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/relayconvert"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// OaiChatBufferedStreamHandler converts an upstream Chat Completions SSE
// response back to the non-streaming JSON shape requested by the client.
func OaiChatBufferedStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	defer service.CloseResponseBodyGracefully(resp)

	state := relayconvert.NewChatToResponsesStreamState("", info.UpstreamModelName)
	var lastData []byte
	var received bool

	scanner := helper.NewStreamScanner(resp.Body)
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 5 || line[:5] != "data:" {
			continue
		}
		data := strings.TrimSpace(line[5:])
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}

		var chunk dto.ChatCompletionsStreamResponse
		if err := common.UnmarshalJsonStr(data, &chunk); err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		if _, err := relayconvert.ChatCompletionsStreamChunkToResponsesEvents(&chunk, state); err != nil {
			return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		received = true
		lastData = append(lastData[:0], data...)
	}
	if err := scanner.Err(); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}
	if !received {
		return nil, types.NewOpenAIError(fmt.Errorf("empty chat completions stream"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	events := relayconvert.FinalizeChatCompletionsStreamToResponses(state)
	var finalResponse *dto.OpenAIResponsesResponse
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Payload.Response != nil {
			finalResponse = events[i].Payload.Response
			break
		}
	}
	if finalResponse == nil {
		return nil, types.NewOpenAIError(fmt.Errorf("chat completions stream has no final response"), types.ErrorCodeBadResponse, http.StatusInternalServerError)
	}

	chatResponse, usage, err := relayconvert.ResponsesResponseToChatCompletionsResponse(finalResponse, state.ID)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	applyUsagePostProcessing(info, usage, lastData)
	chatResponse.Usage = *usage

	responseBody, err := common.Marshal(chatResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeJsonMarshalFailed, http.StatusInternalServerError)
	}
	resp.Header.Set("Content-Type", "application/json; charset=utf-8")
	service.IOCopyBytesGracefully(c, resp, responseBody)
	return usage, nil
}
