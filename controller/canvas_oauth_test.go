package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestGetOrCreateCanvasTokenCreatesAndReusesImageGroupToken(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	config := canvasOAuthConfig{TokenName: "无限画布自动授权", TokenGroup: "image"}

	created, err := getOrCreateCanvasToken(7, config)
	require.NoError(t, err)
	require.Equal(t, 7, created.UserId)
	require.Equal(t, config.TokenName, created.Name)
	require.Equal(t, config.TokenGroup, created.Group)
	require.Equal(t, common.TokenStatusEnabled, created.Status)
	require.True(t, created.UnlimitedQuota)
	require.EqualValues(t, -1, created.ExpiredTime)
	require.NotEmpty(t, created.Key)

	reused, err := getOrCreateCanvasToken(7, config)
	require.NoError(t, err)
	require.Equal(t, created.Id, reused.Id)
	require.Equal(t, created.Key, reused.Key)

	var count int64
	require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", 7).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestGetOrCreateCanvasTokenDoesNotReenableDisabledToken(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	config := canvasOAuthConfig{TokenName: "无限画布自动授权", TokenGroup: "image"}
	disabled := &model.Token{
		UserId:         8,
		Key:            "disabled-key",
		Status:         common.TokenStatusDisabled,
		Name:           config.TokenName,
		CreatedTime:    1,
		AccessedTime:   1,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		Group:          config.TokenGroup,
	}
	require.NoError(t, db.Create(disabled).Error)

	reused, err := getOrCreateCanvasToken(8, config)
	require.NoError(t, err)
	require.Equal(t, disabled.Id, reused.Id)
	require.Equal(t, common.TokenStatusDisabled, reused.Status)
}
