package plugins_test

import (
	"io"
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

func TestAIGASVideoContract(t *testing.T) {
	source, err := builtinplugins.Source("aigas")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "aigas"})
	require.NoError(t, err)
	call := func(hook string, args ...any) map[string]any {
		t.Helper()
		value, callErr := plugin.Engine.Call(t.Context(), hook, args...)
		require.NoError(t, callErr)
		return decodePluginMap(t, value)
	}
	decode := func(body map[string]any) (map[string]any, error) {
		t.Helper()
		value, callErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{"body": map[string]any{"kind": "json", "value": body}})
		if callErr != nil {
			return nil, callErr
		}
		return decodePluginMap(t, value)["requestBody"].(map[string]any), nil
	}
	t.Run("structured references preserve roles and false watermark", func(t *testing.T) {
		refs := []any{map[string]any{"type": "image", "role": "first_frame", "url": "https://cdn.example/first.png"}, map[string]any{"type": "image", "role": "last_frame", "url": "https://cdn.example/last.png"}, map[string]any{"type": "video", "role": "reference", "url": "https://cdn.example/ref.mp4", "name": "clip"}}
		body, decodeErr := decode(map[string]any{"model": "sd-2.5-官方版-720p", "prompt": "transition", "seconds": "8", "ratio": "16:9", "watermark": false, "references": refs, "out_task_id": "customer-1"})
		require.NoError(t, decodeErr)
		request := call("buildSubmitRequest", map[string]any{"baseUrl": "https://www.aigasapi.com/v1/", "apiKey": "key", "publicTaskId": "host-1", "upstreamModel": "upstream-model", "requestBody": body})
		assert.Equal(t, "https://www.aigasapi.com/v1/videos", request["url"])
		outbound := request["body"].(map[string]any)
		assert.Equal(t, "upstream-model", outbound["model"])
		assert.Equal(t, float64(8), outbound["duration"])
		assert.Equal(t, false, outbound["watermark"])
		assert.Equal(t, "16:9", outbound["aspect_ratio"])
		assert.Equal(t, "customer-1", outbound["out_task_id"])
		assert.Equal(t, refs, outbound["references"])
		assert.NotContains(t, outbound, "seconds")
		facts := call("extractUsage", map[string]any{"requestBody": body})
		assert.Equal(t, map[string]any{"seconds": float64(8), "videos": float64(1), "video_input": "video"}, facts)
	})
	t.Run("legacy image arrays do not infer first and last frames", func(t *testing.T) {
		body, decodeErr := decode(map[string]any{"model": "sd-2.5-官方版-720p", "prompt": "move", "duration": 8, "images": []any{"https://cdn.example/a.png", "https://cdn.example/b.png"}})
		require.NoError(t, decodeErr)
		for _, ref := range body["references"].([]any) {
			assert.Equal(t, "reference", ref.(map[string]any)["role"])
		}
		request := call("buildSubmitRequest", map[string]any{"baseUrl": "https://www.aigasapi.com", "publicTaskId": "host-1", "requestBody": body})
		assert.Equal(t, "host-1", request["body"].(map[string]any)["out_task_id"])
	})
	t.Run("real host pipeline validates usage and emits AIGAS JSON", func(t *testing.T) {
		body, decodeErr := decode(map[string]any{"model": "sd-2.5-官方版-720p", "prompt": "waves", "duration": 8})
		require.NoError(t, decodeErr)
		adaptor := taskjsplugin.New(plugin)
		info := &relaycommon.RelayInfo{OriginModelName: "sd-2.5-官方版-720p", ChannelMeta: &relaycommon.ChannelMeta{ChannelType: 61, ChannelBaseUrl: "https://www.aigasapi.com", ApiKey: "key", UpstreamModelName: "sd-2.5-官方版-720p"}, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
		adaptor.Init(info)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
		c.Set("task_request", body)
		require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
		facts, callErr := adaptor.ExtractUsageFactsValidated(c, info)
		require.NoError(t, callErr)
		assert.Equal(t, float64(8), facts["seconds"])
		reader, callErr := adaptor.BuildRequestBody(c, info)
		require.NoError(t, callErr)
		encoded, callErr := io.ReadAll(reader)
		require.NoError(t, callErr)
		var outbound map[string]any
		require.NoError(t, common.Unmarshal(encoded, &outbound))
		assert.Equal(t, float64(8), outbound["duration"])
	})
	t.Run("terminal result uses metadata url and never sends channel key to OSS", func(t *testing.T) {
		body := map[string]any{"id": "upstream-1", "status": "completed", "progress": 100, "metadata": map[string]any{"url": "https://oss.example/result.mp4"}}
		result := call("parseTaskResult", map[string]any{}, body)
		assert.Equal(t, "SUCCESS", result["status"])
		assert.Equal(t, "https://oss.example/result.mp4", result["url"])
		submitted := call("parseSubmitResponse", map[string]any{}, map[string]any{"body": body})
		assert.Equal(t, "upstream-1", submitted["taskId"])
		assert.Equal(t, "SUCCESS", submitted["immediate"].(map[string]any)["status"])
		for _, method := range []string{"GET", "HEAD"} {
			request := call("buildContentRequest", map[string]any{"apiKey": "must-not-leak", "artifactKey": "video", "data": body, "clientRequest": map[string]any{"method": method}})
			assert.Equal(t, "https://oss.example/result.mp4", request["url"])
			assert.Equal(t, true, request["credentialless"])
			assert.NotContains(t, request, "headers")
		}
		assert.Equal(t, "UNKNOWN", call("parseTaskResult", map[string]any{}, map[string]any{"status": "completed"})["status"])
		for _, status := range []string{"bogus", "toString", "__proto__", "unknown"} {
			assert.Equal(t, "UNKNOWN", call("parseTaskResult", map[string]any{}, map[string]any{"status": status})["status"])
		}
		failed := call("parseTaskResult", map[string]any{}, map[string]any{"status": "failed", "error": map[string]any{"code": "invalid_request", "message": "unsupported reference"}})
		assert.Equal(t, "FAILURE", failed["status"])
		assert.Equal(t, "invalid_request: unsupported reference", failed["reason"])
		query := call("buildQueryRequest", map[string]any{"baseUrl": "https://www.aigasapi.com", "apiKey": "key", "taskId": "task/a"})
		assert.Equal(t, "https://www.aigasapi.com/v1/videos/task%2Fa", query["url"])
	})
	t.Run("bounds and role failures reject before submission", func(t *testing.T) {
		cases := []struct {
			name  string
			patch map[string]any
			want  string
		}{
			{"negative sentinel", map[string]any{"duration": -1, "videos": []any{"https://cdn.example/ref.mp4"}}, "between 1 and 3600"},
			{"zero", map[string]any{"duration": 0}, "between 1 and 3600"},
			{"overflow", map[string]any{"duration": 3601}, "between 1 and 3600"},
			{"fraction", map[string]any{"duration": 4.5}, "between 1 and 3600"},
			{"boolean duration", map[string]any{"duration": true}, "between 1 and 3600"},
			{"conflicting duration", map[string]any{"seconds": 9}, "must match"},
			{"multiple videos", map[string]any{"n": 2}, "n=1"},
			{"mixed references", map[string]any{"references": []any{}, "images": []any{}}, "cannot be combined"},
			{"last without first", map[string]any{"references": []any{map[string]any{"type": "image", "role": "last_frame", "url": "https://cdn.example/a.png"}}}, "first_frame"},
			{"non-image frame", map[string]any{"references": []any{map[string]any{"type": "video", "role": "first_frame", "url": "https://cdn.example/a.mp4"}}}, "only images"},
			{"invalid url", map[string]any{"videos": []any{"file:///etc/passwd"}}, "HTTP(S)"},
			{"string false", map[string]any{"watermark": "false"}, "boolean"},
			{"invalid orientation", map[string]any{"orientation": "__proto__"}, "orientation"},
			{"mixed frame and ordinary image", map[string]any{"references": []any{map[string]any{"type": "image", "role": "first_frame", "url": "https://cdn.example/a.png"}, map[string]any{"type": "image", "role": "reference", "url": "https://cdn.example/b.png"}}}, "cannot be mixed"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				req := map[string]any{"model": "sd-2.5-官方版-720p", "prompt": "waves", "duration": 8}
				for key, value := range tc.patch {
					req[key] = value
				}
				_, decodeErr := decode(req)
				require.ErrorContains(t, decodeErr, tc.want)
			})
		}
		_, callErr := plugin.Engine.Call(t.Context(), "extractUsage", map[string]any{"requestBody": map[string]any{"model": "sd-2.5-官方版-720p", "prompt": "waves"}})
		require.ErrorContains(t, callErr, "explicitly")
	})
	t.Run("multipart input reference uses host public URL placeholder", func(t *testing.T) {
		value, callErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{"body": map[string]any{"kind": "multipart", "fields": map[string]any{"model": []any{"sd-2.5-官方版-720p"}, "prompt": []any{"waves"}, "seconds": []any{"8"}, "watermark": []any{"false"}}, "files": []any{map[string]any{"field": "input_reference", "ref": "request_file:input_reference"}}}})
		require.NoError(t, callErr)
		body := decodePluginMap(t, value)["requestBody"].(map[string]any)
		request := call("buildSubmitRequest", map[string]any{"baseUrl": "https://www.aigasapi.com", "requestBody": body})
		outbound := request["body"].(map[string]any)
		assert.Equal(t, false, outbound["watermark"])
		ref := outbound["references"].([]any)[0].(map[string]any)
		assert.Equal(t, "first_frame", ref["role"])
		assert.Equal(t, map[string]any{"__fileRef": "request_file:input_reference", "encoding": "publicUrl", "mediaKind": "image"}, ref["url"])
	})
	t.Run("completion usage only accepts bounded real seconds", func(t *testing.T) {
		facts := call("extractUsageOnComplete", map[string]any{}, map[string]any{"status": "SUCCESS"}, map[string]any{"seconds": "9"})
		assert.Equal(t, map[string]any{"seconds": float64(9)}, facts)
		for _, seconds := range []any{-1, 0, 3601, 2.5, true} {
			value, callErr := plugin.Engine.Call(t.Context(), "extractUsageOnComplete", map[string]any{}, map[string]any{"status": "SUCCESS"}, map[string]any{"seconds": seconds})
			require.NoError(t, callErr)
			assert.Nil(t, value)
		}
	})
}

func TestAIGASResponsesProtocol(t *testing.T) {
	testVideoResponsesProtocol(t, videoResponsesTestCase{pluginKey: "aigas", model: "sd-2.5-官方版-720p", requestBody: map[string]any{"model": "sd-2.5-官方版-720p", "input": "waves at sunset", "seconds": 8}, wantAction: "generate", wantRequest: map[string]any{"model": "sd-2.5-官方版-720p", "prompt": "waves at sunset", "duration": float64(8)}, wantUsageKeys: []string{"seconds", "videos", "video_input"}, wantVendorName: "aigas"})
}
