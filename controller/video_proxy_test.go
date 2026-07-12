package controller

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
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
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Task{}))

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
		adminID = 1
		ownerID = 2
		otherID = 3
		taskID  = "task_owner_video"
	)
	require.NoError(t, db.Create(&model.User{Id: adminID, Username: "admin", AffCode: "admin", Role: common.RoleAdminUser}).Error)
	require.NoError(t, db.Create(&model.User{Id: ownerID, Username: "owner", AffCode: "owner", Role: common.RoleCommonUser}).Error)
	require.NoError(t, db.Create(&model.User{Id: otherID, Username: "other", AffCode: "other", Role: common.RoleCommonUser}).Error)
	require.NoError(t, db.Create(&model.Task{TaskID: taskID, UserId: ownerID}).Error)

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
