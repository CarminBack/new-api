package openai

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"time"

	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
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

// A writer that exposes failures hidden by Gin's http.Flusher interface.
func TestResponsesStreamHTTPClientClose(t *testing.T) {
	initTokenEncodersOnce.Do(service.InitTokenEncoders)
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	for _, completed := range []bool{false, true} {
		t.Run(fmt.Sprintf("completed=%v", completed), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				event := `{"type":"response.output_text.delta","delta":"hello"}`
				if completed {
					event = `{"type":"response.completed","response":{"id":"r","status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}`
				}
				_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer upstream.Close()
			results := make(chan *relaycommon.StreamStatus, 1)
			engine := gin.New()
			engine.POST("/v1/responses", func(c *gin.Context) {
				req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, upstream.URL, nil)
				if err != nil {
					results <- nil
					return
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					results <- nil
					return
				}
				info := &relaycommon.RelayInfo{OriginModelName: "gpt-5.1", DisablePing: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5.1"}}
				_, _ = OaiResponsesStreamHandler(c, info, resp)
				results <- info.StreamStatus
			})
			gateway := httptest.NewServer(engine)
			defer gateway.Close()
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Post(gateway.URL+"/v1/responses", "application/json", strings.NewReader("{}"))
			require.NoError(t, err)
			reader := bufio.NewReader(resp.Body)
			for {
				line, err := reader.ReadString('\n')
				require.NoError(t, err)
				if line == "\n" {
					break
				}
			}
			require.NoError(t, resp.Body.Close())
			select {
			case status := <-results:
				require.NotNil(t, status)
				assert.Equal(t, completed, status.IsSuccessful())
			case <-time.After(5 * time.Second):
				t.Fatal("HTTP disconnect did not finish relay")
			}
		})
	}
}

// A writer that exposes failures hidden by Gin's http.Flusher interface.
type responsesFaultWriter struct {
	*httptest.ResponseRecorder
	failWrite bool
	failFlush bool
	onFlush   func()
}

func (w *responsesFaultWriter) Write(p []byte) (int, error) {
	if w.failWrite && strings.Contains(string(p), "response.completed") {
		return 0, io.ErrClosedPipe
	}
	return w.ResponseRecorder.Write(p)
}

func (w *responsesFaultWriter) FlushError() error {
	if w.failFlush && strings.Contains(w.Body.String(), "response.completed") {
		return io.ErrClosedPipe
	}
	w.ResponseRecorder.Flush()
	if w.onFlush != nil {
		w.onFlush()
	}
	return nil
}

func TestResponsesStreamDeliveryOutcome(t *testing.T) {
	initTokenEncodersOnce.Do(service.InitTokenEncoders)
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	for _, tc := range []struct {
		name, terminal                                        string
		cancel, writeFailure, flushFailure, success, apiError bool
	}{
		{name: "completed then client closes", terminal: "completed", cancel: true, success: true},
		{name: "client closes before completion", cancel: true},
		{name: "EOF before completion"},
		{name: "completion write fails", terminal: "completed", writeFailure: true, apiError: true},
		{name: "completion flush fails", terminal: "completed", flushFailure: true, apiError: true},
		{name: "upstream failed", terminal: "failed"},
		{name: "upstream incomplete", terminal: "incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			w := &responsesFaultWriter{ResponseRecorder: httptest.NewRecorder(), failWrite: tc.writeFailure, failFlush: tc.flushFailure}
			flushed := make(chan struct{}, 1)
			w.onFlush = func() {
				select {
				case flushed <- struct{}{}:
				default:
				}
			}
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
			info := &relaycommon.RelayInfo{OriginModelName: "gpt-5.1", IsStream: true, DisablePing: true, ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5.1"}}
			done := make(chan struct{})
			var apiErr error
			go func() {
				defer close(done)
				_, e := OaiResponsesStreamHandler(c, info, &http.Response{StatusCode: 200, Header: http.Header{}, Body: reader})
				if e != nil {
					apiErr = e
				}
			}()
			if tc.terminal == "" {
				_, err := io.WriteString(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
				require.NoError(t, err)
			} else {
				_, err := io.WriteString(writer, fmt.Sprintf("data: {\"type\":\"response.%s\",\"response\":{\"id\":\"r\",\"status\":\"%s\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n", tc.terminal, tc.terminal))
				require.NoError(t, err)
			}
			if tc.cancel {
				select {
				case <-flushed:
				case <-time.After(5 * time.Second):
					t.Fatal("event was not flushed")
				}
				cancel()
				_ = writer.CloseWithError(context.Canceled)
			} else {
				_ = writer.Close()
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("handler did not finish")
			}
			assert.Equal(t, tc.apiError, apiErr != nil)
			assert.Equal(t, tc.success, info.StreamStatus.IsSuccessful())
			service.MarkRequestPolicySuccess(c, info.StreamStatus)
			assert.Equal(t, tc.success, service.RequestPolicy(c).Successful)
			if tc.success {
				assert.Equal(t, perfmetrics.OutcomeSuccess, perfmetrics.ClassifyRelayOutcome(ctx, info, nil))
				assert.True(t, info.StreamStatus.IsCompletedSuccessfully())
			}
			if tc.writeFailure || tc.flushFailure {
				assert.True(t, info.StreamStatus.HasErrors())
				assert.False(t, errors.Is(apiErr, context.Canceled))
			}
		})
	}
}
