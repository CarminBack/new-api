package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var initTokenEncodersOnce sync.Once

// runResponsesStream drives OaiResponsesStreamHandler over a raw SSE body.
func runResponsesStream(t *testing.T, body string) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo, error) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	initTokenEncodersOnce.Do(service.InitTokenEncoders)
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(common.RequestIdKey, "responses-stream-tracking-test")

	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5.1",
		DisablePing:     true,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gpt-5.1",
		},
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	_, apiErr := OaiResponsesStreamHandler(c, info, resp)
	var err error
	if apiErr != nil {
		err = apiErr
	}
	return c, w, info, err
}

// Metadata-only events must be buffered: the client sees nothing, so an upstream
// that dies before any content must remain retryable.
func TestResponsesStreamBuffersMetadataEvents(t *testing.T) {
	body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n" +
		"data: {\"type\":\"response.in_progress\"}\n\n"

	c, w, info, err := runResponsesStream(t, body)

	assert.NotContains(t, w.Body.String(), "response.created",
		"buffered metadata must not reach the client before content")
	assert.Equal(t, 0, info.SendResponseCount, "no downstream event should be counted")
	assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyStreamActualOutputStarted))
	require.NotNil(t, err, "a stream that ends before any content must be retryable")
}

// A stream that delivers content then ends without a terminal event is not
// retryable — the client already saw output.
func TestResponsesStreamContentThenEOFIsNotRetryable(t *testing.T) {
	body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"

	c, w, info, err := runResponsesStream(t, body)

	assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyStreamActualOutputStarted))
	assert.Greater(t, info.SendResponseCount, 0)
	assert.Contains(t, w.Body.String(), "response.output_text.delta")
	assert.Nil(t, err, "content already reached the client, so the stream must not retry")
}

// Buffered metadata is flushed as soon as real content arrives.
func TestResponsesStreamFlushesPendingBeforeContent(t *testing.T) {
	body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"

	_, w, info, _ := runResponsesStream(t, body)

	out := w.Body.String()
	createdAt := strings.Index(out, "response.created")
	deltaAt := strings.Index(out, "response.output_text.delta")
	require.NotEqual(t, -1, createdAt, "buffered metadata must be flushed once content arrives")
	require.NotEqual(t, -1, deltaAt)
	assert.Less(t, createdAt, deltaAt, "metadata must be emitted before the content that flushed it")
	assert.True(t, info.StreamDownstreamStarted)
}

// The stream tracker must be initialized so channelResponseStarted can trust it.
func TestResponsesStreamEnablesConfirmedWriteTracking(t *testing.T) {
	body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"r\"}}\n\n"

	c, _, _, _ := runResponsesStream(t, body)

	_, tracked := common.GetContextKey(c, constant.ContextKeyStreamResponseTracking)
	require.True(t, tracked, "the handler must opt into confirmed-write tracking")
}

// A terminal event proves the stream completed even without a trailing [DONE].
func TestResponsesStreamTerminalEventWithoutDoneIsNotRetryable(t *testing.T) {
	body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"r\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"

	_, _, info, err := runResponsesStream(t, body)

	assert.Equal(t, "response.completed", info.StreamTerminalEvent)
	assert.True(t, info.StreamUsagePresent)
	assert.Nil(t, err, "a terminal event closes the stream even without [DONE]")
}

// Buffered metadata alone does not count as content: the client never saw it.
func TestResponsesStreamBufferedMetadataPlusTerminalIsNotRetryable(t *testing.T) {
	body := "data: {\"type\":\"response.in_progress\"}\n\n" +
		"data: {\"type\":\"response.failed\",\"response\":{\"id\":\"r\",\"status\":\"failed\"}}\n\n"

	c, _, info, _ := runResponsesStream(t, body)

	assert.Equal(t, "response.failed", info.StreamTerminalEvent)
	assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyStreamActualOutputStarted))
}
