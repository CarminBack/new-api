package plugins_test

import (
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	taskjsplugin "github.com/QuantumNous/new-api/relay/channel/task/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnifiedVideoParameters(t *testing.T) {
	for _, provider := range []struct{ key, model, base string }{
		{"aistarslab", "seedance-720p-c49", "https://www.aistarslab.com"},
		{"v6k", "TTseadanco2.5-720", "https://www.v6k.top"},
		{"aigas", "sd-2.5-官方版-720p", "https://www.aigasapi.com"},
	} {
		t.Run(provider.key, func(t *testing.T) {
			source, err := builtinplugins.Source(provider.key)
			require.NoError(t, err)
			plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: provider.key})
			require.NoError(t, err)
			base := map[string]any{"model": provider.model, "prompt": "animate", "seconds": 8, "aspect_ratio": "9:16", "resolution": "720p"}
			decode := func(req map[string]any) (map[string]any, error) {
				value, callErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
					"model": provider.model, "upstreamModel": provider.model,
					"body": map[string]any{"kind": "json", "value": req},
				})
				if callErr != nil {
					return nil, callErr
				}
				return decodePluginMap(t, value)["requestBody"].(map[string]any), nil
			}
			reference := func(kind, role, url string) map[string]any {
				return map[string]any{"type": kind, "role": role, "url": url}
			}
			for _, scenario := range []string{"text", "image", "video", "first_frame"} {
				t.Run(scenario, func(t *testing.T) {
					req := maps.Clone(base)
					switch scenario {
					case "image":
						req["references"] = []any{reference("image", "reference", "https://cdn.example/image.png")}
					case "video":
						ref := reference("video", "reference", "https://cdn.example/ref.mp4")
						ref["duration_seconds"] = 6.5
						req["references"] = []any{ref}
					case "first_frame":
						req["references"] = []any{reference("image", "first_frame", "https://cdn.example/first.png")}
					}
					before, marshalErr := common.Marshal(req)
					require.NoError(t, marshalErr)
					body, callErr := decode(req)
					after, marshalErr := common.Marshal(req)
					require.NoError(t, marshalErr)
					assert.JSONEq(t, string(before), string(after), "decoding must not mutate the caller request")
					require.NoError(t, callErr)
					adaptor := taskjsplugin.New(plugin)
					info := &relaycommon.RelayInfo{OriginModelName: provider.model, ChannelMeta: &relaycommon.ChannelMeta{
						ChannelType: 61, ChannelBaseUrl: provider.base, ApiKey: "test-key", UpstreamModelName: provider.model,
					}, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
					adaptor.Init(info)
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
					c.Set("task_request", body)
					require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
					facts, callErr := adaptor.ExtractUsageFactsValidated(c, info)
					require.NoError(t, callErr)
					if scenario == "video" {
						assert.Equal(t, "video", facts["video_input"])
					} else {
						assert.Equal(t, "none", facts["video_input"])
					}
					reader, callErr := adaptor.BuildRequestBody(c, info)
					require.NoError(t, callErr)
					encoded, callErr := io.ReadAll(reader)
					require.NoError(t, callErr)
					var outbound map[string]any
					require.NoError(t, common.Unmarshal(encoded, &outbound))
					assert.Equal(t, provider.model, outbound["model"])
					switch provider.key {
					case "aistarslab":
						assert.Equal(t, "9:16", outbound["size"])
						assert.Equal(t, "8", outbound["seconds"])
						meta := outbound["metadata"].(map[string]any)
						if scenario == "image" || scenario == "first_frame" {
							assert.Len(t, meta["images"], 1)
						}
						if scenario == "video" {
							assert.Equal(t, []any{"https://cdn.example/ref.mp4"}, meta["videos"])
						}
						assert.NotContains(t, outbound, "references")
					case "v6k":
						assert.Equal(t, "9:16", outbound["ratio"])
						if scenario == "video" {
							assert.Equal(t, float64(-1), outbound["seconds"])
							assert.Equal(t, 6.5, facts["video_seconds"])
							assert.Len(t, outbound["videos"], 1)
						} else {
							assert.Equal(t, float64(8), outbound["seconds"])
						}
						if scenario == "image" {
							assert.Equal(t, []any{"https://cdn.example/image.png"}, outbound["images"])
						}
						if scenario == "first_frame" {
							assert.Equal(t, "https://cdn.example/first.png", outbound["input_reference"])
						}
						assert.NotContains(t, outbound, "references")
					case "aigas":
						assert.Equal(t, "9:16", outbound["aspect_ratio"])
						assert.Equal(t, float64(8), outbound["duration"])
						if scenario != "text" {
							assert.Len(t, outbound["references"], 1)
						}
					}
				})
			}
			t.Run("reject invalid or conflicting standard fields", func(t *testing.T) {
				for _, patch := range []map[string]any{
					{"duration": 9}, {"ratio": "16:9"}, {"seconds": 3601},
					{"references": nil},
					{"references": []any{map[string]any{"type": "video", "role": "reference", "url": "https://cdn.example/ref.mp4", "duration_seconds": 3601}}},
					{"references": []any{map[string]any{"type": "video", "role": "reference", "url": "https://cdn.example/ref.mp4", "duration_seconds": true}}},
					{"references": []any{map[string]any{"type": "image", "role": "reference", "url": map[string]any{"url": "https://cdn.example/image.png"}}}},
					{"references": []any{reference("image", "last_frame", "https://cdn.example/last.png")}},
					{"references": []any{reference("video", "first_frame", "https://cdn.example/ref.mp4")}},
					{"references": []any{reference("image", "reference", "file:///etc/passwd")}},
					{"references": []any{reference("image", "invalid", "https://cdn.example/image.png")}},
					{"references": []any{reference("image", "reference", "https://cdn.example/image.png")}, "images": []any{"https://cdn.example/other.png"}},
				} {
					req := maps.Clone(base)
					maps.Copy(req, patch)
					_, callErr := decode(req)
					assert.Error(t, callErr, "%v", patch)
				}
			})
			t.Run("mapped model uses the selected provider contract", func(t *testing.T) {
				req := maps.Clone(base)
				req["model"] = "unified-video"
				ctx := map[string]any{"model": "unified-video", "upstreamModel": provider.model, "body": map[string]any{"kind": "json", "value": req}}
				value, callErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, ctx)
				require.NoError(t, callErr)
				intent := decodePluginMap(t, value)
				assert.Equal(t, "unified-video", intent["model"])
				value, callErr = plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{
					"model": "unified-video", "upstreamModel": provider.model, "baseUrl": provider.base, "requestBody": intent["requestBody"],
				})
				require.NoError(t, callErr)
				assert.Equal(t, provider.model, decodePluginMap(t, value)["body"].(map[string]any)["model"])
				if provider.key == "aistarslab" {
					req["seconds"] = 2
					_, callErr = plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, ctx)
					require.ErrorContains(t, callErr, "seconds must be between 4 and 15")
				}
			})
			t.Run("multipart standard references", func(t *testing.T) {
				refs := []any{reference("image", "reference", "https://cdn.example/image.png")}
				encoded, marshalErr := common.Marshal(refs)
				require.NoError(t, marshalErr)
				value, callErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{
					"model": provider.model, "upstreamModel": provider.model,
					"body": map[string]any{"kind": "multipart", "fields": map[string][]string{
						"model": {provider.model}, "prompt": {"animate"}, "seconds": {"8"},
						"aspect_ratio": {"9:16"}, "resolution": {"720p"}, "references": {string(encoded)},
					}},
				})
				require.NoError(t, callErr)
				body := decodePluginMap(t, value)["requestBody"]
				value, callErr = plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{"baseUrl": provider.base, "requestBody": body})
				require.NoError(t, callErr)
				outbound := decodePluginMap(t, value)["body"].(map[string]any)
				switch provider.key {
				case "aistarslab":
					assert.Len(t, outbound["metadata"].(map[string]any)["images"], 1)
				case "v6k":
					assert.Len(t, outbound["images"], 1)
				case "aigas":
					assert.Len(t, outbound["references"], 1)
				}
			})
			t.Run("preserve explicit frame roles", func(t *testing.T) {
				req := maps.Clone(base)
				req["references"] = []any{reference("image", "last_frame", "https://cdn.example/last.png"), reference("image", "first_frame", "https://cdn.example/first.png")}
				body, callErr := decode(req)
				if provider.key == "v6k" {
					require.ErrorContains(t, callErr, "last_frame")
					return
				}
				require.NoError(t, callErr)
				value, callErr := plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{"baseUrl": provider.base, "requestBody": body})
				require.NoError(t, callErr)
				outbound := decodePluginMap(t, value)["body"].(map[string]any)
				if provider.key == "aistarslab" {
					meta := outbound["metadata"].(map[string]any)
					assert.Equal(t, "frames2video", meta["mode_type"])
					assert.Equal(t, []any{"https://cdn.example/first.png", "https://cdn.example/last.png"}, meta["images"])
				} else {
					assert.Len(t, outbound["references"], 2)
				}
			})
		})
	}
}
