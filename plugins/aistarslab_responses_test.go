package plugins_test

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAistarsLabResponsesProtocol(t *testing.T) {
	testVideoResponsesProtocol(t, videoResponsesTestCase{
		pluginKey: "aistarslab",
		model:     "seedance-720p-c47",
		requestBody: map[string]any{
			"model":   "seedance-720p-c47",
			"input":   "waves at sunset",
			"seconds": 8,
			"size":    "16:9",
		},
		wantAction: "generate",
		wantRequest: map[string]any{
			"model":   "seedance-720p-c47",
			"prompt":  "waves at sunset",
			"seconds": float64(8),
			"size":    "16:9",
		},
		wantUsageKeys:  []string{"seconds", "videos"},
		wantVendorName: "aistarslab",
	})
}

func TestAistarsLabBuildSubmitRequestUsesProviderContract(t *testing.T) {
	plugin := compileAistarsLabPlugin(t)
	value, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{
		"baseUrl":       "https://api.video.aistarslab.com/openai/v1/",
		"apiKey":        "secret",
		"upstreamModel": "47:seedance-2.0",
		"requestBody": map[string]any{
			"model":   "seedance-720p-c47",
			"prompt":  "move between frames",
			"seconds": 7,
			"size":    "9:16",
			"images": []any{
				"https://cdn.example/first.png",
				"https://cdn.example/last.png",
			},
			"metadata": map[string]any{
				"mode_type": "frames2video",
				"ignored":   "must-not-pass-through",
			},
		},
	})
	require.NoError(t, err)
	request := decodePluginMap(t, value)
	assert.Equal(t, "https://api.video.aistarslab.com/openai/v1/videos", request["url"])
	headers := request["headers"].(map[string]any)
	assert.Equal(t, "Bearer secret", headers["Authorization"])
	body := request["body"].(map[string]any)
	assert.Equal(t, "47:seedance-2.0", body["model"])
	assert.Equal(t, "7", body["seconds"])
	assert.Equal(t, float64(1), body["n"])
	metadata := body["metadata"].(map[string]any)
	assert.Equal(t, "frames2video", metadata["mode_type"])
	assert.Equal(t, "720p", metadata["resolution"])
	assert.NotContains(t, metadata, "ignored")
}

func TestAistarsLabRejectsUnsupportedInputs(t *testing.T) {
	plugin := compileAistarsLabPlugin(t)
	tests := []struct {
		name    string
		body    map[string]any
		message string
	}{
		{
			name: "unsupported reference scheme",
			body: map[string]any{
				"model":  "seedance-720p-c47",
				"prompt": "animate",
				"images": []any{"ftp://cdn.example/image.png"},
			},
			message: "reference image must be an http(s) URL",
		},
		{
			name: "channel 50 minimum duration",
			body: map[string]any{
				"model":   "seedance-720p-c50",
				"prompt":  "animate",
				"seconds": 4,
			},
			message: "seconds must be between 5 and 15",
		},
		{
			name: "frames require two images",
			body: map[string]any{
				"model":  "seedance-720p-c47",
				"prompt": "animate",
				"images": []any{"https://cdn.example/first.png"},
				"metadata": map[string]any{
					"mode_type": "frames2video",
				},
			},
			message: "frames2video requires exactly two images",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{
				"baseUrl":       "https://api.video.aistarslab.com/openai",
				"apiKey":        "secret",
				"upstreamModel": testCase.body["model"],
				"requestBody":   testCase.body,
			})
			require.ErrorContains(t, err, testCase.message)
		})
	}
}

func TestAistarsLabParsesTaskResult(t *testing.T) {
	plugin := compileAistarsLabPlugin(t)
	value, err := plugin.Engine.Call(t.Context(), "parseTaskResult", map[string]any{}, map[string]any{
		"status":   "completed",
		"progress": 100,
		"metadata": map[string]any{"result_url": "https://cdn.example/result.mp4"},
	})
	require.NoError(t, err)
	result := decodePluginMap(t, value)
	assert.Equal(t, "SUCCESS", result["status"])
	assert.Equal(t, "100%", result["progress"])
	assert.Equal(t, "https://cdn.example/result.mp4", result["url"])
}

func compileAistarsLabPlugin(t *testing.T) *jsplugin.LoadedPlugin {
	t.Helper()
	source, err := builtinplugins.Source("aistarslab")
	require.NoError(t, err)
	registry := jsplugin.NewRegistry()
	plugin, err := registry.RegisterFactory(source, jsplugin.Options{Key: "aistarslab"})
	require.NoError(t, err)
	return plugin
}

func decodePluginMap(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := common.Marshal(value)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, common.Unmarshal(data, &decoded))
	return decoded
}
