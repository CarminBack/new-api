package aistarslab

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIsAistarsLabBaseURL(t *testing.T) {
	for _, baseURL := range []string{
		"https://api.video.aistarslab.com/openai",
		"https://api.video.aistarslab.com/openai/v1",
		"https://api.video.aistarslab.com:443/openai/",
	} {
		require.True(t, IsAistarsLabBaseURL(baseURL), baseURL)
	}
	for _, baseURL := range []string{
		"https://api.video.aistarslab.com/v1",
		"https://example.com/openai",
		"https://example.com/api.video.aistarslab.com/openai",
		"not-a-url",
	} {
		require.False(t, IsAistarsLabBaseURL(baseURL), baseURL)
	}
}

func TestCapabilityMatrixMatchesCurrentSeedanceLines(t *testing.T) {
	channel50 := capabilityForModel("seedance-720p-fast-c50")
	require.True(t, channel50.known)
	require.Equal(t, 5, channel50.minSeconds)
	require.Equal(t, 4, channel50.maxImages)
	require.False(t, channel50.modes["frames2video"])
	require.Equal(t, 1, channel50.maxAudios)

	channel48 := capabilityForModel("seedance-720p-c48")
	require.True(t, channel48.modes["frames2video"])
	require.True(t, channel48.rations["21:9"])
	require.Equal(t, 9, channel48.maxImages)
	require.Equal(t, "720p", channel48.resolution)

	channel49 := capabilityForModel("seedance-720p-c49")
	require.True(t, channel49.modes["frames2video"])
	require.False(t, channel49.rations["21:9"])
	require.True(t, channel49.perItem)
}

func TestValidateAndBuildAistarsLabFramesRequest(t *testing.T) {
	c := testJSONContext(`{
    "model": "seedance-720p-fast-c48",
    "prompt": "从白天过渡到夜晚",
    "seconds": "4",
    "size": "21:9",
    "n": 1,
    "metadata": {
      "resolution": "720p",
      "mode_type": "frames2video",
      "images": ["https://example.com/start.jpg", "https://example.com/end.jpg"],
      "videos": ["https://example.com/reference.mp4"],
      "audios": ["https://example.com/reference.mp3"]
    }
  }`)
	info := &relaycommon.RelayInfo{
		ChannelMeta:   &relaycommon.ChannelMeta{UpstreamModelName: "48:seedance-2.0-fast"},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	a := &TaskAdaptor{}
	require.Nil(t, a.ValidateRequestAndSetAction(c, info))

	body, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, common.Unmarshal(data, &payload))
	require.Equal(t, "48:seedance-2.0-fast", payload["model"])
	require.Equal(t, "4", payload["seconds"])
	metadata := payload["metadata"].(map[string]any)
	require.Equal(t, "frames2video", metadata["mode_type"])
	require.Len(t, metadata["images"], 2)
	require.Len(t, metadata["videos"], 1)
	require.Len(t, metadata["audios"], 1)
}

func TestValidateRejectsUnsupportedChannel50FramesAndBatch(t *testing.T) {
	for _, body := range []string{
		`{"model":"seedance-720p-fast-c50","prompt":"x","seconds":"5","n":2}`,
		`{"model":"seedance-720p-fast-c50","prompt":"x","seconds":"5","n":0}`,
		`{"model":"seedance-720p-fast-c50","prompt":"x","seconds":"5","n":-1}`,
		`{"model":"seedance-720p-fast-c50","prompt":"x","seconds":"invalid"}`,
		`{"model":"seedance-720p-fast-c50","prompt":"x","seconds":"5","metadata":{"mode_type":"frames2video","images":["a","b"]}}`,
	} {
		c := testJSONContext(body)
		taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}})
		require.NotNil(t, taskErr)
		require.Equal(t, 400, taskErr.StatusCode)
	}
}

