package plugins_test

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSoraGrokVideo15RequiresOneReference(t *testing.T) {
	plugin := compileSoraPlugin(t)
	decode := func(body map[string]any) (any, error) {
		return plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
			"body":  map[string]any{"kind": "json", "value": body},
			"model": "grok-video-1.5",
		})
	}

	_, err := decode(map[string]any{"model": "grok-video-1.5", "prompt": "animate", "seconds": 4})
	require.ErrorContains(t, err, "requires exactly one reference image")
	_, err = decode(map[string]any{
		"model": "grok-video-1.5", "prompt": "animate", "seconds": 4,
		"image_urls": []any{"https://cdn.example/one.png", "https://cdn.example/two.png"},
	})
	require.ErrorContains(t, err, "requires exactly one reference image")

	value, err := decode(map[string]any{
		"model": "grok-video-1.5", "prompt": "animate", "seconds": 4,
		"image": "https://cdn.example/one.png",
	})
	require.NoError(t, err)
	resolved := decodePluginValueMap(t, value)
	assert.Equal(t, "image_to_video", resolved["action"])
}

func TestSoraGrokVideoStatusCompatibility(t *testing.T) {
	plugin := compileSoraPlugin(t)
	tests := []struct {
		name       string
		body       map[string]any
		wantStatus string
		wantReason string
	}{
		{name: "unknown is transient", body: map[string]any{"status": "unknown"}, wantStatus: "QUEUED"},
		{name: "succeeded", body: map[string]any{"status": "succeeded"}, wantStatus: "SUCCESS"},
		{name: "string error", body: map[string]any{"status": "failed", "error": "safety rejection"}, wantStatus: "FAILURE", wantReason: "safety rejection"},
		{name: "missing status with error", body: map[string]any{"error": map[string]any{"message": "invalid prompt"}}, wantStatus: "FAILURE", wantReason: "invalid prompt"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			value, err := plugin.Engine.Call(t.Context(), "parseTaskResult", map[string]any{}, testCase.body)
			require.NoError(t, err)
			result := decodePluginValueMap(t, value)
			assert.Equal(t, testCase.wantStatus, result["status"])
			if testCase.wantReason == "" {
				assert.Empty(t, result["reason"])
			} else {
				assert.Equal(t, testCase.wantReason, result["reason"])
			}
		})
	}
}

func compileSoraPlugin(t *testing.T) *jsplugin.LoadedPlugin {
	t.Helper()
	source, err := builtinplugins.Source("sora")
	require.NoError(t, err)
	registry := jsplugin.NewRegistry()
	plugin, err := registry.RegisterFactory(source, jsplugin.Options{Key: "sora"})
	require.NoError(t, err)
	return plugin
}

func decodePluginValueMap(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded, err := common.Marshal(value)
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, common.Unmarshal(encoded, &result))
	return result
}
