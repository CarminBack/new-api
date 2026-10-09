package common

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestTextTimeoutRuleOrderScopeAndFallback(t *testing.T) {
	old, oldDefault := textFirstResponseRules, TextFirstResponseTimeout
	t.Cleanup(func() { textFirstResponseRules = old; TextFirstResponseTimeout = oldDefault })
	TextFirstResponseTimeout = 90
	var err error
	textFirstResponseRules, err = ParseTextFirstResponseRules(`[
 {"model_pattern":"reasoning-*","seconds":180},
 {"model_pattern":"gpt-*","request_path":"/v1/responses","seconds":45},
 {"model_pattern":"unlimited","seconds":0},
 {"model_pattern":"org/model-*","seconds":120}]`)
	require.NoError(t, err)
	for _, tc := range []struct {
		model, path string
		seconds     int
	}{
		{"reasoning-large", "/v1/responses", 180},
		{"gpt-test", "/v1/responses", 45},
		{"gpt-test", "/v1/chat/completions", 90},
		{"unlimited", "/v1/responses", 0},
		{"org/model-large", "/v1/responses", 120},
		{"unknown", "/v1/responses", 90},
	} {
		require.Equal(t, tc.seconds, TextFirstResponseSeconds(tc.model, tc.path))
	}
}

func TestTextTotalTimeoutRulesAllowScopedRollout(t *testing.T) {
	oldRules, oldDefault := textFirstResponseRules, TextFirstResponseTotalTimeout
	t.Cleanup(func() { textFirstResponseRules, TextFirstResponseTotalTimeout = oldRules, oldDefault })
	TextFirstResponseTotalTimeout = 0
	var err error
	textFirstResponseRules, err = ParseTextFirstResponseRules(`[
 {"model_pattern":"gpt-*","request_path":"/v1/responses","reasoning_effort":"max","seconds":120,"total_seconds":0},
 {"model_pattern":"gpt-*","request_path":"/v1/responses","reasoning_effort":"low","seconds":45,"total_seconds":75},
 {"model_pattern":"canary-*","request_path":"/v1/responses","seconds":45,"total_seconds":90},
 {"model_pattern":"long-*","seconds":120,"total_seconds":0},
 {"model_pattern":"legacy-*","seconds":60}]`)
	require.NoError(t, err)
	for _, tc := range []struct {
		model, path string
		total       int
	}{
		{"canary-test", "/v1/responses", 90},
		{"canary-test", "/v1/chat/completions", 0},
		{"long-test", "/v1/responses", 0},
		{"legacy-test", "/v1/responses", 0},
		{"other", "/v1/responses", 0},
	} {
		require.Equal(t, tc.total, TextFirstResponseTotalSeconds(tc.model, tc.path))
	}
	TextFirstResponseTotalTimeout = 100
	require.Equal(t, 0, TextFirstResponseTotalSeconds("long-test", "/v1/responses"), "explicit zero overrides global budget")
	require.Equal(t, 100, TextFirstResponseTotalSeconds("legacy-test", "/v1/responses"), "old rules inherit global budget")
	require.Equal(t, 0, TextFirstResponseTotalSeconds("gpt-test", "/v1/responses", "max"))
	require.Equal(t, 120, TextFirstResponseSeconds("gpt-test", "/v1/responses", "max"))
	require.Equal(t, 75, TextFirstResponseTotalSeconds("gpt-test", "/v1/responses", "low"))
	require.Equal(t, 45, TextFirstResponseSeconds("gpt-test", "/v1/responses", "low"))
	require.Equal(t, 100, TextFirstResponseTotalSeconds("gpt-test", "/v1/responses", "xhigh"), "unmatched effort retains the default")
	require.Equal(t, 100, TextFirstResponseTotalSeconds("gpt-test", "/v1/responses"), "missing effort must not accidentally match a fast class")
	require.Equal(t, 100, TextFirstResponseTotalSeconds("gpt-test", "/v1/chat/completions", "low"))
}

func TestTextTimeoutRulesRejectInvalidConfiguration(t *testing.T) {
	for _, raw := range []string{`{}`, `[{"model_pattern":"[","seconds":45}]`, `[{"seconds":45}]`,
		`[{"model_pattern":"*"}]`, `[{"model_pattern":"*","seconds":-1}]`,
		`[{"model_pattern":"*","seconds":86401}]`, `[{"model_pattern":"*","seconds":1.5}]`,
		`[{"model_pattern":"*","seconds":1,"total_seconds":-1}]`,
		`[{"model_pattern":"*","seconds":1,"total_seconds":86401}]`,
		`[{"model_pattern":"*","seconds":1,"total_seconds":1.5}]`,
		`[{"model_pattern":"*","seconds":45,"request_path":"responses"}]`,
	} {
		_, err := ParseTextFirstResponseRules(raw)
		require.Error(t, err, raw)
	}
}
