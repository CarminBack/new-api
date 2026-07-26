package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newDirectResponsesStreamTestContext(t *testing.T, body io.Reader) (*gin.Context, *httptest.ResponseRecorder, *http.Response, *relaycommon.RelayInfo) {
	t.Helper()
	if constant.StreamingTimeout <= 0 {
		oldTimeout := constant.StreamingTimeout
		constant.StreamingTimeout = 30
		t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(common.RequestIdKey, "direct-responses-test")

	bodyCloser, ok := body.(io.ReadCloser)
	if !ok {
		bodyCloser = io.NopCloser(body)
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       bodyCloser,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"},
		IsStream:    true,
		RelayFormat: types.RelayFormatOpenAIResponses,
		DisablePing: true,
	}
	return c, recorder, resp, info
}

func TestOaiResponsesStreamHandlerReadsUsageFromTerminalVariants(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	for _, eventType := range []string{"response.completed", "response.done", "response.incomplete"} {
		t.Run(eventType, func(t *testing.T) {
			body := "data: {\"type\":\"" + eventType + "\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":12,\"output_tokens\":4,\"total_tokens\":16,\"input_tokens_details\":{\"cached_tokens\":7,\"cache_write_tokens\":3}}}}\n\n"
			c, _, resp, info := newDirectResponsesStreamTestContext(t, strings.NewReader(body))

			usage, apiErr := OaiResponsesStreamHandler(c, info, resp)

			require.Nil(t, apiErr)
			require.NotNil(t, usage)
			assert.Equal(t, 12, usage.PromptTokens)
			assert.Equal(t, 4, usage.CompletionTokens)
			assert.Equal(t, 16, usage.TotalTokens)
			assert.Equal(t, 7, usage.PromptTokensDetails.CachedTokens)
			assert.Equal(t, 3, usage.PromptTokensDetails.CacheWriteTokens)
			require.NotNil(t, usage.BillingUsage)
			assert.Equal(t, "oai_responses", usage.BillingUsage.Source)
			assert.Equal(t, eventType, info.StreamTerminalEvent)
			assert.True(t, info.StreamUsagePresent)
		})
	}
}

func TestOaiResponsesStreamHandlerEstimatesFromFinalResponseOutput(t *testing.T) {
	body := `data: {"type":"response.done","response":{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"final answer only"}]}]}}` + "\n\n"
	c, _, resp, info := newDirectResponsesStreamTestContext(t, strings.NewReader(body))
	info.SetEstimatePromptTokens(11)

	usage, apiErr := OaiResponsesStreamHandler(c, info, resp)

	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 11, usage.PromptTokens)
	assert.Positive(t, usage.CompletionTokens)
	assert.Equal(t, usage.PromptTokens+usage.CompletionTokens, usage.TotalTokens)
	assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens))
	assert.Equal(t, "response.done", info.StreamTerminalEvent)
	assert.False(t, info.StreamUsagePresent)
}

func TestOaiResponsesStreamHandlerReturnsTerminalErrors(t *testing.T) {
	for _, eventType := range []string{"response.failed", "response.error"} {
		t.Run(eventType, func(t *testing.T) {
			body := "data: {\"type\":\"" + eventType + "\",\"response\":{\"error\":{\"type\":\"upstream_error\",\"message\":\"pool account failed\",\"code\":\"account_failed\"}}}\n\n"
			c, recorder, resp, info := newDirectResponsesStreamTestContext(t, strings.NewReader(body))

			usage, apiErr := OaiResponsesStreamHandler(c, info, resp)

			assert.Nil(t, usage)
			require.NotNil(t, apiErr)
			assert.Contains(t, apiErr.Error(), "pool account failed")
			assert.False(t, types.IsSkipRetryError(apiErr), "a terminal upstream failure before output is eligible for channel retry")
			assert.Equal(t, eventType, info.StreamTerminalEvent)
			require.NotNil(t, info.StreamStatus)
			assert.True(t, info.StreamStatus.HasErrors())
			assert.Empty(t, recorder.Body.String(), "failed terminal events must not be forwarded before retry classification")
		})
	}
}

