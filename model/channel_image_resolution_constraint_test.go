package model

import (
	"testing"

	filterdto "github.com/QuantumNous/new-api/dto"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
)

func TestFilterCandidateIDsByImageResolutionIsProgressive(t *testing.T) {
	oldChannels := channelsIDM
	channelsIDM = map[int]*Channel{
		1: channelWithImageTiers(t, 1, nil),
		2: channelWithImageTiers(t, 2, map[string][]string{"gpt-image-2": {"1k"}}),
		3: channelWithImageTiers(t, 3, map[string][]string{"gpt-image-2": {"2k", "4k"}}),
	}
	t.Cleanup(func() { channelsIDM = oldChannels })

	filter := func(tier string) []filterdto.ChannelFilter {
		return []filterdto.ChannelFilter{{Kind: filterdto.FilterImageResolution, ImageResolutionTier: tier}}
	}

	assert.Equal(t, []int{1}, filterCandidateIDsByImageResolution([]int{1}, "gpt-image-2", filter("4k")), "an entirely undeclared pool keeps legacy routing")
	assert.Equal(t, []int{3}, filterCandidateIDsByImageResolution([]int{1, 2, 3}, "gpt-image-2", filter("4k")), "a declared pool excludes undeclared and unsupported channels")
	assert.Equal(t, []int{2}, filterCandidateIDsByImageResolution([]int{1, 2, 3}, "gpt-image-2", filter("1k")))
	assert.Empty(t, filterCandidateIDsByImageResolution([]int{1, 2}, "gpt-image-2", filter("4k")))
}

func channelWithImageTiers(t *testing.T, id int, tiers map[string][]string) *Channel {
	t.Helper()
	channel := &Channel{Id: id}
	setting := kitdto.ChannelSettings{ImageResolutionTiers: tiers}
	channel.SetSetting(setting)
	return channel
}
