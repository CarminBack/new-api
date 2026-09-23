package controller

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	dto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Image requests are non-idempotent and billed per image, so their fallback
// count must be capped regardless of the configured RetryTimes.
func TestRelayRetriesRemainingCapsImageFallback(t *testing.T) {
	// Configured 5 retries, image path: only maxImageFallbacks are allowed.
	require.Equal(t, maxImageFallbacks, relayRetriesRemaining("/v1/images/generations", 0, 5))
	require.Equal(t, 1, relayRetriesRemaining("/v1/images/generations", 1, 5))
	require.Equal(t, 0, relayRetriesRemaining("/v1/images/generations", 2, 5))
	require.Equal(t, 0, relayRetriesRemaining("/v1/images/edits", 3, 5))
	// Non-image paths keep the configured budget.
	require.Equal(t, 5, relayRetriesRemaining("/v1/chat/completions", 0, 5))
	require.Equal(t, 0, relayRetriesRemaining("/v1/chat/completions", 5, 5))
	// A lower configured budget is never raised by the image cap.
	require.Equal(t, 1, relayRetriesRemaining("/v1/images/generations", 0, 1))
}

// The shared retry budget must not gate the first cross-channel fallback, and
// must never gate image fallback at all.
func TestShouldEnforceChannelRetryBudget(t *testing.T) {
	assert.False(t, shouldEnforceChannelRetryBudget("/v1/chat/completions", 0), "the first fallback is always allowed")
	assert.True(t, shouldEnforceChannelRetryBudget("/v1/chat/completions", 1))
	assert.True(t, shouldEnforceChannelRetryBudget("/v1/chat/completions", 2))
	assert.False(t, shouldEnforceChannelRetryBudget("/v1/images/generations", 0), "image fallback has its own gate")
	assert.False(t, shouldEnforceChannelRetryBudget("/v1/images/generations", 1))
	assert.False(t, shouldEnforceChannelRetryBudget("/v1/images/edits", 1))
}

// Terminal errors must not evict a channel: a local rejection is not evidence
// the channel is unhealthy.
func TestShouldExcludeChannelForRetry(t *testing.T) {
	for _, class := range []service.ChannelFailureClass{
		service.ChannelFailureTransient,
		service.ChannelFailureUncertain,
		service.ChannelFailureRateLimited,
		service.ChannelFailureKeyCapability,
		service.ChannelFailurePoolAccount,
	} {
		assert.True(t, shouldExcludeChannelForRetry(class), "class %s should be excluded", class)
	}
	for _, class := range []service.ChannelFailureClass{
		service.ChannelFailureTerminal,
		service.ChannelFailureChannelFatal,
	} {
		assert.False(t, shouldExcludeChannelForRetry(class), "class %s should stay eligible", class)
	}
}

// A task policy decision maps onto the route-attempt failure vocabulary.
func TestTaskFailureClassForAttempt(t *testing.T) {
	assert.Equal(t, service.ChannelFailureTransient,
		taskFailureClassForAttempt(service.PolicyDecision{Action: "retry", Reason: "retry_status_matched"}))
	assert.Equal(t, service.ChannelFailureTerminal,
		taskFailureClassForAttempt(service.PolicyDecision{Action: "stop", Reason: "general_retry_limit"}))
	assert.Equal(t, service.ChannelFailureTerminal,
		taskFailureClassForAttempt(service.PolicyDecision{Action: "stop", Reason: "attempt_budget_exhausted"}))
}

// Only safe text shapes may replay after an uncertain failure; hosted image
// generation must never be replayed.
func TestAllowsUncertainCrossChannelRetryGatesImageTool(t *testing.T) {
	imageTool := []byte(`[{"type":"image_generation"}]`)
	functionTool := []byte(`[{"type":"function","name":"lookup"}]`)

	responsesInfo := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeResponses}
	assert.False(t, allowsUncertainCrossChannelRetry(responsesInfo, &dto.OpenAIResponsesRequest{Tools: imageTool}),
		"an image_generation tool must not be replayed")
	assert.True(t, allowsUncertainCrossChannelRetry(responsesInfo, &dto.OpenAIResponsesRequest{Tools: functionTool}))

	chatInfo := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeChatCompletions}
	assert.True(t, allowsUncertainCrossChannelRetry(chatInfo, nil))

	// Non-text relay modes are never eligible.
	imageInfo := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesGenerations}
	assert.False(t, allowsUncertainCrossChannelRetry(imageInfo, nil))

	assert.False(t, allowsUncertainCrossChannelRetry(nil, nil))
}
