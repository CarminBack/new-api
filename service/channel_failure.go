package service

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

type ChannelFailureClass string

const (
	ChannelFailureTerminal     ChannelFailureClass = "terminal"
	ChannelFailureTransient    ChannelFailureClass = "transient"
	ChannelFailureChannelFatal ChannelFailureClass = "channel_fatal"
	ChannelFailureUncertain    ChannelFailureClass = "uncertain"
)

type ChannelFailureDecision struct {
	Class           ChannelFailureClass
	Reason          string
	Retry           bool
	EvictAffinity   bool
	CountForCircuit bool
}

func DecideChannelFailure(c *gin.Context, err *types.NewAPIError, retriesRemaining int, specificChannel bool, allowUncertainRetry bool) ChannelFailureDecision {
	if err == nil {
		return ChannelFailureDecision{Class: ChannelFailureTerminal, Reason: "no_error"}
	}

	message := strings.ToLower(err.Error())
	path := ""
	responseStarted := false
	if c != nil {
		if c.Request != nil && c.Request.URL != nil {
			path = c.Request.URL.Path
		}
		if c.Writer != nil {
			responseStarted = c.Writer.Written()
		}
	}

	decision := classifyChannelFailure(err, message, path)
	decision.Retry = decision.Class == ChannelFailureTransient ||
		decision.Class == ChannelFailureChannelFatal ||
		(decision.Class == ChannelFailureUncertain && allowUncertainRetry)
	if responseStarted {
		decision.Retry = false
		decision.Reason += ":response_started"
	}
	if retriesRemaining <= 0 {
		decision.Retry = false
		decision.Reason += ":budget_exhausted"
	}
	if specificChannel {
		decision.Retry = false
		decision.Reason += ":specific_channel"
	}
	return decision
}

func classifyChannelFailure(err *types.NewAPIError, message string, path string) ChannelFailureDecision {
	if err.StatusCode == http.StatusGatewayTimeout || err.StatusCode == 524 {
		return ChannelFailureDecision{
			Class:           ChannelFailureUncertain,
			Reason:          "upstream_timeout",
			EvictAffinity:   true,
			CountForCircuit: true,
		}
	}
	if isDeterministicRequestFailure(err, message) {
		return ChannelFailureDecision{Class: ChannelFailureTerminal, Reason: "deterministic_request"}
	}
	if isChannelFatalFailure(err, message) {
		return ChannelFailureDecision{
			Class:           ChannelFailureChannelFatal,
			Reason:          "channel_credentials_or_balance",
			EvictAffinity:   true,
			CountForCircuit: true,
		}
	}
	if types.IsChannelError(err) {
		return ChannelFailureDecision{
			Class:           ChannelFailureTransient,
			Reason:          "channel_error",
			EvictAffinity:   true,
			CountForCircuit: true,
		}
	}
	if types.IsSkipRetryError(err) {
		return ChannelFailureDecision{Class: ChannelFailureTerminal, Reason: "explicit_skip_retry"}
	}
	if err.GetErrorCode() == types.ErrorCodeBadResponseBody && isPotentiallyNonIdempotentPath(path) {
		return ChannelFailureDecision{
			Class:           ChannelFailureUncertain,
			Reason:          "non_idempotent_bad_response",
			EvictAffinity:   true,
			CountForCircuit: true,
		}
	}
	if operation_setting.IsAlwaysSkipRetryStatusCode(err.StatusCode) {
		return ChannelFailureDecision{
			Class:           ChannelFailureUncertain,
			Reason:          "configured_uncertain_status",
			EvictAffinity:   true,
			CountForCircuit: true,
		}
	}
	if err.StatusCode >= 200 && err.StatusCode < 300 {
		return ChannelFailureDecision{Class: ChannelFailureTerminal, Reason: "successful_status"}
	}
	if err.StatusCode < 100 || err.StatusCode > 599 || operation_setting.ShouldRetryByStatusCode(err.StatusCode) {
		return ChannelFailureDecision{
			Class:           ChannelFailureTransient,
			Reason:          "retryable_status",
			EvictAffinity:   true,
			CountForCircuit: true,
		}
	}
	return ChannelFailureDecision{Class: ChannelFailureTerminal, Reason: "non_retryable_status"}
}

func isDeterministicRequestFailure(err *types.NewAPIError, message string) bool {
	if err.StatusCode == http.StatusBadRequest ||
		err.StatusCode == http.StatusRequestTimeout ||
		err.StatusCode == http.StatusRequestEntityTooLarge ||
		err.StatusCode == http.StatusUnprocessableEntity {
		return true
	}
	patterns := []string{
		"exceeds the context window",
		"context length exceeded",
		"maximum context length",
		"input is too long",
		"messages must contain the word 'json'",
		"messages must contain the word \"json\"",
		"content policy",
		"prompt was blocked",
		"prompt is blocked",
		"safety policy",
		"flagged by the moderation",
	}
	for _, pattern := range patterns {
		if strings.Contains(message, pattern) {
			return true
		}
	}
	return false
}

func isChannelFatalFailure(err *types.NewAPIError, message string) bool {
	if err.StatusCode == http.StatusUnauthorized || err.StatusCode == http.StatusPaymentRequired {
		return true
	}
	patterns := []string{
		"insufficient balance",
		"insufficient account balance",
		"insufficient tokens",
		"credit balance is too low",
		"invalid api key",
		"incorrect api key",
		"not enabled for this group",
	}
	for _, pattern := range patterns {
		if strings.Contains(message, pattern) {
			return true
		}
	}
	return false
}

func isPotentiallyNonIdempotentPath(path string) bool {
	return strings.HasPrefix(path, "/v1/images/") ||
		strings.HasPrefix(path, "/v1/video") ||
		strings.HasPrefix(path, "/mj/") ||
		strings.HasPrefix(path, "/suno/") ||
		strings.HasPrefix(path, "/jimeng")
}
