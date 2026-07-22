package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGetNextEnabledKeyExcludingSkipsAttemptedKeys(t *testing.T) {
	channel := &Channel{
		Id:  1,
		Key: "key-0\nkey-1\nkey-2",
		ChannelInfo: ChannelInfo{
			IsMultiKey:   true,
			MultiKeyMode: constant.MultiKeyModeRandom,
			MultiKeyStatusList: map[int]int{
				1: common.ChannelStatusManuallyDisabled,
			},
		},
	}

	key, index, err := channel.GetNextEnabledKeyExcluding(map[int]struct{}{0: {}})

	require.Nil(t, err)
	require.Equal(t, "key-2", key)
	require.Equal(t, 2, index)
	require.False(t, channel.HasUntriedEnabledKey(map[int]struct{}{0: {}, 2: {}}))
}

func TestDatabaseChannelSelectionExcludesAttemptedChannelsByKey(t *testing.T) {
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
	priority1 := int64(1)
	weight := uint(1)
	channels := []*Channel{
		{
			Id:       1,
			Key:      "key-0\nkey-1",
			Status:   common.ChannelStatusEnabled,
			Priority: &priority3,
			Weight:   &weight,
			ChannelInfo: ChannelInfo{
				IsMultiKey:   true,
				MultiKeyMode: constant.MultiKeyModePolling,
			},
		},
		{Id: 2, Key: "fallback", Status: common.ChannelStatusEnabled, Priority: &priority1, Weight: &weight},
	}
	for _, channel := range channels {
		require.NoError(t, db.Create(channel).Error)
		require.NoError(t, db.Create(&Ability{
			Group:     "default",
			Model:     "gpt-test",
			ChannelId: channel.Id,
			Enabled:   true,
			Priority:  channel.Priority,
			Weight:    weight,
		}).Error)
	}

	channel, err := GetRandomSatisfiedChannelWithExclusions(
		"default", "gpt-test", 0, "/v1/responses", "",
		ChannelKeyExclusions{1: {0: {}}},
	)
	require.NoError(t, err)
	require.NotNil(t, channel)
	require.Equal(t, 1, channel.Id)

	channel, err = GetRandomSatisfiedChannelWithExclusions(
		"default", "gpt-test", 0, "/v1/responses", "",
		ChannelKeyExclusions{1: {0: {}, 1: {}}},
	)
	require.NoError(t, err)
	require.NotNil(t, channel)
	require.Equal(t, 2, channel.Id)

	channel, err = GetRandomSatisfiedChannelWithExclusions(
		"default", "gpt-test", 0, "/v1/responses", "",
		ChannelKeyExclusions{1: {0: {}, 1: {}}, 2: {0: {}}},
	)
	require.NoError(t, err)
	require.Nil(t, channel)
}

func TestCachedChannelSelectionExcludesAttemptedChannelsByKey(t *testing.T) {
	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	oldGroupMap := group2model2channels
	oldChannels := channelsIDM
	defer func() {
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
		group2model2channels = oldGroupMap
		channelsIDM = oldChannels
	}()

	common.MemoryCacheEnabled = true
	priority3 := int64(3)
	priority1 := int64(1)
	weight := uint(1)
	channelsIDM = map[int]*Channel{
		1: {
			Id:       1,
			Key:      "key-0\nkey-1",
			Priority: &priority3,
			Weight:   &weight,
			ChannelInfo: ChannelInfo{
				IsMultiKey:   true,
				MultiKeyMode: constant.MultiKeyModePolling,
			},
		},
		2: {Id: 2, Key: "fallback", Priority: &priority1, Weight: &weight},
	}
	group2model2channels = map[string]map[string][]int{
		"default": {"gpt-test": {1, 2}},
	}

	channel, err := GetRandomSatisfiedChannelWithExclusions(
		"default", "gpt-test", 0, "/v1/responses", "",
		ChannelKeyExclusions{1: {0: {}}},
	)
	require.NoError(t, err)
	require.NotNil(t, channel)
	require.Equal(t, 1, channel.Id, "multi-key channel remains eligible while one key is untried")

	channel, err = GetRandomSatisfiedChannelWithExclusions(
		"default", "gpt-test", 0, "/v1/responses", "",
		ChannelKeyExclusions{1: {0: {}, 1: {}}},
	)
	require.NoError(t, err)
	require.NotNil(t, channel)
	require.Equal(t, 2, channel.Id, "selection descends after all keys at the higher priority were attempted")

	channel, err = GetRandomSatisfiedChannelWithExclusions(
		"default", "gpt-test", 0, "/v1/responses", "",
		ChannelKeyExclusions{1: {0: {}, 1: {}}, 2: {0: {}}},
	)
	require.NoError(t, err)
	require.Nil(t, channel, "selection stops instead of repeating the lowest-priority channel")
}
