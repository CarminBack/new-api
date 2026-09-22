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

func TestTextTimeoutRulesRejectInvalidConfiguration(t *testing.T) {
	for _, raw := range []string{`{}`, `[{"model_pattern":"[","seconds":45}]`, `[{"seconds":45}]`,
		`[{"model_pattern":"*"}]`, `[{"model_pattern":"*","seconds":-1}]`,
		`[{"model_pattern":"*","seconds":86401}]`, `[{"model_pattern":"*","seconds":1.5}]`,
		`[{"model_pattern":"*","seconds":45,"request_path":"responses"}]`,
	} {
		_, err := ParseTextFirstResponseRules(raw)
		require.Error(t, err, raw)
	}
}
