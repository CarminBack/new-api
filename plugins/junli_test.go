package plugins_test

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJunliVideoContract(t *testing.T) {
	source, err := builtinplugins.Source("junli")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "junli"})
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
	submit := func(body map[string]any) map[string]any {
		t.Helper()
		request := call("buildSubmitRequest", map[string]any{"baseUrl": "https://y.junlistudio.com/", "apiKey": "key", "upstreamModel": "seedance-2.5", "requestBody": body})
		assert.Equal(t, "https://y.junlistudio.com/v1/videos", request["url"])
		assert.Equal(t, "Bearer key", request["headers"].(map[string]any)["Authorization"])
		return request["body"].(map[string]any)
	}
	base := func() map[string]any {
		return map[string]any{"model": "seedance-2.5", "prompt": "waves", "seconds": 8, "aspect_ratio": "16:9", "resolution": "720p"}
	}
	ref := func(kind, role, url string) map[string]any {
		return map[string]any{"type": kind, "role": role, "url": url}
	}

	t.Run("text request forces url response format", func(t *testing.T) {
		body, decodeErr := decode(base())
		require.NoError(t, decodeErr)
		outbound := submit(body)
		assert.Equal(t, "seedance-2.5", outbound["model"])
		assert.Equal(t, float64(8), outbound["seconds"])
		assert.Equal(t, "url", outbound["response_format"])
		assert.Equal(t, "16:9", outbound["aspect_ratio"])
		assert.Equal(t, "720p", outbound["resolution"])
		assert.NotContains(t, outbound, "reference_mode")
		assert.NotContains(t, outbound, "references")
		facts := call("extractUsage", map[string]any{"requestBody": body})
		assert.Equal(t, map[string]any{"seconds": float64(8), "videos": float64(1), "video_input": "none"}, facts)
	})
	t.Run("ordinary references use asset mode", func(t *testing.T) {
		req := base()
		req["references"] = []any{ref("image", "reference", "https://cdn.example/a.png"), ref("image", "reference", "https://cdn.example/b.png"), ref("video", "reference", "https://cdn.example/c.mp4")}
		body, decodeErr := decode(req)
		require.NoError(t, decodeErr)
		outbound := submit(body)
		assert.Equal(t, "asset", outbound["reference_mode"])
		assert.Equal(t, []any{"https://cdn.example/a.png", "https://cdn.example/b.png"}, outbound["reference_images"])
		assert.Equal(t, []any{"https://cdn.example/c.mp4"}, outbound["reference_videos"])
		assert.NotContains(t, outbound, "references")
		assert.Equal(t, "video", call("extractUsage", map[string]any{"requestBody": body})["video_input"])
	})
	t.Run("frame roles use frame mode regardless of order", func(t *testing.T) {
		req := base()
		req["references"] = []any{ref("image", "last_frame", "https://cdn.example/last.png"), ref("image", "first_frame", "https://cdn.example/first.png")}
		body, decodeErr := decode(req)
		require.NoError(t, decodeErr)
		outbound := submit(body)
		assert.Equal(t, "frame", outbound["reference_mode"])
		assert.Equal(t, "https://cdn.example/first.png", outbound["start_frame"])
		assert.Equal(t, "https://cdn.example/last.png", outbound["end_frame"])
		assert.NotContains(t, outbound, "reference_images")
		req["references"] = []any{ref("image", "first_frame", "https://cdn.example/first.png")}
		body, decodeErr = decode(req)
		require.NoError(t, decodeErr)
		outbound = submit(body)
		assert.Equal(t, "https://cdn.example/first.png", outbound["start_frame"])
		assert.NotContains(t, outbound, "end_frame")
	})
	t.Run("legacy arrays map to ordinary references", func(t *testing.T) {
		req := base()
		req["images"] = []any{"https://cdn.example/a.png"}
		body, decodeErr := decode(req)
		require.NoError(t, decodeErr)
		assert.Equal(t, []any{"https://cdn.example/a.png"}, submit(body)["reference_images"])
	})
	t.Run("invalid or unsupported input is rejected before submission", func(t *testing.T) {
		cases := []struct {
			name  string
			patch map[string]any
			want  string
		}{
			{"audio reference", map[string]any{"references": []any{ref("audio", "reference", "https://cdn.example/a.mp3")}}, "audio"},
			{"legacy audios", map[string]any{"audios": []any{"https://cdn.example/a.mp3"}}, "audio"},
			{"last without first", map[string]any{"references": []any{ref("image", "last_frame", "https://cdn.example/a.png")}}, "first_frame"},
			{"frame mixed with ordinary", map[string]any{"references": []any{ref("image", "first_frame", "https://cdn.example/a.png"), ref("image", "reference", "https://cdn.example/b.png")}}, "cannot be mixed"},
			{"video frame", map[string]any{"references": []any{ref("video", "first_frame", "https://cdn.example/a.mp4")}}, "only images"},
			{"invalid url", map[string]any{"references": []any{ref("image", "reference", "file:///etc/passwd")}}, "HTTP(S)"},
			{"negative sentinel", map[string]any{"seconds": -1}, "between 1 and 3600"},
			{"overflow", map[string]any{"seconds": 3601}, "between 1 and 3600"},
			{"conflicting duration", map[string]any{"duration": 9}, "must match"},
			{"bad resolution", map[string]any{"resolution": "hd"}, "resolution"},
			{"multiple outputs", map[string]any{"n": 2}, "n=1"},
			{"mixed legacy and structured", map[string]any{"references": []any{}, "images": []any{}}, "cannot be combined"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				req := base()
				for key, value := range tc.patch {
					req[key] = value
				}
				_, decodeErr := decode(req)
				require.ErrorContains(t, decodeErr, tc.want)
			})
		}
		_, callErr := plugin.Engine.Call(t.Context(), "extractUsage", map[string]any{"requestBody": map[string]any{"model": "seedance-2.5", "prompt": "waves"}})
		require.ErrorContains(t, callErr, "explicitly")
	})
	t.Run("status mapping and failure reasons", func(t *testing.T) {
		for status, want := range map[string]string{"queued": "QUEUED", "in_progress": "IN_PROGRESS", "completed": "SUCCESS", "failed": "FAILURE", "bogus": "UNKNOWN", "toString": "UNKNOWN", "__proto__": "UNKNOWN"} {
			assert.Equal(t, want, call("parseTaskResult", map[string]any{}, map[string]any{"status": status})["status"], status)
		}
		failed := call("parseTaskResult", map[string]any{}, map[string]any{"status": "failed", "error": map[string]any{"code": "invalid_request", "message": "bad media"}})
		assert.Equal(t, "invalid_request: bad media", failed["reason"])
		assert.Equal(t, "upstream said no", call("parseTaskResult", map[string]any{}, map[string]any{"status": "failed", "fail_reason": "upstream said no"})["reason"])
		assert.Equal(t, "video generation failed", call("parseTaskResult", map[string]any{}, map[string]any{"status": "failed"})["reason"])
		done := call("parseTaskResult", map[string]any{}, map[string]any{"status": "completed", "progress": 100})
		assert.Equal(t, "100%", done["progress"])
		assert.NotContains(t, done, "url")
	})
	t.Run("submit and query use the OpenAI video lifecycle", func(t *testing.T) {
		submitted := call("parseSubmitResponse", map[string]any{}, map[string]any{"body": map[string]any{"id": "video_1", "status": "queued"}})
		assert.Equal(t, "video_1", submitted["taskId"])
		assert.NotContains(t, submitted, "immediate")
		immediate := call("parseSubmitResponse", map[string]any{}, map[string]any{"body": map[string]any{"id": "video_2", "status": "failed", "error": map[string]any{"message": "nope"}}})
		assert.Equal(t, "FAILURE", immediate["immediate"].(map[string]any)["status"])
		_, callErr := plugin.Engine.Call(t.Context(), "parseSubmitResponse", map[string]any{}, map[string]any{"body": map[string]any{"detail": "model not allowed"}})
		require.ErrorContains(t, callErr, "model not allowed")
		query := call("buildQueryRequest", map[string]any{"baseUrl": "https://y.junlistudio.com/v1", "apiKey": "key", "taskId": "task/a"})
		assert.Equal(t, "https://y.junlistudio.com/v1/videos/task%2Fa", query["url"])
		assert.Equal(t, "GET", query["method"])
	})
	t.Run("content download goes through the original task and forwards only Range", func(t *testing.T) {
		ctx := map[string]any{
			"baseUrl": "https://y.junlistudio.com", "apiKey": "key", "artifactKey": "video", "upstreamTaskId": "task/a",
			"clientRequest": map[string]any{"method": "GET", "headers": map[string]any{"Range": "bytes=0-99", "Cookie": "secret", "Authorization": "Bearer client"}},
		}
		request := call("buildContentRequest", ctx)
		assert.Equal(t, "https://y.junlistudio.com/v1/videos/task%2Fa/content", request["url"])
		headers := request["headers"].(map[string]any)
		assert.Equal(t, "Bearer key", headers["Authorization"])
		assert.Equal(t, "bytes=0-99", headers["Range"])
		assert.NotContains(t, headers, "Cookie")
		assert.NotContains(t, headers, "Accept")
		ctx["artifactKey"] = "thumbnail"
		_, callErr := plugin.Engine.Call(t.Context(), "buildContentRequest", ctx)
		require.ErrorContains(t, callErr, "artifact_not_found")
		artifacts, callErr := plugin.Engine.Call(t.Context(), "listArtifacts", map[string]any{"status": "SUCCESS"})
		require.NoError(t, callErr)
		assert.Len(t, artifacts, 1)
		artifacts, callErr = plugin.Engine.Call(t.Context(), "listArtifacts", map[string]any{"status": "IN_PROGRESS"})
		require.NoError(t, callErr)
		assert.Empty(t, artifacts)
	})
	t.Run("completion usage trusts only bounded integer seconds", func(t *testing.T) {
		success := map[string]any{"status": "SUCCESS"}
		assert.Equal(t, map[string]any{"seconds": float64(8)}, call("extractUsageOnComplete", map[string]any{}, success, map[string]any{"seconds": "8"}))
		for _, seconds := range []any{-1, 0, 3601, 4.5, true, "x"} {
			value, callErr := plugin.Engine.Call(t.Context(), "extractUsageOnComplete", map[string]any{}, success, map[string]any{"seconds": seconds})
			require.NoError(t, callErr)
			assert.Nil(t, value, "%v", seconds)
		}
	})
	t.Run("capabilities are per model and never inferred", func(t *testing.T) {
		caps := plugin.Meta.VideoCapabilities
		require.Len(t, caps, 10)
		for _, model := range plugin.Meta.Models {
			assert.Contains(t, caps, model)
		}
		minimax := caps["minimax-h3-optimized"]
		assert.Nil(t, minimax.Resolutions, "empty upstream resolution list is unknown, not declared")
		require.NotNil(t, minimax.MaxVideos)
		assert.Equal(t, 3, *minimax.MaxVideos)
		wan := caps["wan3.0-720p-mx10.5.0-gfapi"]
		assert.Equal(t, []int{4, 5, 6, 8, 10, 12, 15, 30}, wan.SecondsSteps)
		require.NotNil(t, wan.MaxVideos)
		assert.Equal(t, 5, *wan.MaxVideos)
		assert.Nil(t, caps["seedance-2.5"].SecondsSteps)
	})
}
