package plugins_test

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestYeepayVideoContract(t *testing.T) {
	source, err := builtinplugins.Source("yeepay")
	require.NoError(t, err)
	plugin, err := jsplugin.NewRegistry().RegisterFactory(source, jsplugin.Options{Key: "yeepay"})
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
		request := call("buildSubmitRequest", map[string]any{"baseUrl": "https://www.yeepay.top/", "apiKey": "key", "publicTaskId": "task_public_1", "requestBody": body})
		assert.Equal(t, "https://www.yeepay.top/v1/videos", request["url"])
		headers := request["headers"].(map[string]any)
		assert.Equal(t, "Bearer key", headers["Authorization"])
		assert.Equal(t, "task_public_1", headers["Idempotency-Key"])
		return request["body"].(map[string]any)
	}
	base := func(model string) map[string]any {
		return map[string]any{"model": model, "prompt": "waves", "seconds": 8, "aspect_ratio": "16:9", "resolution": "720p"}
	}
	ref := func(kind, role, url string) map[string]any {
		return map[string]any{"type": kind, "role": role, "url": url}
	}

	t.Run("text request uses the platform field names", func(t *testing.T) {
		body, decodeErr := decode(base("Xseadanco2.0"))
		require.NoError(t, decodeErr)
		outbound := submit(body)
		assert.Equal(t, "Xseadanco2.0", outbound["model"])
		assert.Equal(t, float64(8), outbound["duration"])
		assert.Equal(t, "16:9", outbound["ratio"])
		assert.Equal(t, "720p", outbound["resolution"])
		for _, key := range []string{"seconds", "aspect_ratio", "size", "images", "start_frame"} {
			assert.NotContains(t, outbound, key)
		}
	})
	t.Run("references map to platform arrays and frames", func(t *testing.T) {
		req := base("Xseadanco2.0")
		req["references"] = []any{ref("image", "reference", "https://cdn.example/a.png"), ref("video", "reference", "https://cdn.example/b.mp4"), ref("audio", "reference", "https://cdn.example/c.mp3")}
		body, decodeErr := decode(req)
		require.NoError(t, decodeErr)
		outbound := submit(body)
		assert.Equal(t, []any{"https://cdn.example/a.png"}, outbound["images"])
		assert.Equal(t, []any{"https://cdn.example/b.mp4"}, outbound["reference_videos"])
		assert.Equal(t, []any{"https://cdn.example/c.mp3"}, outbound["reference_audios"])
		req["references"] = []any{ref("image", "last_frame", "https://cdn.example/last.png"), ref("image", "first_frame", "https://cdn.example/first.png")}
		body, decodeErr = decode(req)
		require.NoError(t, decodeErr)
		outbound = submit(body)
		assert.Equal(t, "https://cdn.example/first.png", outbound["start_frame"])
		assert.Equal(t, "https://cdn.example/last.png", outbound["end_frame"])
		assert.NotContains(t, outbound, "images")
	})
	t.Run("size becomes ratio and resolution and is never sent", func(t *testing.T) {
		req := map[string]any{"model": "Xseadanco2.0", "prompt": "waves", "seconds": 8, "size": "1280x720"}
		body, decodeErr := decode(req)
		require.NoError(t, decodeErr)
		outbound := submit(body)
		assert.Equal(t, "16:9", outbound["ratio"])
		assert.Equal(t, "720p", outbound["resolution"])
		assert.NotContains(t, outbound, "size")
		req["size"] = "720x1280"
		body, decodeErr = decode(req)
		require.NoError(t, decodeErr)
		outbound = submit(body)
		assert.Equal(t, "9:16", outbound["ratio"])
		assert.Equal(t, "720p", outbound["resolution"], "the short side names the tier")
		req["resolution"] = "720P"
		req["aspect_ratio"] = "9:16"
		_, decodeErr = decode(req)
		require.NoError(t, decodeErr, "a matching explicit ratio and resolution is not a conflict")
	})
	t.Run("optional fields are forwarded only when supplied", func(t *testing.T) {
		req := base("Xseadanco2.0")
		req["generate_audio"] = false
		req["negative_prompt"] = "blur"
		body, decodeErr := decode(req)
		require.NoError(t, decodeErr)
		outbound := submit(body)
		assert.Equal(t, false, outbound["generate_audio"], "explicit false must survive")
		assert.Equal(t, "blur", outbound["negative_prompt"])
		assert.NotContains(t, outbound, "quality")
	})
	t.Run("invalid or unsupported input is rejected before submission", func(t *testing.T) {
		cases := []struct {
			name  string
			patch map[string]any
			want  string
		}{
			{"malformed size", map[string]any{"size": "big"}, "size must look like"},
			{"size conflicts with ratio", map[string]any{"size": "720x1280"}, "conflicts with aspect_ratio"},
			{"size conflicts with resolution", map[string]any{"size": "1920x1080"}, "conflicts with resolution"},
			{"plain http media", map[string]any{"references": []any{ref("image", "reference", "http://cdn.example/a.png")}}, "HTTPS"},
			{"file url", map[string]any{"references": []any{ref("image", "reference", "file:///etc/passwd")}}, "HTTPS"},
			{"last without first", map[string]any{"references": []any{ref("image", "last_frame", "https://cdn.example/a.png")}}, "first_frame"},
			{"frame mixed with ordinary", map[string]any{"references": []any{ref("image", "first_frame", "https://cdn.example/a.png"), ref("image", "reference", "https://cdn.example/b.png")}}, "cannot be mixed"},
			{"audio frame", map[string]any{"references": []any{ref("audio", "first_frame", "https://cdn.example/a.mp3")}}, "only images"},
			{"negative sentinel", map[string]any{"seconds": -1}, "between 1 and 3600"},
			{"overflow", map[string]any{"seconds": 3601}, "between 1 and 3600"},
			{"conflicting duration", map[string]any{"duration": 9}, "must match"},
			{"bad resolution", map[string]any{"resolution": "hd"}, "resolution"},
			{"string audio flag", map[string]any{"generate_audio": "false"}, "boolean"},
			{"multiple outputs", map[string]any{"n": 2}, "n=1"},
			{"mixed legacy and structured", map[string]any{"references": []any{}, "images": []any{}}, "cannot be combined"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				req := base("Xseadanco2.0")
				for key, value := range tc.patch {
					req[key] = value
				}
				_, decodeErr := decode(req)
				require.ErrorContains(t, decodeErr, tc.want)
			})
		}
	})
	t.Run("billing follows each model's catalog unit", func(t *testing.T) {
		usage := func(req map[string]any) (map[string]any, error) {
			body, decodeErr := decode(req)
			require.NoError(t, decodeErr)
			value, callErr := plugin.Engine.Call(t.Context(), "extractUsage", map[string]any{"requestBody": body})
			if callErr != nil {
				return nil, callErr
			}
			return decodePluginMap(t, value), nil
		}
		facts, usageErr := usage(base("Xseadanco2.0"))
		require.NoError(t, usageErr)
		assert.Equal(t, map[string]any{"videos": float64(1)}, facts, "per-request models ignore seconds")
		facts, usageErr = usage(base("TTsedanco2.0-720"))
		require.NoError(t, usageErr)
		assert.Equal(t, map[string]any{"seconds": float64(8), "video_input": "none"}, facts)
		req := base("TTsedanco2.0-720")
		delete(req, "seconds")
		_, usageErr = usage(req)
		require.ErrorContains(t, usageErr, "explicitly")
		req = base("XXseedacn2.5")
		req["references"] = []any{ref("video", "reference", "https://cdn.example/a.mp4")}
		_, usageErr = usage(req)
		require.ErrorContains(t, usageErr, "duration_seconds")
		withDuration := ref("video", "reference", "https://cdn.example/a.mp4")
		withDuration["duration_seconds"] = 6.5
		other := ref("video", "reference", "https://cdn.example/b.mp4")
		other["duration_seconds"] = 3.5
		req["references"] = []any{withDuration, other}
		facts, usageErr = usage(req)
		require.NoError(t, usageErr)
		assert.Equal(t, map[string]any{"seconds": float64(8), "video_input": "video", "reference_seconds": float64(10)}, facts)
		req = base("XXseedacn2.5")
		facts, usageErr = usage(req)
		require.NoError(t, usageErr)
		assert.Equal(t, float64(0), facts["reference_seconds"])
	})
	t.Run("submit and query use the platform task lifecycle", func(t *testing.T) {
		submitted := call("parseSubmitResponse", map[string]any{}, map[string]any{"body": map[string]any{"code": 0, "success": true, "task_id": "vid_1", "status": "processing", "next_poll_seconds": 10}})
		assert.Equal(t, "vid_1", submitted["taskId"])
		assert.NotContains(t, submitted, "immediate")
		for _, body := range []map[string]any{
			{"error": map[string]any{"type": "invalid_prompt", "message": "blocked"}},
			{"code": 402, "success": false, "task_id": "vid_x", "message": "insufficient credits"},
			{},
		} {
			_, callErr := plugin.Engine.Call(t.Context(), "parseSubmitResponse", map[string]any{}, map[string]any{"body": body})
			require.Error(t, callErr, "%v", body)
		}
		_, callErr := plugin.Engine.Call(t.Context(), "parseSubmitResponse", map[string]any{}, map[string]any{"body": map[string]any{"error": map[string]any{"type": "invalid_prompt", "message": "blocked"}}})
		require.ErrorContains(t, callErr, "invalid_prompt: blocked")
		query := call("buildQueryRequest", map[string]any{"baseUrl": "https://www.yeepay.top/v1", "apiKey": "key", "taskId": "vid/a"})
		assert.Equal(t, "https://www.yeepay.top/v1/tasks/vid%2Fa", query["url"], "a base that already ends in /v1 must not double it")
		assert.Equal(t, "GET", query["method"])
	})
	t.Run("status mapping, failures and missing deliverables", func(t *testing.T) {
		video := map[string]any{"video_url": "https://www.yeepay.top/api/video-content/vid_1"}
		for status, want := range map[string]string{"processing": "IN_PROGRESS", "failed": "FAILURE", "bogus": "UNKNOWN", "expired": "UNKNOWN", "toString": "UNKNOWN", "__proto__": "UNKNOWN"} {
			assert.Equal(t, want, call("parseTaskResult", map[string]any{}, map[string]any{"status": status})["status"], status)
		}
		done := call("parseTaskResult", map[string]any{}, map[string]any{"status": "succeeded", "video_url": video["video_url"]})
		assert.Equal(t, "SUCCESS", done["status"])
		assert.Equal(t, "100%", done["progress"])
		empty := call("parseTaskResult", map[string]any{}, map[string]any{"status": "succeeded"})
		assert.Equal(t, "UNKNOWN", empty["status"], "a success without a video address must stay unresolved")
		insecure := call("parseTaskResult", map[string]any{}, map[string]any{"status": "succeeded", "video_url": "http://example.com/v.mp4"})
		assert.Equal(t, "UNKNOWN", insecure["status"])
		failed := call("parseTaskResult", map[string]any{}, map[string]any{"status": "failed", "error_code": "content_blocked", "error_detail": "prompt rejected"})
		assert.Equal(t, "content_blocked: prompt rejected", failed["reason"])
		assert.Equal(t, "video generation failed", call("parseTaskResult", map[string]any{}, map[string]any{"status": "failed"})["reason"])
	})
	t.Run("download is credentialless and addressed by the upstream task id", func(t *testing.T) {
		for _, method := range []string{"GET", "HEAD"} {
			request := call("buildContentRequest", map[string]any{"baseUrl": "https://www.yeepay.top/v1", "apiKey": "must-not-leak", "artifactKey": "video", "upstreamTaskId": "vid/a", "clientRequest": map[string]any{"method": method, "headers": map[string]any{"Range": "bytes=0-99"}}})
			assert.Equal(t, "https://www.yeepay.top/api/video-content/vid%2Fa", request["url"])
			assert.Equal(t, method, request["method"])
			assert.Equal(t, true, request["credentialless"])
			assert.NotContains(t, request, "headers", "credentialless requests cannot carry headers; the host forwards Range itself")
		}
		_, callErr := plugin.Engine.Call(t.Context(), "buildContentRequest", map[string]any{"baseUrl": "https://www.yeepay.top", "artifactKey": "thumbnail", "upstreamTaskId": "vid_1", "clientRequest": map[string]any{"method": "GET"}})
		require.ErrorContains(t, callErr, "artifact_not_found")
		artifacts, callErr := plugin.Engine.Call(t.Context(), "listArtifacts", map[string]any{"status": "SUCCESS", "data": map[string]any{"video_url": "https://www.yeepay.top/api/video-content/vid_1"}})
		require.NoError(t, callErr)
		assert.Len(t, artifacts, 1)
		artifacts, callErr = plugin.Engine.Call(t.Context(), "listArtifacts", map[string]any{"status": "SUCCESS", "data": map[string]any{}})
		require.NoError(t, callErr)
		assert.Empty(t, artifacts)
		artifacts, callErr = plugin.Engine.Call(t.Context(), "listArtifacts", map[string]any{"status": "IN_PROGRESS"})
		require.NoError(t, callErr)
		assert.Empty(t, artifacts)
	})
	t.Run("capabilities are per model and never inferred", func(t *testing.T) {
		caps := plugin.Meta.VideoCapabilities
		require.Len(t, caps, 21)
		for _, model := range plugin.Meta.Models {
			assert.Contains(t, caps, model)
		}
		assert.Nil(t, caps["Xminimex-h3"].Resolutions, "the catalog's 2k tier is not a plain pixel tier, so it stays undeclared")
		assert.Equal(t, []int{5, 10, 15}, caps["A-SD2.0"].SecondsSteps)
		assert.Nil(t, caps["Xseadanco2.0"].SecondsSteps, "a contiguous range needs no step list")
		require.NotNil(t, caps["TTP-grok"].MaxVideos)
		assert.Equal(t, 0, *caps["TTP-grok"].MaxVideos, "an explicit zero is a declared limit, not an unknown")
		assert.Equal(t, []string{"1080p", "720p"}, caps["wan3/1080"].Resolutions)
		require.NotNil(t, caps["XXseedacn2.5"].ReferenceDurationRequired)
		assert.True(t, *caps["XXseedacn2.5"].ReferenceDurationRequired)
		assert.Nil(t, caps["XXseadanco2.0M"].ReferenceDurationRequired)
	})
}
