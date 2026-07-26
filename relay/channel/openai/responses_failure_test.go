package openai

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newResponsesFailureTestContext(t *testing.T) *gin.Context {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c
}

func TestRecordResponsesUpstreamFailureCapturesSanitizedFields(t *testing.T) {
	c := newResponsesFailureTestContext(t)
	c.Set(common.UpstreamRequestIdKey, "req_secret.example.com/path?key=secret")
	info := &relaycommon.RelayInfo{ReceivedResponseCount: 7}
	event := dto.ResponsesStreamResponse{
		Type: "response.failed",
		Response: &dto.OpenAIResponsesResponse{
			ID: "resp_123",
			Error: types.OpenAIError{
				Type:    "server_error",
				Code:    "upstream_secret",
				Message: "upstream https://secret.example.com/v1/responses?token=secret failed",
				Param:   "messages",
			},
		},
	}

	recordResponsesUpstreamFailure(c, info, event, true)

	raw, ok := common.GetContextKey(c, constant.ContextKeyUpstreamFailure)
	require.True(t, ok)
	failure, ok := raw.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "response.failed", failure["event"])
	assert.Equal(t, true, failure["downstream_started"])
	assert.Equal(t, 7, failure["received_event_count"])
	assert.Equal(t, "resp_123", failure["response_id"])
	assert.Equal(t, true, failure["error_present"])
	assert.Equal(t, "server_error", failure["type"])
	assert.Equal(t, "upstream_secret", failure["code"])
	assert.Equal(t, "messages", failure["param"])
	assert.NotContains(t, failure["message"], "secret.example.com")
	assert.NotContains(t, failure["message"], "token=secret")
	assert.NotContains(t, failure["upstream_request_id"], "secret.example.com")
}

func TestRecordResponsesUpstreamFailureMarksMissingError(t *testing.T) {
	c := newResponsesFailureTestContext(t)
	event := dto.ResponsesStreamResponse{
		Type:     "response.failed",
		Response: &dto.OpenAIResponsesResponse{ID: "resp_without_error"},
	}

	recordResponsesUpstreamFailure(c, nil, event, false)

	raw, ok := common.GetContextKey(c, constant.ContextKeyUpstreamFailure)
	require.True(t, ok)
	failure := raw.(map[string]interface{})
	assert.Equal(t, false, failure["error_present"])
	assert.Equal(t, "response.failed", failure["event"])
	assert.Equal(t, "resp_without_error", failure["response_id"])
	_, hasMessage := failure["message"]
	assert.False(t, hasMessage)
}

func TestRecordResponsesUpstreamFailureBoundsMessage(t *testing.T) {
	c := newResponsesFailureTestContext(t)
	event := dto.ResponsesStreamResponse{
		Type: "response.error",
		Response: &dto.OpenAIResponsesResponse{Error: types.OpenAIError{
			Type:    "server_error",
			Message: strings.Repeat("x", upstreamFailureMessageLimit+50),
		}},
	}

	recordResponsesUpstreamFailure(c, nil, event, false)

	raw, ok := common.GetContextKey(c, constant.ContextKeyUpstreamFailure)
	require.True(t, ok)
	failure := raw.(map[string]interface{})
	message, ok := failure["message"].(string)
	require.True(t, ok)
	assert.Len(t, message, upstreamFailureMessageLimit+3)
	assert.True(t, strings.HasSuffix(message, "..."))
}
