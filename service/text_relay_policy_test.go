package service

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTextRelayPolicyExcludesImagesAndHostedTools(t *testing.T) {
	for _, tc := range []struct {
		name, path, body, group string
		eligible                bool
	}{
		{"chat", "/v1/chat/completions", `{}`, "default", true},
		{"responses functions", "/v1/responses", `{"tools":[{"type":"function","name":"lookup"}]}`, "default", true},
		{"image endpoint", "/v1/images/generations", `{}`, "default", false},
		{"image group", "/v1/chat/completions", `{}`, "Image", false},
		{"image modality", "/v1/chat/completions", `{"modalities":["image"]}`, "default", false},
		{"image tool", "/v1/responses", `{"tools":[{"type":"image_generation"}]}`, "default", false},
		{"hosted tool", "/v1/responses", `{"tools":[{"type":"mcp"}]}`, "default", false},
		{"background", "/v1/responses", `{"background":true}`, "default", false},
		{"embeddings", "/v1/embeddings", `{"input":"hello"}`, "default", true},
		{"compact", "/v1/responses/compact", `{"input":[]}`, "default", true},
		{"gemini invalid tools", "/v1beta/models/gemini-test:generateContent", `{"tools":{}}`, "default", false},
		{"gemini invalid modality", "/v1beta/models/gemini-test:generateContent", `{"generationConfig":{"responseModalities":"IMAGE"}}`, "default", false},
		{"gemini native", "/v1beta/models/gemini-test:generateContent", `{}`, "default", true},
		{"gemini stream", "/v1beta/models/gemini-test:streamGenerateContent", `{"generationConfig":{"responseModalities":["TEXT"]}}`, "default", true},
		{"gemini image", "/v1beta/models/gemini-test:generateContent", `{"generationConfig":{"responseModalities":["TEXT","IMAGE"]}}`, "default", false},
		{"gemini hosted tool", "/v1beta/models/gemini-test:generateContent", `{"tools":[{"googleSearch":{}}]}`, "default", false},
		{"gemini function", "/v1beta/models/gemini-test:generateContent", `{"tools":[{"functionDeclarations":[{"name":"lookup"}]}]}`, "default", true},
		{"malformed", "/v1/responses", `broken`, "default", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			c.Set("group", tc.group)
			assert.Equal(t, tc.eligible, IsTextRelayRequest(c))
			cancel := PrepareTextRelayContext(c, time.Minute)
			defer cancel()
			_, hasDeadline := c.Request.Context().Deadline()
			assert.Equal(t, tc.eligible, hasDeadline)
		})
	}
}

