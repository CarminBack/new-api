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
	ChannelFailureRateLimited  ChannelFailureClass = "rate_limited"
	ChannelFailurePoolAccount  ChannelFailureClass = "pool_account"
	// ChannelFailureKeyCapability is retained as a stable log value for Sol
	// capability misses. The failure belongs to a pooled account, not the key.
	ChannelFailureKeyCapability ChannelFailureClass = "key_capability"
)

type ChannelFailureDecision struct {
	Class           ChannelFailureClass
	Reason          string
	Retry           bool
	EvictAffinity   bool
	CountForCircuit bool
}

func DecideChannelFailure(c *gin.Context, err *types.NewAPIError, retriesRemaining int, specificChannel bool, allowUncertainRetry bool) ChannelFailureDecision {
	return DecideChannelFailureForModel(c, err, "", retriesRemaining, specificChannel, allowUncertainRetry)
}

// DecideChannelFailureForModel classifies an upstream error with the original
// model available. The model-aware path is used by the main relay loop so a
// narrow key-capability fallback cannot affect unrelated 400 responses.
func DecideChannelFailureForModel(c *gin.Context, err *types.NewAPIError, modelName string, retriesRemaining int, specificChannel bool, allowUncertainRetry bool) ChannelFailureDecision {
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
		responseStarted = channelResponseStarted(c)
	}

	decision := classifyChannelFailure(err, message, path, modelName)
	decision.Retry = decision.Class == ChannelFailureTransient ||
		decision.Class == ChannelFailureChannelFatal ||
		decision.Class == ChannelFailureRateLimited ||
		decision.Class == ChannelFailurePoolAccount ||
		decision.Class == ChannelFailureKeyCapability ||
		(decision.Class == ChannelFailureUncertain && allowUncertainRetry)
	if isPotentiallyNonIdempotentPath(path) {
		decision.Retry = false
		decision.Reason += ":non_idempotent_path"
	}
	if responseStarted {
		decision.Retry = false
		decision.Reason += ":response_started"
		if decision.Reason == "responses_stream_failure:response_started" {
			// A generic late stream failure has no upstream cause. Do not let an
			// already-started, ambiguous response punish the whole channel.
			decision.CountForCircuit = false
		}
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

func classifyChannelFailure(err *types.NewAPIError, message string, path string, modelName string) ChannelFailureDecision {
	if err.StatusCode == http.StatusTooManyRequests {
		return ChannelFailureDecision{
			Class:           ChannelFailureRateLimited,
			Reason:          "upstream_rate_limited",
			EvictAffinity:   true,
			CountForCircuit: true,
		}
	}
	if err.StatusCode == http.StatusGatewayTimeout || err.StatusCode == 524 {
		return ChannelFailureDecision{
			Class:           ChannelFailureUncertain,
			Reason:          "upstream_timeout",
			EvictAffinity:   true,
			CountForCircuit: true,
		}
	}
	if isSolKeyCapabilityFailure(err, message, path, modelName) {
		return ChannelFailureDecision{
			Class:           ChannelFailureKeyCapability,
			Reason:          "sol_key_capability",
			EvictAffinity:   true,
			CountForCircuit: true,
		}
	}
	if isExplicitGatewayCredentialFailure(message) {
		return ChannelFailureDecision{
			Class:           ChannelFailureChannelFatal,
			Reason:          "gateway_credentials",
			EvictAffinity:   true,
			CountForCircuit: true,
		}
	}
	if isPoolAccountFailure(err, message) {
		return ChannelFailureDecision{
			Class:           ChannelFailurePoolAccount,
			Reason:          "pooled_account_unavailable",
			EvictAffinity:   true,
			CountForCircuit: true,
		}
	}
	if isResponsesContentFailure(err, message) {
		return ChannelFailureDecision{
			Class:           ChannelFailureTerminal,
			Reason:          "responses_policy_failure",
			CountForCircuit: false,
		}
	}
	if isResponsesAccountFailure(err, message) {
		return ChannelFailureDecision{
			Class:           ChannelFailurePoolAccount,
			Reason:          "responses_account_failure",
			EvictAffinity:   true,
			CountForCircuit: false,
		}
	}
	if isResponsesRateLimitFailure(err) {
		return ChannelFailureDecision{
			Class:           ChannelFailureRateLimited,
			Reason:          "responses_rate_limited",
			EvictAffinity:   true,
			CountForCircuit: true,
		}
	}
	if isResponsesServiceFailure(err) {
		return ChannelFailureDecision{
			Class:           ChannelFailureUncertain,
			Reason:          "responses_upstream_failure",
			EvictAffinity:   true,
			CountForCircuit: true,
		}
	}
	if (err.GetErrorCode() == types.ErrorCodeBadResponse || err.GetErrorCode() == types.ErrorCodeBadResponseBody) &&
		(strings.Contains(message, "responses stream") || strings.Contains(message, "empty responses stream")) {
		return ChannelFailureDecision{
			Class:           ChannelFailureUncertain,
			Reason:          "responses_stream_failure",
			EvictAffinity:   true,
			CountForCircuit: true,
		}
	}
	if isDeterministicRequestFailure(err, message) {
		return ChannelFailureDecision{Class: ChannelFailureTerminal, Reason: "deterministic_request"}
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

// Responses terminal errors can be emitted after the stream has started. They
// must still keep the no-retry boundary, but account, access, and policy
// failures should not make the whole channel look unhealthy.
func isResponsesAccountFailure(err *types.NewAPIError, message string) bool {
	if err == nil || err.GetErrorType() != types.ErrorTypeOpenAIError {
		return false
	}
	code := strings.ToLower(string(err.GetErrorCode()))
	for _, pattern := range []string{"account", "authentication", "permission", "model_not_supported", "model_access", "subscription"} {
		if strings.Contains(code, pattern) {
			return true
		}
	}
	patterns := []string{
		"account",
		"api key",
		"authentication",
		"permission",
		"not enabled",
		"not supported",
		"subscription",
	}
	for _, pattern := range patterns {
		if strings.Contains(message, pattern) {
			return true
		}
	}
	return false
}

func isResponsesContentFailure(err *types.NewAPIError, message string) bool {
	if err == nil || err.GetErrorType() != types.ErrorTypeOpenAIError {
		return false
	}
	code := strings.ToLower(string(err.GetErrorCode()))
	for _, pattern := range []string{"content_filter", "content_policy", "moderation", "safety", "policy_violation"} {
		if strings.Contains(code, pattern) {
			return true
		}
	}
	for _, pattern := range []string{"content policy", "moderation", "safety policy", "prompt was blocked"} {
		if strings.Contains(message, pattern) {
			return true
		}
	}
	return false
}

func isResponsesRateLimitFailure(err *types.NewAPIError) bool {
	if err == nil || err.GetErrorType() != types.ErrorTypeOpenAIError {
		return false
	}
	switch err.GetErrorCode() {
	case "rate_limit", "rate_limit_exceeded", "too_many_requests":
		return true
	default:
		return false
	}
}

func isResponsesServiceFailure(err *types.NewAPIError) bool {
	if err == nil || err.GetErrorType() != types.ErrorTypeOpenAIError || err.StatusCode < http.StatusInternalServerError {
		return false
	}
	// The generic fallback has no upstream reason and is deliberately not
	// counted after output. Only structured upstream failure codes affect health.
	switch err.GetErrorCode() {
	case "server_error", "upstream_error", "internal_error", "overloaded_error":
		return true
	default:
		return false
	}
}

// NormalizeDeterministicRequestStatus keeps the upstream status in route
// diagnostics while returning a client-correct 400 for request errors that an
// upstream gateway wrapped as 5xx.
func NormalizeDeterministicRequestStatus(err *types.NewAPIError, decision ChannelFailureDecision) {
	if err == nil || err.StatusCode < http.StatusInternalServerError ||
		decision.Class != ChannelFailureTerminal ||
		!strings.HasPrefix(decision.Reason, "deterministic_request") {
		return
	}
	err.StatusCode = http.StatusBadRequest
}

func isSolKeyCapabilityFailure(err *types.NewAPIError, message string, path string, modelName string) bool {
	if err == nil || err.StatusCode != http.StatusBadRequest || !strings.EqualFold(strings.TrimSpace(modelName), "gpt-5.6-sol") {
		return false
	}
	if !isSolCapabilityRetryPath(path) {
		return false
	}
	unsupported := strings.Contains(message, "not supported") ||
		strings.Contains(message, "unsupported") ||
		strings.Contains(message, "does not support") ||
		strings.Contains(message, "doesn't support")
	accountOrCodex := strings.Contains(message, "chatgpt account") || strings.Contains(message, "codex")
	return unsupported && accountOrCodex
}

func isSolCapabilityRetryPath(path string) bool {
	path = strings.TrimSuffix(path, "/")
	switch path {
	case "/v1/chat/completions", "/v1/completions", "/v1/responses":
		return true
	default:
		return false
	}
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
		"invalid request",
		"invalid model",
		"model not found",
		"unsupported model",
		"does not support this model",
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

func isExplicitGatewayCredentialFailure(message string) bool {
	patterns := []string{
		"invalid gateway api key",
		"incorrect gateway api key",
		"invalid channel api key",
		"incorrect channel api key",
		"gateway authentication failed",
	}
	for _, pattern := range patterns {
		if strings.Contains(message, pattern) {
			return true
		}
	}
	return false
}

func isPoolAccountFailure(err *types.NewAPIError, message string) bool {
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
		"account deactivated",
		"account disabled",
		"account suspended",
		"account is not active",
		"subscription expired",
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
		strings.HasPrefix(path, "/kling/v1/videos") ||
		strings.HasPrefix(path, "/mj/") ||
		strings.HasPrefix(path, "/suno/") ||
		strings.HasPrefix(path, "/jimeng")
}
