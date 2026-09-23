package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestBadResponseBodyError builds the error a Responses stream reports when
// the upstream body ends without a usable terminal event.
func newTestBadResponseBodyError() *types.NewAPIError {
	return types.NewOpenAIError(
		fmt.Errorf("empty responses stream: upstream ended before terminal event"),
		types.ErrorCodeBadResponseBody,
		http.StatusBadGateway,
	)
}

func newChannelFailureContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c
}

// Without stream tracking the legacy heuristic applies: any written body byte
// counts as a started response.
func TestChannelResponseStartedWithoutTrackingUsesWritten(t *testing.T) {
	c := newChannelFailureContext(t)
	assert.False(t, channelResponseStarted(c))

	// gin only flips Written() on an actual body write, not on WriteHeader.
	_, err := c.Writer.Write([]byte("data: x\n\n"))
	require.NoError(t, err)
	assert.True(t, channelResponseStarted(c))
}

// A tracked stream must ignore a prepared HTTP header and follow the confirmed
// actual-output flag instead.
func TestChannelResponseStartedRespectsTracking(t *testing.T) {
	c := newChannelFailureContext(t)
	common.SetContextKey(c, constant.ContextKeyStreamResponseTracking, true)
	common.SetContextKey(c, constant.ContextKeyStreamActualOutputStarted, false)

	// Metadata was flushed downstream, so the writer already has a body.
	_, err := c.Writer.Write([]byte("event: response.created\ndata: {}\n\n"))
	require.NoError(t, err)
	assert.True(t, c.Writer.Written(), "the body is written")
	assert.False(t, channelResponseStarted(c), "but metadata-only output must stay retryable")

	common.SetContextKey(c, constant.ContextKeyStreamActualOutputStarted, true)
	assert.True(t, channelResponseStarted(c))
}

// A failed stream that buffered only metadata must still be classified as
// retryable, which is what allows failover to another channel.
func TestDecideChannelFailureBufferedMetadataStillRetries(t *testing.T) {
	c := newChannelFailureContext(t)
	common.SetContextKey(c, constant.ContextKeyStreamResponseTracking, true)
	common.SetContextKey(c, constant.ContextKeyStreamActualOutputStarted, false)
	_, err := c.Writer.Write([]byte("event: response.created\ndata: {}\n\n"))
	require.NoError(t, err)

	decision := DecideChannelFailure(c, newTestBadResponseBodyError(), 1, false, true)
	assert.True(t, decision.Retry, "a stream that produced no real output must remain retryable")
	assert.NotContains(t, decision.Reason, "response_started")
}

// A stream that already delivered real content must never retry: the client
// would receive duplicated output.
func TestDecideChannelFailureAfterActualOutputStopsRetry(t *testing.T) {
	c := newChannelFailureContext(t)
	common.SetContextKey(c, constant.ContextKeyStreamResponseTracking, true)
	common.SetContextKey(c, constant.ContextKeyStreamActualOutputStarted, true)
	_, err := c.Writer.Write([]byte("event: response.output_text.delta\ndata: {}\n\n"))
	require.NoError(t, err)

	decision := DecideChannelFailure(c, newTestBadResponseBodyError(), 1, false, true)
	assert.False(t, decision.Retry)
	assert.Contains(t, decision.Reason, "response_started")
}

func TestChannelResponseStartedNilContext(t *testing.T) {
	require.NotPanics(t, func() {
		assert.False(t, channelResponseStarted(nil))
	})
}
