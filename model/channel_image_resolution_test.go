package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCachedChannelSelectionFiltersByImageResolutionTier(t *testing.T) {
	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	oldGroupMap := group2model2channels
	oldChannels := channelsIDM
	oldSettings := channel2settings
	defer func() {
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
		group2model2channels = oldGroupMap
		channelsIDM = oldChannels
		channel2settings = oldSettings
	}()

	common.MemoryCacheEnabled = true
	priority3 := int64(3)
	priority2 := int64(2)
	priority0 := int64(0)
	weight := uint(1)
	channelsIDM = map[int]*Channel{
		22: {Id: 22, Priority: &priority3, Weight: &weight},
		25: {Id: 25, Priority: &priority2, Weight: &weight},
		60: {Id: 60, Priority: &priority0, Weight: &weight},
	}
	group2model2channels = map[string]map[string][]int{
		"Image": {"gpt-image-2": {22, 25, 60}},
	}
	channel2settings = map[int]dto.ChannelSettings{
		22: {ImageResolutionTiers: map[string][]string{"gpt-image-2": {"1k"}}},
		25: {},
		60: {ImageResolutionTiers: map[string][]string{"gpt-image-2": {"1k", "2k", "4k"}}},
	}

	tests := []struct {
		name  string
		tier  string
		retry int
		want  int
	}{
		{name: "1k prefers image3", tier: "1k", retry: 0, want: 22},
		{name: "1k retries to high-resolution provider", tier: "1k", retry: 1, want: 60},
		{name: "2k excludes 1k-only and undeclared channels", tier: "2k", retry: 0, want: 60},
		{name: "2k retry stays in tier", tier: "2k", retry: 1, want: 60},
		{name: "4k selects declared provider", tier: "4k", retry: 0, want: 60},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel, err := GetRandomSatisfiedChannel("Image", "gpt-image-2", tt.retry, "/v1/images/generations", tt.tier)
			require.NoError(t, err)
			require.NotNil(t, channel)
			assert.Equal(t, tt.want, channel.Id)
		})
	}

	assert.True(t, IsChannelEnabledForGroupModelWithImageResolution("Image", "gpt-image-2", "1k", 22))
	assert.False(t, IsChannelEnabledForGroupModelWithImageResolution("Image", "gpt-image-2", "2k", 22))
	assert.False(t, IsChannelEnabledForGroupModelWithImageResolution("Image", "gpt-image-2", "2k", 25))
	assert.True(t, IsChannelEnabledForGroupModelWithImageResolution("Image", "gpt-image-2", "2k", 60))
}

func TestCachedChannelSelectionPreservesLegacyGroupsWithoutDeclarations(t *testing.T) {
	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	oldGroupMap := group2model2channels
	oldChannels := channelsIDM
	oldSettings := channel2settings
	defer func() {
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
		group2model2channels = oldGroupMap
		channelsIDM = oldChannels
		channel2settings = oldSettings
	}()

	common.MemoryCacheEnabled = true
	priority3 := int64(3)
	priority0 := int64(0)
	weight := uint(1)
	channelsIDM = map[int]*Channel{
		1: {Id: 1, Priority: &priority3, Weight: &weight},
		2: {Id: 2, Priority: &priority0, Weight: &weight},
	}
	group2model2channels = map[string]map[string][]int{
		"legacy": {"image-model": {1, 2}},
	}
	channel2settings = map[int]dto.ChannelSettings{1: {}, 2: {}}

	channel, err := GetRandomSatisfiedChannel("legacy", "image-model", 0, "/v1/images/generations", "4k")
	require.NoError(t, err)
	require.NotNil(t, channel)
	assert.Equal(t, 1, channel.Id)
}

func TestDatabaseChannelSelectionFiltersBeforeApplyingPriority(t *testing.T) {
	oldDB := DB
	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	defer func() {
		DB = oldDB
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
	}()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}))
	DB = db
	common.MemoryCacheEnabled = false

	priority3 := int64(3)
	priority2 := int64(2)
	priority0 := int64(0)
	weight := uint(1)
	oneKSetting := `{"image_resolution_tiers":{"gpt-image-2":["1k"]}}`
	undeclaredSetting := `{}`
	allTiersSetting := `{"image_resolution_tiers":{"gpt-image-2":["1k","2k","4k"]}}`
	require.NoError(t, db.Create(&Channel{Id: 22, Key: "test", Status: common.ChannelStatusEnabled, Priority: &priority3, Weight: &weight, Setting: &oneKSetting}).Error)
	require.NoError(t, db.Create(&Channel{Id: 25, Key: "test", Status: common.ChannelStatusEnabled, Priority: &priority2, Weight: &weight, Setting: &undeclaredSetting}).Error)
	require.NoError(t, db.Create(&Channel{Id: 60, Key: "test", Status: common.ChannelStatusEnabled, Priority: &priority0, Weight: &weight, Setting: &allTiersSetting}).Error)
	require.NoError(t, db.Create(&Ability{Group: "Image", Model: "gpt-image-2", ChannelId: 22, Enabled: true, Priority: &priority3, Weight: weight}).Error)
	require.NoError(t, db.Create(&Ability{Group: "Image", Model: "gpt-image-2", ChannelId: 25, Enabled: true, Priority: &priority2, Weight: weight}).Error)
	require.NoError(t, db.Create(&Ability{Group: "Image", Model: "gpt-image-2", ChannelId: 60, Enabled: true, Priority: &priority0, Weight: weight}).Error)

	tests := []struct {
		name  string
		tier  string
		retry int
		want  int
	}{
		{name: "1k prefers image3", tier: "1k", retry: 0, want: 22},
		{name: "1k retry excludes undeclared channel", tier: "1k", retry: 1, want: 60},
		{name: "2k filters before priority", tier: "2k", retry: 0, want: 60},
		{name: "2k retry remains on supported channel", tier: "2k", retry: 1, want: 60},
		{name: "4k selects declared provider", tier: "4k", retry: 0, want: 60},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel, err := GetRandomSatisfiedChannel("Image", "gpt-image-2", tt.retry, "/v1/images/generations", tt.tier)
			require.NoError(t, err)
			require.NotNil(t, channel)
			assert.Equal(t, tt.want, channel.Id)
		})
	}

	assert.False(t, IsChannelEnabledForGroupModelWithImageResolution("Image", "gpt-image-2", "2k", 22))
	assert.False(t, IsChannelEnabledForGroupModelWithImageResolution("Image", "gpt-image-2", "2k", 25))
	assert.True(t, IsChannelEnabledForGroupModelWithImageResolution("Image", "gpt-image-2", "2k", 60))
}
