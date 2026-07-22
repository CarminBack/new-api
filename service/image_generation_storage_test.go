package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSaveImageGenerationResponseStoresFileAndRecord(t *testing.T) {
	truncateServiceImageGenerationTables(t)

	storageDir := t.TempDir()
	t.Setenv("IMAGE_GENERATION_STORAGE_DIR", storageDir)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set(common.RequestIdKey, "req_image_store")

	responseBody, err := common.Marshal(dto.ImageResponse{
		Data: []dto.ImageData{
			{
				B64Json: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII=",
				Url:     "http://127.0.0.1/should-not-be-fetched.png",
			},
		},
	})
	require.NoError(t, err)

	relayInfo := &relaycommon.RelayInfo{
		UserId:          7,
		TokenId:         8,
		OriginModelName: "gemini-3.1-flash-image",
		UsingGroup:      "Image",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId: 9,
		},
	}
	request := &dto.ImageRequest{
		Prompt:  "a red cube",
		Size:    "1024x1024",
		Quality: "standard",
	}

	SaveImageGenerationResponse(c, relayInfo, request, responseBody, 50000)

	var records []model.ImageGeneration
	require.NoError(t, model.DB.Find(&records).Error)
	require.Len(t, records, 1)
	require.Equal(t, "req_image_store", records[0].RequestId)
	require.Equal(t, "gemini-3.1-flash-image", records[0].ModelName)
	require.Equal(t, "a red cube", records[0].Prompt)
	require.Equal(t, "1024x1024", records[0].Size)
	require.Equal(t, 50000, records[0].Quota)
	require.Equal(t, model.ImageGenerationStatusSuccess, records[0].Status)
	require.NotEmpty(t, records[0].FilePath)

	_, err = os.Stat(filepath.Join(storageDir, records[0].FilePath))
	require.NoError(t, err)
}

func TestSaveImageGenerationResponseStoresURLFileAndRecord(t *testing.T) {
	truncateServiceImageGenerationTables(t)

	storageDir := t.TempDir()
	t.Setenv("IMAGE_GENERATION_STORAGE_DIR", storageDir)
	pngBytes, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII=")
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "image/png,image/jpeg,image/webp,image/gif" {
			t.Errorf("Accept header = %q", got)
			http.Error(w, "unexpected Accept header", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		if _, writeErr := w.Write(pngBytes); writeErr != nil {
			t.Errorf("write image response: %v", writeErr)
		}
	}))
	defer server.Close()
	allowPrivateImageArchiveServer(t, server.URL)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", nil)
	c.Set(common.RequestIdKey, "req_image_url_store")

	responseBody, err := common.Marshal(dto.ImageResponse{
		Data: []dto.ImageData{{Url: server.URL + "/image.png"}},
	})
	require.NoError(t, err)
	relayInfo := &relaycommon.RelayInfo{
		UserId:          17,
		TokenId:         18,
		OriginModelName: "gpt-image-url",
		UsingGroup:      "Image",
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: 19},
	}
	request := &dto.ImageRequest{Prompt: "a blue cube", Size: "1024x1024"}

	SaveImageGenerationResponse(c, relayInfo, request, responseBody, 60000)

	var records []model.ImageGeneration
	require.NoError(t, model.DB.Find(&records).Error)
	require.Len(t, records, 1)
	require.Equal(t, "req_image_url_store", records[0].RequestId)
	require.Equal(t, "image/png", records[0].MimeType)
	require.Equal(t, 60000, records[0].Quota)
	storedBytes, err := os.ReadFile(filepath.Join(storageDir, records[0].FilePath))
	require.NoError(t, err)
	require.Equal(t, pngBytes, storedBytes)
}

