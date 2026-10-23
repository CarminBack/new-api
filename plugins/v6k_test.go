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

func TestV6KVideoContract(t *testing.T) {
	source, err := builtinplugins.Source("v6k")
	require.NoError(t, err)
	registry := jsplugin.NewRegistry()
	plugin, err := registry.RegisterFactory(source, jsplugin.Options{Key: "v6k"})
	require.NoError(t, err)
	call := func(hook string, args ...any) map[string]any {
		t.Helper()
		value, callErr := plugin.Engine.Call(t.Context(), hook, args...)
		require.NoError(t, callErr)
		return decodePluginMap(t, value)
	}
	decode := func(kind string, req any) map[string]any {
		t.Helper()
		body := map[string]any{"kind": kind, "value": req}
		if kind == "multipart" {
			body = req.(map[string]any)
			body["kind"] = kind
		}
		value, callErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{"model": "video-QY", "body": body})
		require.NoError(t, callErr)
		return decodePluginMap(t, value)["requestBody"].(map[string]any)
	}

	t.Run("independent plugin binds only declared models", func(t *testing.T) {
		assert.Empty(t, plugin.Meta.ChannelTypes)
		for _, model := range plugin.Meta.Models {
			binding, found := registry.Generation().LookupEndpoint("POST", "/v1/videos", model)
			require.True(t, found, model)
			assert.Equal(t, "v6k", binding.Plugin.Meta.Key)
		}
	})
	t.Run("mapped model and top level media", func(t *testing.T) {
		body := decode("json", map[string]any{"model": "video-QY", "prompt": "move", "duration": "8", "aspect_ratio": "16:9", "images": []any{map[string]any{"url": "https://cdn.example/image.png"}}, "input_reference": "https://cdn.example/first.png"})
		assert.Equal(t, "video-QY", body["model"])
		request := call("buildSubmitRequest", map[string]any{"baseUrl": "https://www.v6k.top/v1/", "apiKey": "test-key", "upstreamModel": "upstream-id", "requestBody": body})
		assert.Equal(t, "https://www.v6k.top/v1/videos", request["url"])
		outbound := request["body"].(map[string]any)
		assert.Equal(t, "upstream-id", outbound["model"])
		assert.Equal(t, float64(8), outbound["seconds"])
		assert.Equal(t, "16:9", outbound["ratio"])
		assert.NotContains(t, outbound, "size")
		assert.NotContains(t, outbound, "metadata")
		assert.NotContains(t, outbound, "duration")
		assert.Contains(t, outbound, "images")
		assert.Contains(t, outbound, "input_reference")
	})
	t.Run("reference video defaults to smart duration without negative billing", func(t *testing.T) {
		body := decode("json", map[string]any{"prompt": "edit reference", "seconds": 8, "videos": []any{"https://cdn.example/ref.mp4"}})
		assert.NotContains(t, body, "seconds")
		assert.Equal(t, true, body["_v6kSmartDuration"])
		facts := call("extractUsage", map[string]any{"model": "video-QY", "requestBody": body})
		assert.Equal(t, map[string]any{"video_count": float64(1), "video_input": "video"}, facts)
		request := call("buildSubmitRequest", map[string]any{"baseUrl": "https://www.v6k.top", "requestBody": body})
		assert.Equal(t, float64(-1), request["body"].(map[string]any)["seconds"])
	})
	t.Run("smart duration passes real host validation without weakening numeric guards", func(t *testing.T) {
		for _, model := range []string{"video-QY", "TTseadanco2.5-720"} {
			body := decode("json", map[string]any{"prompt": "edit", "seconds": -1, "videos": []any{map[string]any{"url": "https://cdn.example/ref.mp4", "duration_seconds": 6.5}}})
			body["model"] = model
			adaptor := taskjsplugin.New(plugin)
			info := &relaycommon.RelayInfo{
				OriginModelName: model,
				ChannelMeta:     &relaycommon.ChannelMeta{ChannelBaseUrl: "https://www.v6k.top", ChannelType: 61, ApiKey: "key", UpstreamModelName: model},
				TaskRelayInfo:   &relaycommon.TaskRelayInfo{},
			}
			adaptor.Init(info)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
			c.Set("task_request", body)
			require.Nil(t, adaptor.ValidateRequestAndSetAction(c, info))
			facts, callErr := adaptor.ExtractUsageFactsValidated(c, info)
			require.NoError(t, callErr)
			if model == "video-QY" {
				assert.Equal(t, float64(1), facts["video_count"])
			} else {
				assert.Equal(t, 6.5, facts["video_seconds"])
			}
			reader, callErr := adaptor.BuildRequestBody(c, info)
			require.NoError(t, callErr)
			encoded, callErr := io.ReadAll(reader)
			require.NoError(t, callErr)
			var outbound map[string]any
			require.NoError(t, common.Unmarshal(encoded, &outbound))
			assert.Equal(t, float64(-1), outbound["seconds"])
			assert.NotContains(t, outbound, "_v6kSmartDuration")
		}
	})
	t.Run("per second smart duration requires reference duration", func(t *testing.T) {
		req := map[string]any{"model": "TTseadanco2.5-720", "prompt": "edit", "seconds": -1, "videos": []any{map[string]any{"url": "https://cdn.example/ref.mp4", "duration_seconds": 6.5}}}
		facts := call("extractUsage", map[string]any{"model": "TTseadanco2.5-720", "requestBody": req})
		assert.Equal(t, map[string]any{"video_seconds": 6.5, "video_input": "video"}, facts)
		req["videos"] = []any{"https://cdn.example/ref.mp4"}
		_, callErr := plugin.Engine.Call(t.Context(), "extractUsage", map[string]any{"model": "TTseadanco2.5-720", "requestBody": req})
		require.ErrorContains(t, callErr, "duration_seconds")
		delete(req, "videos")
		req["seconds"] = 9
		facts = call("extractUsage", map[string]any{"model": "TTseadanco2.5-720", "requestBody": req})
		assert.Equal(t, float64(9), facts["video_seconds"])
		// The final known upstream model's billing profile takes precedence over the alias.
		facts = call("extractUsage", map[string]any{"model": "TTseadanco2.5-720", "upstreamModel": "video-QY", "requestBody": req})
		assert.Equal(t, float64(1), facts["video_count"])
		assert.NotContains(t, facts, "video_seconds")
	})
	t.Run("count model does not invent default duration", func(t *testing.T) {
		body := decode("json", map[string]any{"prompt": "waves"})
		assert.NotContains(t, body, "seconds")
		facts := call("extractUsage", map[string]any{"requestBody": body})
		assert.Equal(t, float64(1), facts["video_count"])
	})
	t.Run("multipart streams references and preserves intelligent duration", func(t *testing.T) {
		files := []any{map[string]any{"field": "video", "ref": "request_file:video", "filename": "ref.mp4"}}
		body := decode("multipart", map[string]any{"fields": map[string]any{"model": []any{"video-QY"}, "prompt": []any{"edit"}, "seconds": []any{"-1"}}, "files": files})
		request := call("buildSubmitRequest", map[string]any{"baseUrl": "https://www.v6k.top", "apiKey": "key", "requestBody": body, "files": files})
		assert.Equal(t, "multipart", request["bodyType"])
		assert.NotContains(t, request["headers"].(map[string]any), "Content-Type")
		assert.Contains(t, request["parts"], map[string]any{"name": "seconds", "value": "-1"})
		assert.Contains(t, request["parts"], map[string]any{"name": "video", "fileRef": "request_file:video", "filename": "ref.mp4"})
		facts := call("extractUsage", map[string]any{"model": "video-QY", "requestBody": body, "files": files})
		assert.Equal(t, "video", facts["video_input"])
	})
	t.Run("array form fields", func(t *testing.T) {
		body := decode("multipart", map[string]any{"fields": map[string]any{"prompt": []any{"waves"}, "images[]": []any{"https://cdn.example/a.png", "https://cdn.example/b.png"}, "seconds": []any{"8"}}})
		assert.Len(t, body["images"], 2)
		assert.Equal(t, float64(8), body["seconds"])
	})
	t.Run("reject invalid quantities and ambiguous inputs before submit", func(t *testing.T) {
		cases := []struct {
			name   string
			fields map[string]any
			want   string
		}{
			{"zero", map[string]any{"seconds": 0}, "seconds"},
			{"negative", map[string]any{"seconds": -2}, "seconds"},
			{"too large", map[string]any{"seconds": 3601}, "3600"},
			{"fraction", map[string]any{"seconds": 1.5}, "seconds"},
			{"boolean", map[string]any{"seconds": true}, "seconds"},
			{"smart without video", map[string]any{"seconds": -1}, "reference video"},
			{"conflicting duration aliases", map[string]any{"seconds": 8, "duration": 9}, "agree"},
			{"metadata duration", map[string]any{"metadata": map[string]any{"duration": -1}}, "metadata"},
			{"metadata count", map[string]any{"metadata": map[string]any{"n": 2}}, "metadata"},
			{"multiple outputs", map[string]any{"n": 2}, "n=1"},
			{"boolean output count", map[string]any{"n": true}, "n=1"},
			{"ratio used as size", map[string]any{"size": "16:9"}, "pixel"},
			{"conflicting size and ratio", map[string]any{"size": "1280x720", "ratio": "9:16"}, "must match"},
			{"bad reference", map[string]any{"videos": []any{"file:///secret"}}, "HTTP(S)"},
			{"unbounded reference", map[string]any{"videos": []any{map[string]any{"url": "https://cdn.example/v.mp4", "duration_seconds": 3601}}}, "3600"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				req := map[string]any{"model": "video-QY", "prompt": "test"}
				for k, v := range tc.fields {
					req[k] = v
				}
				_, callErr := plugin.Engine.CallPath(t.Context(), "protocols", []string{"openai_video", "decodeRequest"}, map[string]any{"body": map[string]any{"kind": "json", "value": req}})
				require.ErrorContains(t, callErr, tc.want)
			})
		}
	})
	t.Run("poll status and preserve actionable upstream error", func(t *testing.T) {
		cases := map[string]string{"queued": "QUEUED", "in_progress": "IN_PROGRESS", "completed": "SUCCESS", "failed": "FAILURE", "unknown": "UNKNOWN", "bogus": "UNKNOWN", "": "UNKNOWN"}
		for upstream, want := range cases {
			result := call("parseTaskResult", map[string]any{}, map[string]any{"status": upstream, "progress": 42, "error": map[string]any{"code": "upstream_failure", "message": "请把 seconds 设为 -1（智能时长）"}})
			assert.Equal(t, want, result["status"])
			if want == "FAILURE" {
				assert.Equal(t, "upstream_failure: 请把 seconds 设为 -1（智能时长）", result["reason"])
			}
		}
		result := call("parseSubmitResponse", map[string]any{}, map[string]any{"body": map[string]any{"id": "000012345678901234567890"}})
		assert.Equal(t, "000012345678901234567890", result["taskId"])
		_, callErr := plugin.Engine.Call(t.Context(), "parseSubmitResponse", map[string]any{}, map[string]any{"body": map[string]any{"id": 123}})
		require.ErrorContains(t, callErr, "task id")
	})
	t.Run("authenticated content GET HEAD and range without credential leakage", func(t *testing.T) {
		query := call("buildQueryRequest", map[string]any{"baseUrl": "https://www.v6k.top/v1/", "taskId": "task/a", "apiKey": "key"})
		assert.Equal(t, "https://www.v6k.top/v1/videos/task%2Fa", query["url"])
		for _, method := range []string{"GET", "HEAD"} {
			request := call("buildContentRequest", map[string]any{"baseUrl": "https://www.v6k.top/", "apiKey": "channel-key", "upstreamTaskId": "task/a", "artifactKey": "video", "data": map[string]any{"url": "https://evil.example/result.mp4"}, "clientRequest": map[string]any{"method": method, "headers": map[string]any{"Range": "bytes=0-1023", "If-Range": "etag", "Authorization": "Bearer client-secret"}}})
			assert.Equal(t, "https://www.v6k.top/v1/videos/task%2Fa/content", request["url"])
			assert.Equal(t, method, request["method"])
			assert.Equal(t, map[string]any{"Authorization": "Bearer channel-key", "Range": "bytes=0-1023", "If-Range": "etag"}, request["headers"])
			assert.NotContains(t, request, "credentialless")
		}
		artifacts, callErr := plugin.Engine.Call(t.Context(), "listArtifacts", map[string]any{"status": "SUCCESS", "data": map[string]any{"status": "completed"}})
		require.NoError(t, callErr)
		require.NotEmpty(t, artifacts)
	})
}

func TestV6KResponsesProtocol(t *testing.T) {
	testVideoResponsesProtocol(t, videoResponsesTestCase{
		pluginKey: "v6k", model: "video-QY", requestBody: map[string]any{"model": "video-QY", "input": "waves at sunset", "seconds": 8, "size": "1280x720"},
		wantAction: "generate", wantRequest: map[string]any{"model": "video-QY", "prompt": "waves at sunset", "seconds": float64(8), "size": "1280x720"},
		wantUsageKeys: []string{"video_count", "video_input"}, wantVendorName: "v6k",
	})
}
