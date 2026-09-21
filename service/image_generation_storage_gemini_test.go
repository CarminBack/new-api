package service

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupGeminiImageArchiveTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(&model.ImageGeneration{}))
	t.Setenv("IMAGE_GENERATION_STORAGE_DIR", t.TempDir())
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func geminiInlineImageResponse(mimeType, data string) *dto.GeminiChatResponse {
	return &dto.GeminiChatResponse{Candidates: []dto.GeminiChatCandidate{{
		Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{
			InlineData: &dto.GeminiInlineData{MimeType: mimeType, Data: data},
		}}},
	}}}
}

func TestGeminiImageCaptureRejectsInvalidDataBeforeBilling(t *testing.T) {
	const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII="
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	CaptureGeminiImageGeneration(c, &dto.GeminiChatResponse{Candidates: []dto.GeminiChatCandidate{{
		Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
			{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "not-base64"}},
			{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: base64.StdEncoding.EncodeToString([]byte("not an image"))}},
			{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: png}},
		}},
	}}})
	require.Equal(t, 1, CapturedGeminiImageGenerationCount(c))
}

func TestGeminiImageCapturePreservesIdenticalImagesWithinOneResponse(t *testing.T) {
	const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII="
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	response := &dto.GeminiChatResponse{Candidates: []dto.GeminiChatCandidate{{
		Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
			{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: png}},
			{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: png}},
		}},
	}}}
	CaptureGeminiImageGeneration(c, response)
	require.Equal(t, 2, CapturedGeminiImageGenerationCount(c))
	CaptureGeminiImageGeneration(c, response)
	require.Equal(t, 2, CapturedGeminiImageGenerationCount(c))
}

func TestGeminiImageCaptureCountsIdenticalImagesAcrossResponses(t *testing.T) {
	const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII="
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	CaptureGeminiImageGeneration(c, geminiInlineImageResponse("image/png", png))
	CaptureGeminiImageGeneration(c, geminiInlineImageResponse("image/png", png))
	require.Equal(t, 2, CapturedGeminiImageGenerationCount(c))
}

func TestGeminiImageCapturePreservesValidCountWhenArchiveLimitReached(t *testing.T) {
	const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII="
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(pendingGeminiImagesContextKey, &pendingGeminiImages{
		seenResponses: make(map[*dto.GeminiChatResponse]struct{}),
		size:          maxPendingGeminiImageBytes - 1,
	})
	CaptureGeminiImageGeneration(c, geminiInlineImageResponse("image/png", png))
	require.Equal(t, 1, CapturedGeminiImageGenerationCount(c))
	pending, _ := c.Get(pendingGeminiImagesContextKey)
	require.True(t, pending.(*pendingGeminiImages).archiveLimited)
	require.Empty(t, pending.(*pendingGeminiImages).images)
}

func TestResetPendingGeminiImageGenerationIsolatesRetries(t *testing.T) {
	const png1 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII="
	const png2 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	CaptureGeminiImageGeneration(c, geminiInlineImageResponse("image/png", png1))
	require.Equal(t, 1, CapturedGeminiImageGenerationCount(c))

	ResetPendingGeminiImageGeneration(c)
	require.Zero(t, CapturedGeminiImageGenerationCount(c))
	CaptureGeminiImageGeneration(c, geminiInlineImageResponse("image/png", png2))
	require.Equal(t, 1, CapturedGeminiImageGenerationCount(c))
}

func TestCleanupExpiredImageGenerationsRetriesFileRemovalFailure(t *testing.T) {
	db := setupGeminiImageArchiveTestDB(t)
	relativePath := filepath.Join("blocked", "non-empty")
	absolutePath := imageGenerationFilePath(relativePath)
	require.NoError(t, os.MkdirAll(absolutePath, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(absolutePath, "child"), []byte("x"), 0600))
	record := &model.ImageGeneration{
		UserId: 1, RequestId: "cleanup-retry", FilePath: relativePath,
		Status: model.ImageGenerationStatusSuccess, ExpireAt: 1,
	}
	require.NoError(t, db.Create(record).Error)

	CleanupExpiredImageGenerations()
	require.NoError(t, db.First(record, record.Id).Error)
	require.Equal(t, model.ImageGenerationStatusSuccess, record.Status)
	require.Equal(t, relativePath, record.FilePath)

	require.NoError(t, os.RemoveAll(absolutePath))
	CleanupExpiredImageGenerations()
	require.NoError(t, db.First(record, record.Id).Error)
	require.Equal(t, model.ImageGenerationStatusExpired, record.Status)
	require.Empty(t, record.FilePath)
}

