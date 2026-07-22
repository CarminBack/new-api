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
			wantClass:        ChannelFailureTransient,
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
			name:             "balance 403 is channel fatal",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("insufficient account balance"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(http.StatusForbidden)),
			retriesRemaining: 2,
			wantClass:        ChannelFailureChannelFatal,
			wantRetry:        true,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "balance skip marker is still channel fatal",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("insufficient account balance"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(http.StatusForbidden), types.ErrOptionWithSkipRetry()),
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
			wantClass:        ChannelFailureTransient,
			wantEvict:        true,
			wantCountCircuit: true,
		},
		{
			name:             "specific channel disables retry",
			path:             "/v1/responses",
			apiErr:           types.NewError(errors.New("rate limited"), types.ErrorCodeBadResponse, types.ErrOptionWithStatusCode(http.StatusTooManyRequests)),
			retriesRemaining: 2,
			specificChannel:  true,
			wantClass:        ChannelFailureTransient,
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
