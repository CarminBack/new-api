package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitChannelCacheSupportsChannelGroupWithoutAbilityRows(t *testing.T) {
	resetPricingEndpointTestTables(t)

	channel := &Channel{
		Id:     501,
		Type:   constant.ChannelTypeOpenAI,
		Key:    "key-501",
		Status: common.ChannelStatusEnabled,
		Name:   "channel-with-isolated-group",
		Group:  "isolated-test-group",
		Models: "gpt-test",
	}
	require.NoError(t, DB.Create(channel).Error)

	assert.NotPanics(t, InitChannelCache)
	assert.Equal(t, []int{501}, group2model2channels["isolated-test-group"]["gpt-test"])
}
