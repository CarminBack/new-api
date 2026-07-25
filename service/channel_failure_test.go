package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDecideChannelFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name             string
		path             string
		apiErr           *types.NewAPIError
		retriesRemaining int
		specificChannel  bool
		responseStarted  bool
		allowUncertain   bool
		wantClass        ChannelFailureClass
		wantRetry        bool
		wantEvict        bool
		wantCountCircuit bool
	}{
		{
			name:             "affinity 429 retries and evicts",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("rate limited"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(http.StatusTooManyRequests)),
			retriesRemaining: 2,
			wantClass:        ChannelFailureRateLimited,
			wantRetry:        true,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "affinity 503 retries and evicts",
			path:             "/v1/chat/completions",
			apiErr:           types.NewError(errors.New("unavailable"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(http.StatusServiceUnavailable)),
			retriesRemaining: 2,
			wantClass:        ChannelFailureTransient,
			wantRetry:        true,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "524 is uncertain and does not retry",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("origin timeout"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(524)),
			retriesRemaining: 2,
			wantClass:        ChannelFailureUncertain,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "524 retries when safe text retry is allowed",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("origin timeout"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(524)),
			retriesRemaining: 2,
			allowUncertain:   true,
			wantClass:        ChannelFailureUncertain,
			wantRetry:        true,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "524 does not retry after response started",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("origin timeout"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(524)),
			retriesRemaining: 2,
			responseStarted:  true,
			allowUncertain:   true,
			wantClass:        ChannelFailureUncertain,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "524 does not retry for a specific channel",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("origin timeout"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(524)),
			retriesRemaining: 2,
			specificChannel:  true,
			allowUncertain:   true,
			wantClass:        ChannelFailureUncertain,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "524 does not retry when budget is exhausted",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("origin timeout"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(524)),
			retriesRemaining: 0,
			allowUncertain:   true,
			wantClass:        ChannelFailureUncertain,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "524 skip marker is still uncertain",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("origin timeout"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(524), types.ErrOptionWithSkipRetry()),
			retriesRemaining: 2,
			wantClass:        ChannelFailureUncertain,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "pooled account balance 403 retries without gateway fatal",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("insufficient account balance"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(http.StatusForbidden)),
			retriesRemaining: 2,
			wantClass:        ChannelFailurePoolAccount,
			wantRetry:        true,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "pooled account balance ignores skip marker",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("insufficient account balance"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(http.StatusForbidden), types.ErrOptionWithSkipRetry()),
			retriesRemaining: 2,
			wantClass:        ChannelFailurePoolAccount,
			wantRetry:        true,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "responses account failure does not count channel circuit",
			path:             "/v1/responses",
			apiErr:           types.WithOpenAIError(types.OpenAIError{Type: "upstream_error", Code: "account_error", Message: "account does not support this model"}, http.StatusBadGateway, types.ErrOptionWithSkipRetry()),
			retriesRemaining: 2,
			wantClass:        ChannelFailurePoolAccount,
			wantRetry:        true,
			wantEvict:        true,
			wantCountCircuit: false,
		},
		{
			name:             "structured responses server failure counts channel circuit",
			path:             "/v1/responses",
			apiErr:           types.WithOpenAIError(types.OpenAIError{Type: "upstream_error", Code: "server_error", Message: "upstream unavailable"}, http.StatusBadGateway, types.ErrOptionWithSkipRetry()),
			retriesRemaining: 2,
			wantClass:        ChannelFailureUncertain,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "generic responses stream failure counts channel circuit",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("responses stream error: response.failed"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(http.StatusBadGateway), types.ErrOptionWithSkipRetry()),
			retriesRemaining: 2,
			wantClass:        ChannelFailureUncertain,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "generic late responses stream failure does not count channel circuit",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("responses stream error: response.failed"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(http.StatusBadGateway), types.ErrOptionWithSkipRetry()),
			retriesRemaining: 2,
			responseStarted:  true,
			wantClass:        ChannelFailureUncertain,
			wantEvict:        true,
			wantCountCircuit: false,
		},
		{
			name:             "responses policy failure does not count channel circuit",
			path:             "/v1/responses",
			apiErr:           types.WithOpenAIError(types.OpenAIError{Type: "policy_error", Code: "content_policy", Message: "prompt was blocked by content policy"}, http.StatusBadGateway, types.ErrOptionWithSkipRetry()),
			retriesRemaining: 2,
			wantClass:        ChannelFailureTerminal,
			wantCountCircuit: false,
		},
		{
			name:             "structured content filter does not count channel circuit",
			path:             "/v1/responses",
			apiErr:           types.WithOpenAIError(types.OpenAIError{Type: "policy_error", Code: "content_filter", Message: "request rejected"}, http.StatusBadGateway, types.ErrOptionWithSkipRetry()),
			retriesRemaining: 2,
			wantClass:        ChannelFailureTerminal,
			wantCountCircuit: false,
		},
		{
			name:             "structured responses rate limit uses rate limit health class",
			path:             "/v1/responses",
			apiErr:           types.WithOpenAIError(types.OpenAIError{Type: "rate_limit_error", Code: "rate_limit_exceeded", Message: "too many requests"}, http.StatusBadGateway, types.ErrOptionWithSkipRetry()),
			retriesRemaining: 2,
			wantClass:        ChannelFailureRateLimited,
			wantRetry:        true,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "generic pooled account 401 is not gateway fatal",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("invalid api key"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(http.StatusUnauthorized)),
			retriesRemaining: 2,
			wantClass:        ChannelFailurePoolAccount,
			wantRetry:        true,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "pooled account failure on image is not replayed",
			path:             "/v1/images/generations",
			apiErr:           types.NewError(errors.New("account suspended"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(http.StatusForbidden)),
			retriesRemaining: 2,
			wantClass:        ChannelFailurePoolAccount,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "explicit gateway credential failure stays fatal",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("invalid gateway api key"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(http.StatusUnauthorized)),
			retriesRemaining: 2,
			wantClass:        ChannelFailureChannelFatal,
			wantRetry:        true,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "context window wrapped as 502 is terminal",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("maximum context length exceeded"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(http.StatusBadGateway)),
			retriesRemaining: 2,
			wantClass:        ChannelFailureTerminal,
		},
		{
			name:             "400 is terminal",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("bad request"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(http.StatusBadRequest)),
			retriesRemaining: 2,
			wantClass:        ChannelFailureTerminal,
		},
		{
			name:             "content policy wrapped as 403 is terminal",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("prompt was blocked by the content policy"), types.ErrorCodePromptBlocked, types.ErrOptionWithStatusCode(http.StatusForbidden)),
			retriesRemaining: 2,
			wantClass:        ChannelFailureTerminal,
		},
		{
			name:             "422 validation failure is terminal",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("validation failed"), types.ErrorCodeInvalidRequest, types.ErrOptionWithStatusCode(http.StatusUnprocessableEntity)),
			retriesRemaining: 2,
			wantClass:        ChannelFailureTerminal,
		},
		{
			name:             "bad response body on responses retries",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("malformed response"), types.ErrorCodeBadResponseBody, types.ErrOptionWithStatusCode(http.StatusBadGateway)),
			retriesRemaining: 2,
			wantClass:        ChannelFailureTransient,
			wantRetry:        true,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "bad response body on image is uncertain",
			path:             "/v1/images/generations",
			apiErr:           types.NewError(errors.New("malformed response"), types.ErrorCodeBadResponseBody, types.ErrOptionWithStatusCode(http.StatusBadGateway)),
			retriesRemaining: 2,
			wantClass:        ChannelFailureUncertain,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "image 503 is classified but never replayed",
			path:             "/v1/images/generations",
			apiErr:           types.NewError(errors.New("unavailable"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(http.StatusServiceUnavailable)),
			retriesRemaining: 2,
			wantClass:        ChannelFailureTransient,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "Kling video 503 is classified but never replayed",
			path:             "/kling/v1/videos/text2video",
			apiErr:           types.NewError(errors.New("unavailable"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(http.StatusServiceUnavailable)),
			retriesRemaining: 2,
			wantClass:        ChannelFailureTransient,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "response already written disables retry",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("unavailable"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(http.StatusServiceUnavailable)),
			retriesRemaining: 2,
			responseStarted:  true,
			wantClass:        ChannelFailureTransient,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "retry budget exhausted",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("rate limited"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(http.StatusTooManyRequests)),
			retriesRemaining: 0,
			wantClass:        ChannelFailureRateLimited,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "specific channel disables retry",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("rate limited"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(http.StatusTooManyRequests)),
			retriesRemaining: 2,
			specificChannel:  true,
			wantClass:        ChannelFailureRateLimited,
			wantEvict:        true,
			wantCountCircuit: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(writer)
			ctx.Request = httptest.NewRequest(http.MethodPost, tt.path, nil)
			if tt.responseStarted {
				ctx.Writer.WriteHeaderNow()
			}

			decision := DecideChannelFailure(ctx, tt.apiErr, tt.retriesRemaining, tt.specificChannel, tt.allowUncertain)
			require.Equal(t, tt.wantClass, decision.Class)
			require.Equal(t, tt.wantRetry, decision.Retry)
			require.Equal(t, tt.wantEvict, decision.EvictAffinity)
			require.Equal(t, tt.wantCountCircuit, decision.CountForCircuit)
		})
	}
}

