package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestGetOrCreateCanvasTokenCreatesAndReusesGroupToken(t *testing.T) {
	db := setupTokenControllerTestDB(t)

	created, err := getOrCreateCanvasToken(7, "无限画布自动授权", "Image")
	require.NoError(t, err)
	require.Equal(t, 7, created.UserId)
	require.Equal(t, "无限画布自动授权", created.Name)
	require.Equal(t, "Image", created.Group)
	require.Equal(t, common.TokenStatusEnabled, created.Status)
	require.True(t, created.UnlimitedQuota)
	require.EqualValues(t, -1, created.ExpiredTime)
	require.NotEmpty(t, created.Key)

	reused, err := getOrCreateCanvasToken(7, "无限画布自动授权", "Image")
	require.NoError(t, err)
	require.Equal(t, created.Id, reused.Id)
	require.Equal(t, created.Key, reused.Key)

	var count int64
	require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", 7).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestCanvasTokenSpecsProvisionThreeGroups(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	config := canvasOAuthConfig{
		TokenName:    "无限画布自动授权",
		ImageGroup:   "Image",
		VideoGroup:   "Video",
		ChatGPTGroup: "ChatGPT",
	}

	specs := canvasTokenSpecs(config)
	require.Equal(t, []canvasTokenSpec{
		{Capability: "image", Name: "无限画布自动授权", Group: "Image"},
		{Capability: "video", Name: "无限画布自动授权 (Video)", Group: "Video"},
		{Capability: "text", Name: "无限画布自动授权 (ChatGPT)", Group: "ChatGPT"},
	}, specs)

	created := make(map[string]*model.Token, len(specs))
	for _, spec := range specs {
		token, err := getOrCreateCanvasToken(9, spec.Name, spec.Group)
		require.NoError(t, err)
		created[spec.Capability] = token
	}
	require.NotEqual(t, created["image"].Key, created["video"].Key)
	require.NotEqual(t, created["video"].Key, created["text"].Key)

	var count int64
	require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", 9).Count(&count).Error)
	require.EqualValues(t, 3, count)

	for _, spec := range specs {
		reused, err := getOrCreateCanvasToken(9, spec.Name, spec.Group)
		require.NoError(t, err)
		require.Equal(t, created[spec.Capability].Id, reused.Id)
	}
	require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", 9).Count(&count).Error)
	require.EqualValues(t, 3, count)
}

func TestGetOrCreateCanvasTokenDoesNotReenableDisabledToken(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	disabled := &model.Token{
		UserId:         8,
		Key:            "disabled-key",
		Status:         common.TokenStatusDisabled,
		Name:           "无限画布自动授权 (Video)",
		CreatedTime:    1,
		AccessedTime:   1,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		Group:          "Video",
	}
	require.NoError(t, db.Create(disabled).Error)

	reused, err := getOrCreateCanvasToken(8, disabled.Name, disabled.Group)
	require.NoError(t, err)
	require.Equal(t, disabled.Id, reused.Id)
	require.Equal(t, common.TokenStatusDisabled, reused.Status)
}