func TestBuildRequestMergesReferencesInProviderOrderWithoutDuplicates(t *testing.T) {
	c := testJSONContext(`{
    "model": "seedance-720p-fast-c48",
    "prompt": "x",
    "image": "https://example.com/first.jpg",
    "images": ["https://example.com/first.jpg", "https://example.com/second.jpg"],
    "seconds": "4",
    "metadata": {
      "mode_type": "image2video",
	  "ratio": "9:16",
      "images": ["https://example.com/second.jpg", "https://example.com/third.jpg"]
    }
  }`)
	info := &relaycommon.RelayInfo{
		ChannelMeta:   &relaycommon.ChannelMeta{UpstreamModelName: "48:seedance-2.0-fast"},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	a := &TaskAdaptor{}
	require.Nil(t, a.ValidateRequestAndSetAction(c, info))
	body, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	data, err := io.ReadAll(body)
	require.NoError(t, err)

	var payload requestPayload
	require.NoError(t, common.Unmarshal(data, &payload))
	require.Equal(t, "9:16", payload.Size)
	require.Empty(t, payload.Metadata.Ratio)
	require.Equal(t, []string{
		"https://example.com/first.jpg",
		"https://example.com/second.jpg",
		"https://example.com/third.jpg",
	}, payload.Metadata.Images)
}

func TestBuildRequestAcceptsUnifiedTopLevelFields(t *testing.T) {
	c := testJSONContext(`{
    "model": "seedance-720p-fast-c48",
    "prompt": "统一公共字段",
    "duration": 4,
    "resolution": "720p",
    "size": "21:9",
    "mode_type": "image2video",
    "images": ["https://example.com/reference.jpg"],
    "videos": ["https://example.com/reference.mp4"],
    "audios": ["https://example.com/reference.mp3"],
    "n": 1
  }`)
	info := &relaycommon.RelayInfo{
		ChannelMeta:   &relaycommon.ChannelMeta{UpstreamModelName: "48:seedance-2.0-fast"},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	a := &TaskAdaptor{}
	require.Nil(t, a.ValidateRequestAndSetAction(c, info))
	body, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	data, err := io.ReadAll(body)
	require.NoError(t, err)

	var payload requestPayload
	require.NoError(t, common.Unmarshal(data, &payload))
	require.Equal(t, "720p", payload.Metadata.Resolution)
	require.Equal(t, "image2video", payload.Metadata.ModeType)
	require.Len(t, payload.Metadata.Images, 1)
	require.Len(t, payload.Metadata.Videos, 1)
	require.Len(t, payload.Metadata.Audios, 1)
}

func TestBuildRequestStoresBase64ReferenceImage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("REFERENCE_MEDIA_STORAGE_DIR", dir)
	originalServerAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://token.example.test"
	t.Cleanup(func() { system_setting.ServerAddress = originalServerAddress })

	c := testJSONContext(`{
    "model": "seedance-720p-fast-c48",
    "prompt": "base64 reference",
    "duration": 4,
    "mode_type": "image2video",
    "images": ["data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL7WQAAAABJRU5ErkJggg=="]
  }`)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "48:seedance-2.0-fast"}, TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	a := &TaskAdaptor{}
	require.Nil(t, a.ValidateRequestAndSetAction(c, info))
	body, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	data, err := io.ReadAll(body)
	require.NoError(t, err)

	var payload requestPayload
	require.NoError(t, common.Unmarshal(data, &payload))
	require.Len(t, payload.Metadata.Images, 1)
	require.True(t, strings.HasPrefix(payload.Metadata.Images[0], "https://token.example.test/api/reference-media/"))
	entries, err := filepath.Glob(filepath.Join(dir, "*"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestDoResponsePreservesProviderError(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body: io.NopCloser(strings.NewReader(`{
      "error": {
        "message": "model must use the complete id",
        "code": "model_ambiguous"
      }
    }`)),
	}

	_, _, taskErr := (&TaskAdaptor{}).DoResponse(c, resp, &relaycommon.RelayInfo{})
	require.NotNil(t, taskErr)
	require.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	require.Equal(t, "model_ambiguous", taskErr.Code)
	require.Contains(t, taskErr.Message, "model must use the complete id")
}

func testJSONContext(body string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/videos", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}