func TestNormalizeDeterministicRequestStatus(t *testing.T) {
	err := types.NewError(
		errors.New("Your input exceeds the context window of this model"),
		types.ErrorCodeBadResponse,
		types.ErrOptionWithStatusCode(http.StatusBadGateway),
	)
	decision := DecideChannelFailureForModel(nil, err, "gpt-5.6-terra", 1, false, false)

	require.Equal(t, ChannelFailureTerminal, decision.Class)
	require.Equal(t, "deterministic_request", decision.Reason)
	NormalizeDeterministicRequestStatus(err, decision)
	require.Equal(t, http.StatusBadRequest, err.StatusCode)
}

func TestDecideChannelFailureForModelSolKeyCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name             string
		model            string
		path             string
		message          string
		responseStarted  bool
		wantClass        ChannelFailureClass
		wantRetry        bool
		wantEvict        bool
		wantCountCircuit bool
	}{
		{
			name:             "unsupported ChatGPT account retries key",
			model:            "gpt-5.6-sol",
			path:             "/v1/chat/completions",
			message:          "This ChatGPT account is not supported for Codex model gpt-5.6-sol",
			wantClass:        ChannelFailureKeyCapability,
			wantRetry:        true,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:      "ordinary JSON 400 stays terminal",
			model:     "gpt-5.6-sol",
			path:      "/v1/chat/completions",
			message:   "messages must contain the word 'json'",
			wantClass: ChannelFailureTerminal,
		},
		{
			name:      "different model stays terminal",
			model:     "gpt-5.5",
			path:      "/v1/chat/completions",
			message:   "This ChatGPT account is not supported for Codex model",
			wantClass: ChannelFailureTerminal,
		},
		{
			name:      "image path does not retry",
			model:     "gpt-5.6-sol",
			path:      "/v1/images/generations",
			message:   "This ChatGPT account is not supported for Codex model",
			wantClass: ChannelFailureTerminal,
		},
		{
			name:             "started response does not retry",
			model:            "gpt-5.6-sol",
			path:             "/v1/responses",
			message:          "This ChatGPT account is not supported for Codex model",
			responseStarted:  true,
			wantClass:        ChannelFailureKeyCapability,
			wantEvict:        true,
			wantCountCircuit: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(writer)
			ctx.Request = httptest.NewRequest(http.MethodPost, tt.path, nil)
			if tt.responseStarted {
				ctx.Writer.WriteHeaderNow()
			}
			err := types.NewError(errors.New(tt.message), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(http.StatusBadRequest))
			decision := DecideChannelFailureForModel(ctx, err, tt.model, 2, false, false)
			require.Equal(t, tt.wantClass, decision.Class)
			require.Equal(t, tt.wantRetry, decision.Retry)
			require.Equal(t, tt.wantEvict, decision.EvictAffinity)
			require.Equal(t, tt.wantCountCircuit, decision.CountForCircuit)
		})
	}
}

func TestResponsesEmptyStreamIsRetryableBeforeOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	err := types.NewError(errors.New("empty responses stream: upstream ended before terminal event"), types.ErrorCodeBadResponseBody, types.ErrOptionWithStatusCode(http.StatusBadGateway))

	decision := DecideChannelFailureForModel(ctx, err, "gpt-5.6-sol", 1, false, true)

	require.Equal(t, ChannelFailureUncertain, decision.Class)
	require.True(t, decision.Retry)
	require.Contains(t, decision.Reason, "responses_stream_failure")
}
