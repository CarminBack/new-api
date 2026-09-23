package helper

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newStreamTrackingContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	common.SetContextKey(c, constant.ContextKeyStreamResponseTracking, true)
	common.SetContextKey(c, constant.ContextKeyStreamDownstreamStarted, false)
	common.SetContextKey(c, constant.ContextKeyStreamActualOutputStarted, false)
	return c
}

// Metadata-only events must stay retryable: writing response.created cannot be
// treated as real downstream output, or an early upstream failure could never
// fail over to another channel.
func TestResponseChunkDataMetadataDoesNotMarkActualOutput(t *testing.T) {
	for _, eventType := range []string{"response.created", "response.in_progress", "response.queued"} {
		c := newStreamTrackingContext(t)
		payload, err := json.Marshal(map[string]any{"type": eventType})
		require.NoError(t, err)

		require.NoError(t, ResponseChunkData(c, dto.ResponsesStreamResponse{Type: eventType}, string(payload)))

		assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyStreamDownstreamStarted), "event %s should mark downstream started", eventType)
		assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyStreamActualOutputStarted), "event %s must not mark actual output", eventType)
	}
}

// Content-bearing events must mark actual output so an already-visible stream is
// never retried against another channel.
func TestResponseChunkDataContentMarksActualOutput(t *testing.T) {
	eventTypes := []string{
		"response.output_text.delta",
		"response.reasoning_summary_text.delta",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.audio_transcript.delta",
		"response.audio.delta",
		"response.image_generation_call.partial_image",
		"response.image_generation_call.completed",
	}
	for _, eventType := range eventTypes {
		c := newStreamTrackingContext(t)
		payload, err := json.Marshal(map[string]any{"type": eventType})
		require.NoError(t, err)

		require.NoError(t, ResponseChunkData(c, dto.ResponsesStreamResponse{Type: eventType}, string(payload)))

		assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyStreamActualOutputStarted), "event %s should mark actual output", eventType)
	}
}

// An output_item.added/done carrying tool arguments is real content, while the
// same event without arguments is metadata.
func TestResponsesEventHasActualOutputItemArguments(t *testing.T) {
	withArgs := dto.ResponsesStreamResponse{
		Type: dto.ResponsesOutputTypeItemAdded,
		Item: &dto.ResponsesOutput{Type: dto.BuildInCallFunctionCall, Arguments: json.RawMessage(`{"a":1}`)},
	}
	assert.True(t, responsesEventHasActualOutput(withArgs))

	withoutArgs := dto.ResponsesStreamResponse{
		Type: dto.ResponsesOutputTypeItemAdded,
		Item: &dto.ResponsesOutput{Type: dto.BuildInCallFunctionCall},
	}
	assert.False(t, responsesEventHasActualOutput(withoutArgs))

	doneWithoutArgs := dto.ResponsesStreamResponse{
		Type: dto.ResponsesOutputTypeItemDone,
		Item: &dto.ResponsesOutput{Type: dto.BuildInCallFunctionCall},
	}
	assert.False(t, responsesEventHasActualOutput(doneWithoutArgs))
}

// A raw write must count as a confirmed body write so short writes are surfaced
// instead of silently truncating the SSE stream.
func TestResponseChunkDataWritesRawPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)

	require.NoError(t, ResponseChunkData(c, dto.ResponsesStreamResponse{Type: "response.output_text.delta"}, `{"delta":"hi"}`))

	assert.True(t, c.Writer.Written())
	assert.Contains(t, recorder.Body.String(), "event: response.output_text.delta")
	assert.Contains(t, recorder.Body.String(), `data: {"delta":"hi"}`)
}

// channelResponseStarted lives in service, but the helper must agree with it:
// a tracked stream that only wrote metadata is not "started".
func TestTrackedStreamMetadataIsNotStarted(t *testing.T) {
	c := newStreamTrackingContext(t)
	require.NoError(t, ResponseChunkData(c, dto.ResponsesStreamResponse{Type: "response.created"}, `{}`))
	assert.True(t, c.Writer.Written(), "the HTTP header is written")
	assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyStreamActualOutputStarted), "but the stream is not logically started")
}

// MarkActualStreamOutput lets non-Responses converters share the same boundary.
func TestMarkActualStreamOutputSetsBoundary(t *testing.T) {
	c := newStreamTrackingContext(t)
	assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyStreamActualOutputStarted))
	MarkActualStreamOutput(c)
	assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyStreamActualOutputStarted))
}