func TestOaiResponsesStreamHandlerLeavesMissingUsageUncharged(t *testing.T) {
	body := `data: {"type":"response.done","response":{"status":"completed"}}` + "\n\n"
	c, _, resp, info := newDirectResponsesStreamTestContext(t, strings.NewReader(body))

	usage, apiErr := OaiResponsesStreamHandler(c, info, resp)

	require.NotNil(t, apiErr)
	assert.Nil(t, usage)
	assert.False(t, types.IsSkipRetryError(apiErr))
	assert.Equal(t, "response.done", info.StreamTerminalEvent)
	assert.False(t, info.StreamUsagePresent)
}

func TestOaiResponsesStreamHandlerRecordsEOFWithoutTerminalEvent(t *testing.T) {
	body := `data: {"type":"response.created","response":{"id":"resp_1"}}` + "\n\n"
	c, _, resp, info := newDirectResponsesStreamTestContext(t, strings.NewReader(body))

	usage, apiErr := OaiResponsesStreamHandler(c, info, resp)

	require.NotNil(t, apiErr)
	assert.Nil(t, usage)
	assert.False(t, types.IsSkipRetryError(apiErr))
	assert.Empty(t, info.StreamTerminalEvent)
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonEOF, info.StreamStatus.EndReason)
}

func TestOaiResponsesStreamHandlerLeavesClientDisconnectUncharged(t *testing.T) {
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	c, _, resp, info := newDirectResponsesStreamTestContext(t, reader)
	requestCtx, cancel := context.WithCancel(c.Request.Context())
	c.Request = c.Request.WithContext(requestCtx)

	done := make(chan struct{})
	var usageTotal int
	var apiErr *types.NewAPIError
	go func() {
		usage, err := OaiResponsesStreamHandler(c, info, resp)
		if usage != nil {
			usageTotal = usage.TotalTokens
		}
		apiErr = err
		close(done)
	}()
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for client disconnect")
	}
	require.Nil(t, apiErr)
	assert.Zero(t, usageTotal)
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
}

func TestOaiResponsesStreamHandlerTerminalEventWinsOverEOF(t *testing.T) {
	body := `data: {"type":"response.created","response":{"id":"resp_1"}}` + "\n" +
		`data: {"type":"response.done","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}` + "\n"
	c, _, resp, info := newDirectResponsesStreamTestContext(t, strings.NewReader(body))

	usage, apiErr := OaiResponsesStreamHandler(c, info, resp)

	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, "response.done", info.StreamTerminalEvent)
	assert.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
}

func TestOaiResponsesStreamHandlerDoesNotRetryAfterOutputWrite(t *testing.T) {
	body := `data: {"type":"response.output_text.delta","delta":"hello"}` + "\n" +
		`data: {"type":"response.failed","response":{"error":{"type":"upstream_error","message":"late failure"}}}` + "\n"
	c, recorder, resp, info := newDirectResponsesStreamTestContext(t, strings.NewReader(body))

	usage, apiErr := OaiResponsesStreamHandler(c, info, resp)

	assert.Nil(t, usage)
	require.NotNil(t, apiErr)
	assert.True(t, types.IsSkipRetryError(apiErr))
	assert.Contains(t, recorder.Body.String(), "hello")
}

func TestOaiResponsesStreamHandlerRetriesAfterMetadataOnlyFailure(t *testing.T) {
	body := `data: {"type":"response.created","response":{"id":"resp_meta"}}` + "\n" +
		`data: {"type":"response.in_progress","response":{"id":"resp_meta"}}` + "\n" +
		`data: {"type":"response.failed","response":{"error":{"type":"server_error","message":"temporary"}}}` + "\n"
	c, recorder, resp, info := newDirectResponsesStreamTestContext(t, strings.NewReader(body))

	usage, apiErr := OaiResponsesStreamHandler(c, info, resp)

	assert.Nil(t, usage)
	require.NotNil(t, apiErr)
	assert.False(t, types.IsSkipRetryError(apiErr))
	assert.Empty(t, recorder.Body.String(), "metadata-only failure must not be sent to downstream")
}
