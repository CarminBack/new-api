package controller

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupImageGenerationControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	require.NoError(t, db.AutoMigrate(&model.ImageGeneration{}, &model.Midjourney{}, &model.Log{}))
	t.Setenv("IMAGE_GENERATION_STORAGE_DIR", t.TempDir())
	return db
}

func TestImageGenerationContentAuthorizationAndLogRedaction(t *testing.T) {
	db := setupImageGenerationControllerTestDB(t)
	relativePath := filepath.Join("20260919", "user-1", "image.png")
	absolutePath := filepath.Join(os.Getenv("IMAGE_GENERATION_STORAGE_DIR"), relativePath)
	require.NoError(t, os.MkdirAll(filepath.Dir(absolutePath), 0750))
	require.NoError(t, os.WriteFile(absolutePath, []byte("png-data"), 0600))
	record := &model.ImageGeneration{UserId: 1, RequestId: "req_image", FilePath: relativePath, MimeType: "image/png", Status: model.ImageGenerationStatusSuccess, CreatedAt: time.Now().Unix(), ExpireAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, db.Create(record).Error)
	other := *record
	other.Id = 0
	other.UserId = 2
	require.NoError(t, db.Create(&other).Error)
	signature := model.GenerateImageGenerationContentSignature(record, record.ExpireAt)
	expired := time.Now().Add(-time.Second).Unix()
	extended := record.ExpireAt + 1
	cases := []struct {
		name      string
		id        int
		expires   int64
		signature string
		status    int
	}{
		{"valid", record.Id, record.ExpireAt, signature, http.StatusOK},
		{"missing", record.Id, record.ExpireAt, "", http.StatusUnauthorized},
		{"tampered", record.Id, record.ExpireAt, "invalid", http.StatusUnauthorized},
		{"other user record", other.Id, record.ExpireAt, signature, http.StatusUnauthorized},
		{"expired", record.Id, expired, model.GenerateImageGenerationContentSignature(record, expired), http.StatusUnauthorized},
		{"extended beyond retention", record.Id, extended, model.GenerateImageGenerationContentSignature(record, extended), http.StatusUnauthorized},
	}
	var accessLog bytes.Buffer
	previousWriter := gin.DefaultWriter
	gin.DefaultWriter = &accessLog
	t.Cleanup(func() { gin.DefaultWriter = previousWriter })
	router := gin.New()
	middleware.SetUpLogger(router)
	router.GET("/api/image-generations/:id/content", GetImageGenerationContent)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/image-generations/%d/content?expires=%d&signature=%s", tc.id, tc.expires, tc.signature), nil)
			router.ServeHTTP(result, request)
			require.Equal(t, tc.status, result.Code)
			if tc.status == http.StatusOK {
				require.Equal(t, "png-data", result.Body.String())
				require.Equal(t, "nosniff", result.Header().Get("X-Content-Type-Options"))
			}
		})
	}
	require.NotContains(t, accessLog.String(), signature)
	require.NotContains(t, accessLog.String(), "signature=")
	require.NoError(t, db.Model(record).Update("file_path", "../outside.png").Error)
	record.FilePath = "../outside.png"
	result := httptest.NewRecorder()
	router.ServeHTTP(result, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/image-generations/%d/content?expires=%d&signature=%s", record.Id, record.ExpireAt, model.GenerateImageGenerationContentSignature(record, record.ExpireAt)), nil))
	require.Equal(t, http.StatusGone, result.Code)
}