func TestGeminiImageArchiveDatabaseFailureRemovesAllFiles(t *testing.T) {
	db := setupGeminiImageArchiveTestDB(t)
	const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII="
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(common.RequestIdKey, "gemini-db-failure")
	CaptureGeminiImageGeneration(c, geminiInlineImageResponse("image/png", png))
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:image-generation-create-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "image_generations" {
			tx.AddError(errors.New("injected image generation insert failure"))
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Create().Remove("test:image-generation-create-failure") })

	SavePendingGeminiImageGeneration(c, &relaycommon.RelayInfo{
		UserId: 7, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 9},
	}, &dto.ImageRequest{Prompt: "database failure"}, 50000)

	var count int64
	require.NoError(t, db.Model(&model.ImageGeneration{}).Count(&count).Error)
	require.Zero(t, count)
	var files []string
	require.NoError(t, filepath.Walk(os.Getenv("IMAGE_GENERATION_STORAGE_DIR"), func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			files = append(files, path)
		}
		return err
	}))
	require.Empty(t, files)
}

func TestGeminiImageArchiveUsesSettledQuotaAndSkipsDuplicates(t *testing.T) {
	db := setupGeminiImageArchiveTestDB(t)
	const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII="
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(common.RequestIdKey, "gemini-image-test")
	response := geminiInlineImageResponse("image/png", png)
	CaptureGeminiImageGeneration(c, response)
	CaptureGeminiImageGeneration(c, response)
	require.Equal(t, 1, CapturedGeminiImageGenerationCount(c))
	SavePendingGeminiImageGeneration(c, &relaycommon.RelayInfo{
		UserId: 7, TokenId: 8, OriginModelName: "gemini-3-pro-image", UsingGroup: "Image",
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 9},
	}, &dto.ImageRequest{Prompt: "draw a cat", Size: "2K", Quality: "standard"}, 80000)

	var records []model.ImageGeneration
	require.NoError(t, db.Find(&records).Error)
	require.Len(t, records, 1)
	require.Equal(t, 80000, records[0].Quota)
	require.Equal(t, "draw a cat", records[0].Prompt)
	require.Equal(t, "2K", records[0].Size)
	require.Equal(t, "image/png", records[0].MimeType)
	raw, err := os.ReadFile(GetImageGenerationAbsolutePath(&records[0]))
	require.NoError(t, err)
	expected, err := base64.StdEncoding.DecodeString(png)
	require.NoError(t, err)
	require.Equal(t, expected, raw)

	SavePendingGeminiImageGeneration(c, &relaycommon.RelayInfo{UserId: 7}, &dto.ImageRequest{Prompt: "duplicate"}, 1)
	require.NoError(t, db.Find(&records).Error)
	require.Len(t, records, 1)
}

func TestGeminiImageArchiveMultipleImagesSplitSettledQuota(t *testing.T) {
	db := setupGeminiImageArchiveTestDB(t)
	png1 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII="
	png2 := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(common.RequestIdKey, "gemini-multi-test")
	CaptureGeminiImageGeneration(c, &dto.GeminiChatResponse{Candidates: []dto.GeminiChatCandidate{{
		Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
			{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: png1}},
			{Text: "caption"},
			{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: png2}},
		}},
	}}})
	require.Equal(t, 2, CapturedGeminiImageGenerationCount(c))
	SavePendingGeminiImageGeneration(c, &relaycommon.RelayInfo{
		UserId: 1, TokenId: 2, OriginModelName: "gemini-3.1-flash-image", UsingGroup: "Image",
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 3},
	}, &dto.ImageRequest{Prompt: "two images", Size: "2K"}, 80001)

	var records []model.ImageGeneration
	require.NoError(t, db.Order("id").Find(&records).Error)
	require.Len(t, records, 2)
	require.Equal(t, 80001, records[0].Quota+records[1].Quota)
	require.Equal(t, 40000, records[0].Quota)
	require.Equal(t, 40001, records[1].Quota)
}

func TestGeminiImageArchiveIgnoresTextAndInvalidImageData(t *testing.T) {
	db := setupGeminiImageArchiveTestDB(t)
	const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII="
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	CaptureGeminiImageGeneration(c, &dto.GeminiChatResponse{Candidates: []dto.GeminiChatCandidate{{
		Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{
			{Text: "text only"},
			{InlineData: &dto.GeminiInlineData{MimeType: "text/plain", Data: "ignored"}},
			{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "not-base64"}},
			{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: png}},
		}},
	}}})
	SavePendingGeminiImageGeneration(c, &relaycommon.RelayInfo{
		UserId: 1, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 2},
	}, &dto.ImageRequest{Prompt: "mixed validity"}, 10)
	var records []model.ImageGeneration
	require.NoError(t, db.Find(&records).Error)
	require.Len(t, records, 1)
	require.Equal(t, 10, records[0].Quota)
	require.Equal(t, 0, records[0].ImageIndex)
}
