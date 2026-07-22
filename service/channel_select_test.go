package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestRetryParamExcludeChannelExcludesEveryKey(t *testing.T) {
	param := &RetryParam{}
	channel := &model.Channel{
		Id:   37,
		Key:  "key-0\nkey-1",
		Keys: []string{"key-0", "key-1"},
		ChannelInfo: model.ChannelInfo{
			IsMultiKey: true,
		},
	}

	param.ExcludeChannel(channel)

	require.Equal(t, map[int]struct{}{0: {}, 1: {}}, param.ExcludedKeys(channel.Id))
}
