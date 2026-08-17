package controller

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupVideoProxyTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	originalDB := model.DB
	originalMainDatabaseType := common.MainDatabaseType()
	originalLogDatabaseType := common.LogDatabaseType()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.LogDatabaseType())
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}, &model.Task{}))

	t.Cleanup(func() {
		model.DB = originalDB
		common.SetDatabaseTypes(originalMainDatabaseType, originalLogDatabaseType)
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	return db
}

func TestGetAccessibleVideoTaskPermissions(t *testing.T) {
	db := setupVideoProxyTestDB(t)

	const (
		rootID  = 1
		adminID = 2
		ownerID = 3
		otherID = 4
		taskID  = "task_owner_video"
	)
	require.NoError(t, db.Create(&model.User{Id: rootID, Username: "root", AffCode: "root", Role: common.RoleRootUser}).Error)
	require.NoError(t, db.Create(&model.User{Id: adminID, Username: "admin", AffCode: "admin", Role: common.RoleAdminUser}).Error)
	require.NoError(t, db.Create(&model.User{Id: ownerID, Username: "owner", AffCode: "owner", Role: common.RoleCommonUser}).Error)
	require.NoError(t, db.Create(&model.User{Id: otherID, Username: "other", AffCode: "other", Role: common.RoleCommonUser}).Error)
	require.NoError(t, db.Create(&model.Task{TaskID: taskID, UserId: ownerID}).Error)

	t.Run("root can access another user's task", func(t *testing.T) {
		task, exists, err := getAccessibleVideoTask(rootID, taskID)
		require.NoError(t, err)
		require.True(t, exists)
		assert.Equal(t, ownerID, task.UserId)
	})

	t.Run("admin can access another user's task", func(t *testing.T) {
		task, exists, err := getAccessibleVideoTask(adminID, taskID)
		require.NoError(t, err)
		require.True(t, exists)
		assert.Equal(t, ownerID, task.UserId)
	})

	t.Run("owner can access own task", func(t *testing.T) {
		task, exists, err := getAccessibleVideoTask(ownerID, taskID)
		require.NoError(t, err)
		require.True(t, exists)
		assert.Equal(t, ownerID, task.UserId)
	})

	t.Run("regular user cannot access another user's task", func(t *testing.T) {
		task, exists, err := getAccessibleVideoTask(otherID, taskID)
		require.NoError(t, err)
		assert.False(t, exists)
		assert.Empty(t, task.TaskID)
	})
}

func TestVideoPreviewProxyAcceptsValidSignedURLWithoutAuthorization(t *testing.T) {
	db := setupVideoProxyTestDB(t)
	channel := &model.Channel{Name: "signed preview", Type: constant.ChannelTypeOpenAI}
	require.NoError(t, db.Create(channel).Error)

	videoBytes := []byte("signed-video-content")
	receivedRange := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedRange <- r.Header.Get("Range")
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-5/%d", len(videoBytes)))
		w.WriteHeader(http.StatusPartialContent)
		_, err := w.Write(videoBytes[:6])
		require.NoError(t, err)
	}))
	defer upstream.Close()
	allowPrivateVideoProxyServer(t, upstream.URL)

	task := &model.Task{
		TaskID:    "task_signed_preview",
		UserId:    42,
		ChannelId: channel.Id,
		Status:    model.TaskStatusSuccess,
		PrivateData: model.TaskPrivateData{
			ResultURL: upstream.URL + "/video.mp4",
		},
	}
	require.NoError(t, db.Create(task).Error)

	expires := time.Now().Add(time.Minute).Unix()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "task_id", Value: task.TaskID}}
	ctx.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf(
		"/api/video-previews/%s/content?expires=%d&signature=%s",
		task.TaskID,
		expires,
		model.GenerateVideoPreviewContentSignature(task, expires),
	), nil)
	ctx.Request.Header.Set("Range", "bytes=0-5")

	VideoPreviewProxy(ctx)

	assert.Equal(t, http.StatusPartialContent, recorder.Code)
	assert.Equal(t, "video/mp4", recorder.Header().Get("Content-Type"))
	assert.Equal(t, videoBytes[:6], recorder.Body.Bytes())
	assert.Equal(t, "bytes=0-5", <-receivedRange)
}

func allowPrivateVideoProxyServer(t *testing.T, serverURL string) {
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
	service.InitHttpClient()
	t.Cleanup(func() {
		*fetchSetting = original
		service.InitHttpClient()
	})
}

func TestVideoPreviewProxyRejectsExpiredSignedURL(t *testing.T) {
	db := setupVideoProxyTestDB(t)
	task := &model.Task{TaskID: "task_expired_preview", UserId: 42, Status: model.TaskStatusSuccess}
	require.NoError(t, db.Create(task).Error)

	expires := time.Now().Add(-time.Minute).Unix()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "task_id", Value: task.TaskID}}
	ctx.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf(
		"/api/video-previews/%s/content?expires=%d&signature=%s",
		task.TaskID,
		expires,
		model.GenerateVideoPreviewContentSignature(task, expires),
	), nil)

	VideoPreviewProxy(ctx)

	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestTasksToDtoIncludesValidSignedVideoPreviewURL(t *testing.T) {
	task := &model.Task{
		ID:        99,
		TaskID:    "task_dto_preview",
		UserId:    42,
		Status:    model.TaskStatusSuccess,
		UpdatedAt: time.Now().Unix(),
		PrivateData: model.TaskPrivateData{
			ResultURL: "https://cdn.example.com/video.mp4",
		},
	}

	items := tasksToDto([]*model.Task{task}, false)
	require.Len(t, items, 1)
	require.NotEmpty(t, items[0].PreviewURL)

	parsed, err := url.Parse(items[0].PreviewURL)
	require.NoError(t, err)
	assert.Equal(t, "/api/video-previews/task_dto_preview/content", parsed.Path)
	expires, err := strconv.ParseInt(parsed.Query().Get("expires"), 10, 64)
	require.NoError(t, err)
	assert.True(t, model.ValidateVideoPreviewContentSignature(task, expires, parsed.Query().Get("signature")))
}

func TestResolveOpenAIVideoURLPrefersStoredResultURL(t *testing.T) {
	task := &model.Task{
		TaskID: "task_public",
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: "task_upstream",
			ResultURL:      "https://cdn.example.com/video.mp4",
		},
	}

	videoURL, requiresAuth := resolveOpenAIVideoURL(task, "https://api.example.com/")

	assert.Equal(t, "https://cdn.example.com/video.mp4", videoURL)
	assert.False(t, requiresAuth)
}

func TestResolveOpenAIVideoURLFallsBackToAuthenticatedContentEndpoint(t *testing.T) {
	task := &model.Task{
		TaskID: "task_public",
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: "task_upstream",
			ResultURL:      "https://token.example.com/v1/videos/task_public/content",
		},
	}

	videoURL, requiresAuth := resolveOpenAIVideoURL(task, "https://api.example.com/")

	assert.Equal(t, "https://api.example.com/v1/videos/task_upstream/content", videoURL)
	assert.True(t, requiresAuth)
}
