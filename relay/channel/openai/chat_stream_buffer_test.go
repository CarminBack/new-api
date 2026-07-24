package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOaiChatBufferedStreamHandlerReturnsNonStreamingJSON(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"resp_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-5.6-luna","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		`data: {"id":"resp_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-5.6-luna","choices":[{"index":0,"delta":{"content":"{\"answer\":"},"finish_reason":null}]}`,
		`data: {"id":"resp_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-5.6-luna","choices":[{"index":0,"delta":{"content":"42}"},"finish_reason":null}]}`,
		`data: {"id":"resp_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-5.6-luna","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: {"id":"resp_1","object":"chat.completion.chunk","created":1710000000,"model":"gpt-5.6-luna","choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`,
		`data: [DONE]`,
		``,
	}, "\n")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(common.RequestIdKey, "buffer-test")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5.6-luna"},
		RelayFormat: types.RelayFormatOpenAI,
	}

	usage, apiErr := OaiChatBufferedStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.Equal(t, 10, usage.TotalTokens)
	require.Equal(t, "application/json; charset=utf-8", recorder.Header().Get("Content-Type"))

	var got dto.OpenAITextResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &got))
	require.Equal(t, "chat.completion", got.Object)
	require.Len(t, got.Choices, 1)
	require.Equal(t, `{"answer":42}`, got.Choices[0].Message.StringContent())
	require.Equal(t, "stop", got.Choices[0].FinishReason)
	require.Equal(t, 10, got.Usage.TotalTokens)
}