func TestTextRelayDeadlinePreservesParentAndStopsRetryWithoutHealthPenalty(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{}`)).WithContext(ctx)
	end := PrepareTextRelayContext(c, time.Hour)
	defer end()
	parentDeadline, _ := ctx.Deadline()
	deadline, _ := c.Request.Context().Deadline()
	assert.Equal(t, parentDeadline, deadline)
	decision := DecideChannelFailureForModel(c, types.NewError(context.DeadlineExceeded, types.ErrorCodeDoRequestFailed), "model", 3, false, true)
	assert.False(t, decision.Retry)
	assert.False(t, decision.CountForCircuit)
	assert.False(t, decision.EvictAffinity)
	assert.Equal(t, "text_request_context_done", decision.Reason)
}

func TestTextRelayCompletionRecordsAffinityButCancellationDoesNot(t *testing.T) {
	for _, outcome := range []string{"success", "deadline", "client_cancel"} {
		t.Run(outcome, func(t *testing.T) {
			setupChannelHealthTest(t)
			high, _ := failoverChannels(t)
			c := failoverContext()
			parent, cancelParent := context.WithCancel(c.Request.Context())
			defer cancelParent()
			c.Request = c.Request.WithContext(parent)
			c.Set("channel_id", high.Id)
			key := t.Name()
			setChannelAffinityContext(c, channelAffinityMeta{CacheKey: key, TTLSeconds: 60})
			t.Cleanup(func() { _, _ = getChannelAffinityCache().DeleteMany([]string{key}) })
			budget := time.Minute
			if outcome == "deadline" {
				budget = time.Nanosecond
			}
			finish := PrepareTextRelayContext(c, budget)
			relayCtx := c.Request.Context()
			if outcome == "deadline" {
				<-relayCtx.Done()
			} else if outcome == "client_cancel" {
				cancelParent()
			}
			if outcome == "success" {
				MarkRequestPolicySuccess(c, nil)
			}
			finish()
			RecordChannelAffinity(c, high.Id)
			cached, found, err := getChannelAffinityCache().Get(key)
			require.NoError(t, err)
			require.Equal(t, outcome == "success", found)
			if outcome == "success" {
				require.Equal(t, high.Id, cached)
				require.NoError(t, c.Request.Context().Err())
			}
			require.Error(t, relayCtx.Err(), "relay deadline resources must be released")
		})
	}
}

func TestTextTimeoutEffortUsesOriginalProtocolBody(t *testing.T) {
	for _, tc := range []struct {
		path, body, effort string
	}{
		{"/v1/responses", `{"reasoning":{"effort":"max"}}`, "max"},
		{"/v1/chat/completions", `{"reasoning_effort":"xhigh"}`, "xhigh"},
		{"/v1/messages", `{"output_config":{"effort":"high"}}`, "high"},
		{"/v1/responses", `{}`, ""},
		{"/v1/responses", `{"reasoning":{"effort":123}}`, ""},
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
		require.Equal(t, tc.effort, TextRequestReasoningEffort(c))
		c.Request = httptest.NewRequest("POST", tc.path, strings.NewReader(`{"reasoning":{"effort":"low"}}`))
		require.Equal(t, tc.effort, TextRequestReasoningEffort(c), "channel overrides must not change the original timeout class")
	}
}

func TestTextFirstResponseBudgetIsSharedAcrossAttempts(t *testing.T) {
	oldTotal, oldMinimum := common.TextFirstResponseTotalTimeout, common.TextRetryMinRemainingSeconds
	common.TextFirstResponseTotalTimeout, common.TextRetryMinRemainingSeconds = 90, 5
	t.Cleanup(func() {
		common.TextFirstResponseTotalTimeout, common.TextRetryMinRemainingSeconds = oldTotal, oldMinimum
	})
	for _, tc := range []struct {
		name    string
		elapsed time.Duration
		retry   bool
	}{
		{"enough for failover", 45 * time.Second, true},
		{"too little for failover", 88 * time.Second, false},
		{"expired", 91 * time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := failoverContext()
			start := time.Now().Add(-tc.elapsed)
			common.SetContextKey(c, constant.ContextKeyRequestStartTime, start)
			deadline, enabled := TextFirstResponseDeadline(c)
			require.True(t, enabled)
			assert.Equal(t, start.Add(90*time.Second), deadline)
			// A later attempt must retain the original deadline.
			common.SetContextKey(c, constant.ContextKeyRequestStartTime, time.Now())
			next, _ := TextFirstResponseDeadline(c)
			assert.Equal(t, deadline, next)
			decision := DecideChannelFailureForModel(c, types.NewErrorWithStatusCode(context.DeadlineExceeded, types.ErrorCodeBadResponseStatusCode, 502), "gpt-test", 3, false, true)
			assert.Equal(t, tc.retry, decision.Retry)
			if !tc.retry {
				assert.Contains(t, decision.Reason, "first_response_budget_exhausted")
			}
			require.NoError(t, c.Request.Context().Err(), "pre-response budget must not cancel established streams")
		})
	}
	c := failoverContext()
	common.TextFirstResponseTotalTimeout = 0
	_, enabled := TextFirstResponseDeadline(c)
	assert.False(t, enabled, "default preserves existing behavior")
	common.TextFirstResponseTotalTimeout = 90
	c.Request.URL.Path = "/v1/images/generations"
	c.Set(textRelayEligibilityKey, false)
	_, enabled = TextFirstResponseDeadline(c)
	assert.False(t, enabled, "media requests keep existing deadlines")
}

func TestTextAdaptiveWeightNeedsSamplesPreservesFloorAndExpires(t *testing.T) {
	now := setupChannelHealthTest(t)
	enabled := common.TextAdaptiveRoutingEnabled
	common.TextAdaptiveRoutingEnabled = true
	textChannelLatency.Lock()
	old := textChannelLatency.samples
	textChannelLatency.samples = make(map[textLatencyKey]textLatencySample)
	textChannelLatency.Unlock()
	t.Cleanup(func() {
		common.TextAdaptiveRoutingEnabled = enabled
		textChannelLatency.Lock()
		textChannelLatency.samples = old
		textChannelLatency.Unlock()
	})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{}`))
	param := &RetryParam{Ctx: c, ModelName: "model", RequestPath: "/v1/responses"}
	key := textLatencyKey{7, "model", "/v1/responses"}
	for range 7 {
		recordTextLatencySample(key, 120000)
	}
	require.Equal(t, 1.0, textChannelWeightFactor(param)(7))
	recordTextLatencySample(key, 120000)
	assert.Equal(t, 0.25, textChannelWeightFactor(param)(7), "slow channels retain a recovery traffic floor")
	assert.Equal(t, 1.0, textChannelWeightFactor(param)(8), "unobserved channels retain configured weight")
	param.ModelName = "other"
	assert.Equal(t, 1.0, textChannelWeightFactor(param)(7), "models must not contaminate one another")
	param.ModelName = "model"
	for range 12 {
		recordTextLatencySample(key, 1000)
	}
	assert.Equal(t, 1.0, textChannelWeightFactor(param)(7), "successful fast samples restore weight")
	*now = now.Add(11 * time.Minute)
	assert.Equal(t, 1.0, textChannelWeightFactor(param)(7))
	common.TextAdaptiveRoutingEnabled = false
	assert.Nil(t, textChannelWeightFactor(param))
}