func TestImageArchivePreservesPayloadShapesAndSettledQuota(t *testing.T) {
	const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII="
	item := dto.ImageData{B64Json: png}
	cases := []struct {
		name  string
		data  any
		count int
	}{
		{"array", []dto.ImageData{item}, 1},
		{"object", item, 1},
		{"split representations", []dto.ImageData{{Url: "http://127.0.0.1/never-fetch.png"}, item}, 1},
		{"metadata does not take quota", []dto.ImageData{item, {RevisedPrompt: "metadata"}}, 1},
		{"remainder belongs to last image", []dto.ImageData{item, item}, 2},
		{"metadata only", []dto.ImageData{{RevisedPrompt: "metadata"}}, 0},
		{"non-image base64", []dto.ImageData{{B64Json: base64.StdEncoding.EncodeToString([]byte("<html>not an image</html>"))}}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := setupImageGenerationControllerTestDB(t)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set(common.RequestIdKey, "local-image-archive")
			payload, err := common.Marshal(map[string]any{"data": tc.data})
			require.NoError(t, err)
			info := &relaycommon.RelayInfo{UserId: 7, TokenId: 8, OriginModelName: "test-image", UsingGroup: "Image", ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 9}}
			service.SaveImageGenerationResponse(c, info, &dto.ImageRequest{Prompt: "local archive verification"}, payload, 7)
			var records []model.ImageGeneration
			require.NoError(t, db.Order("id").Find(&records).Error)
			require.Len(t, records, tc.count)
			total := 0
			for _, record := range records {
				total += record.Quota
				require.Equal(t, "image/png", record.MimeType)
				stored, err := os.ReadFile(service.GetImageGenerationAbsolutePath(&record))
				require.NoError(t, err)
				expected, err := base64.StdEncoding.DecodeString(png)
				require.NoError(t, err)
				require.Equal(t, expected, stored)
			}
			if tc.count > 0 {
				require.Equal(t, 7, total)
			}
		})
	}
}

func TestDrawingHistoryKeepsOwnershipFiltersAndPagination(t *testing.T) {
	db := setupImageGenerationControllerTestDB(t)
	now := time.Now().Unix()
	rows := []model.ImageGeneration{
		{UserId: 1, ChannelId: 7, RequestId: "own-image", CreatedAt: now, UseTime: 1, Status: model.ImageGenerationStatusSuccess, FilePath: "test.png", ExpireAt: now + 3600},
		{UserId: 2, ChannelId: 7, RequestId: "other-image", CreatedAt: now + 1, UseTime: 1, Status: model.ImageGenerationStatusSuccess},
	}
	require.NoError(t, db.Create(&rows).Error)
	require.NoError(t, db.Create(&model.Midjourney{UserId: 1, MjId: "own-mj", SubmitTime: (now - 10) * 1000}).Error)
	require.EqualValues(t, 3, model.CountAllDrawingLogs(model.TaskQueryParams{}))
	require.EqualValues(t, 2, model.CountAllUserDrawingLogs(1, model.TaskQueryParams{}))
	first := model.GetAllUserDrawingLogs(1, 0, 1, model.TaskQueryParams{})
	require.Len(t, first, 1)
	require.Equal(t, "own-image", first[0].MjId)
	require.Zero(t, first[0].ChannelId)
	second := model.GetAllUserDrawingLogs(1, 1, 1, model.TaskQueryParams{})
	require.Len(t, second, 1)
	require.Equal(t, "own-mj", second[0].MjId)
	filtered := model.TaskQueryParams{ChannelID: "7", MjID: "own-image", StartTimestamp: strconv.FormatInt((now-1)*1000, 10), EndTimestamp: strconv.FormatInt((now+1)*1000, 10)}
	require.EqualValues(t, 1, model.CountAllUserDrawingLogs(1, filtered))
	require.Empty(t, model.GetAllUserDrawingLogs(2, 0, 20, filtered))

	originalForward := setting.MjForwardUrlEnabled
	setting.MjForwardUrlEnabled = true
	t.Cleanup(func() { setting.MjForwardUrlEnabled = originalForward })
	for _, handler := range []gin.HandlerFunc{GetAllMidjourney, GetUserMidjourney} {
		response := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(response)
		c.Set("id", 1)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/mj/?p=1&page_size=20", nil)
		handler(c)
		var result struct {
			Success bool `json:"success"`
			Data    struct {
				Items []*model.Midjourney `json:"items"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
		require.True(t, result.Success)
		found := false
		for _, item := range result.Data.Items {
			if item.MjId == "own-image" {
				require.Contains(t, item.ImageUrl, fmt.Sprintf("/api/image-generations/%d/content?", rows[0].Id))
				require.Contains(t, item.ImageUrl, "signature=")
				found = true
			}
		}
		require.True(t, found)
	}
}