func TestSaveImageGenerationResponseRejectsPrivateURL(t *testing.T) {
	truncateServiceImageGenerationTables(t)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", nil)
	c.Set(common.RequestIdKey, "req_image_private_url")

	responseBody, err := common.Marshal(dto.ImageResponse{
		Data: []dto.ImageData{{Url: "http://127.0.0.1/private.png"}},
	})
	require.NoError(t, err)
	relayInfo := &relaycommon.RelayInfo{
		UserId:          27,
		OriginModelName: "gpt-image-url",
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: 29},
	}

	SaveImageGenerationResponse(c, relayInfo, &dto.ImageRequest{Prompt: "private"}, responseBody, 100)

	var count int64
	require.NoError(t, model.DB.Model(&model.ImageGeneration{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestDownloadImageGenerationURLRequiresSSRFProtection(t *testing.T) {
	fetchSetting := system_setting.GetFetchSetting()
	original := *fetchSetting
	fetchSetting.EnableSSRFProtection = false
	t.Cleanup(func() {
		*fetchSetting = original
	})

	_, _, _, err := downloadImageGenerationURL(context.Background(), "https://example.com/image.png")

	require.ErrorContains(t, err, "SSRF protection must be enabled")
}

func TestDecodeImageGenerationURLResponseRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name        string
		response    *http.Response
		maxBytes    int64
		errContains string
	}{
		{
			name: "non-success status",
			response: &http.Response{
				StatusCode: http.StatusForbidden,
				Body:       io.NopCloser(bytes.NewReader(nil)),
			},
			maxBytes:    1024,
			errContains: "HTTP 403",
		},
		{
			name: "declared oversized",
			response: &http.Response{
				StatusCode:    http.StatusOK,
				ContentLength: 1025,
				Body:          io.NopCloser(bytes.NewReader(nil)),
			},
			maxBytes:    1024,
			errContains: "exceeds limit",
		},
		{
			name: "streamed oversized",
			response: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(bytes.Repeat([]byte{0x89}, 1025))),
			},
			maxBytes:    1024,
			errContains: "exceeds limit",
		},
		{
			name: "html body",
			response: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString("<html>not an image</html>")),
			},
			maxBytes:    1024,
			errContains: "unsupported downloaded image content type",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, _, err := decodeImageGenerationURLResponse(test.response, test.maxBytes)
			require.ErrorContains(t, err, test.errContains)
		})
	}
}

func TestCleanupExpiredImageGenerationsDeletesFileAndMarksExpired(t *testing.T) {
	truncateServiceImageGenerationTables(t)

	storageDir := t.TempDir()
	t.Setenv("IMAGE_GENERATION_STORAGE_DIR", storageDir)

	relativePath := filepath.Join("20260710", "user-1", "expired.png")
	absolutePath := filepath.Join(storageDir, relativePath)
	require.NoError(t, os.MkdirAll(filepath.Dir(absolutePath), 0750))
	require.NoError(t, os.WriteFile(absolutePath, []byte("png"), 0600))

	record := &model.ImageGeneration{
		UserId:    1,
		RequestId: "req_expired",
		FilePath:  relativePath,
		Status:    model.ImageGenerationStatusSuccess,
		CreatedAt: time.Now().Add(-8 * 24 * time.Hour).Unix(),
		ExpireAt:  time.Now().Add(-time.Hour).Unix(),
	}
	require.NoError(t, model.DB.Create(record).Error)

	CleanupExpiredImageGenerations()

	_, err := os.Stat(absolutePath)
	require.True(t, os.IsNotExist(err))

	var reloaded model.ImageGeneration
	require.NoError(t, model.DB.First(&reloaded, record.Id).Error)
	require.Equal(t, model.ImageGenerationStatusExpired, reloaded.Status)
	require.Empty(t, reloaded.FilePath)
}

func truncateServiceImageGenerationTables(t *testing.T) {
	t.Helper()
	require.NoError(t, model.DB.Exec("DELETE FROM image_generations").Error)
}

func allowPrivateImageArchiveServer(t *testing.T, serverURL string) {
	t.Helper()
	parsedURL, err := url.Parse(serverURL)
	require.NoError(t, err)
	_, port, err := net.SplitHostPort(parsedURL.Host)
	require.NoError(t, err)

	fetchSetting := system_setting.GetFetchSetting()
	original := *fetchSetting
	*fetchSetting = system_setting.FetchSetting{
		EnableSSRFProtection:   true,
		AllowPrivateIp:         true,
		AllowedPorts:           []string{port},
		ApplyIPFilterForDomain: true,
	}
	InitHttpClient()
	t.Cleanup(func() {
		*fetchSetting = original
		InitHttpClient()
	})
}
