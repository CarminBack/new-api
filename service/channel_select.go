package service

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
)

type RetryParam struct {
	Ctx                 *gin.Context
	TokenGroup          string
	ModelName           string
	RequestPath         string
	ImageResolutionTier string
	Retry               *int
	Attempted           model.ChannelKeyExclusions
}

func (p *RetryParam) MarkAttempted(channelID int, keyIndex int) {
	if channelID <= 0 {
		return
	}
	if p.Attempted == nil {
		p.Attempted = make(model.ChannelKeyExclusions)
	}
	if p.Attempted[channelID] == nil {
		p.Attempted[channelID] = make(map[int]struct{})
	}
	p.Attempted[channelID][keyIndex] = struct{}{}
}

func (p *RetryParam) ExcludedKeys(channelID int) map[int]struct{} {
	if p == nil || p.Attempted == nil {
		return nil
	}
	return p.Attempted[channelID]
}

func (p *RetryParam) ExcludeChannel(channel *model.Channel) {
	if p == nil || channel == nil {
		return
	}
	keyCount := 1
	if channel.ChannelInfo.IsMultiKey {
		keyCount = len(channel.GetKeys())
	}
	if keyCount == 0 {
		keyCount = 1
	}
	for keyIndex := 0; keyIndex < keyCount; keyIndex++ {
		p.MarkAttempted(channel.Id, keyIndex)
	}
}

func (p *RetryParam) GetRetry() int {
	if p.Retry == nil {
		return 0
	}
	return *p.Retry
}

func (p *RetryParam) IncreaseRetry() {
	if p.Retry == nil {
		p.Retry = new(int)
	}
	*p.Retry++
}

// CacheGetRandomSatisfiedChannel tries to get a random channel that satisfies the requirements.
// 尝试获取一个满足要求的随机渠道。
//
// For "auto" tokenGroup with cross-group Retry enabled:
// 对于启用了跨分组重试的 "auto" tokenGroup：
//
//   - Request-local channel/key exclusions make each retry pick the highest-priority
//     untried candidate instead of repeating the lowest priority.
//   - The initial selection may scan later groups when earlier groups have no model.
//   - A retry moves to a later group only when cross-group retry is enabled.
func CacheGetRandomSatisfiedChannel(param *RetryParam) (*model.Channel, string, error) {
	var channel *model.Channel
	var err error
	selectGroup := param.TokenGroup
	userGroup := common.GetContextKeyString(param.Ctx, constant.ContextKeyUserGroup)

	if param.TokenGroup == "auto" {
		if len(setting.GetAutoGroups()) == 0 {
			return nil, selectGroup, errors.New("auto groups is not enabled")
		}
		autoGroups := GetUserAutoGroup(userGroup)

		// startGroupIndex: the group index to start searching from
		// startGroupIndex: 开始搜索的分组索引
		startGroupIndex := 0
		crossGroupRetry := common.GetContextKeyBool(param.Ctx, constant.ContextKeyTokenCrossGroupRetry)

		if lastGroupIndex, exists := common.GetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex); exists {
			if idx, ok := lastGroupIndex.(int); ok {
				startGroupIndex = idx
			}
		}

		for i := startGroupIndex; i < len(autoGroups); i++ {
			if param.GetRetry() > 0 && i > startGroupIndex && !crossGroupRetry {
				break
			}
			autoGroup := autoGroups[i]
			logger.LogDebug(param.Ctx, "Auto selecting group: %s", autoGroup)

			channel, err = getRandomSatisfiedChannelWithCircuit(param, autoGroup)
			if err != nil {
				return nil, autoGroup, err
			}
			if channel == nil {
				// Current group has no available channel for this model, try next group
				// 当前分组没有该模型的可用渠道，尝试下一个分组
				logger.LogDebug(param.Ctx, "No untried channel in group %s for model %s, trying next group", autoGroup, param.ModelName)
				// 重置状态以尝试下一个分组
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i+1)
				common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupRetryIndex, 0)
				continue
			}
			common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroup, autoGroup)
			selectGroup = autoGroup
			logger.LogDebug(param.Ctx, "Auto selected group: %s", autoGroup)

			common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i)
			break
		}
	} else {
		channel, err = getRandomSatisfiedChannelWithCircuit(param, param.TokenGroup)
		if err != nil {
			return nil, param.TokenGroup, err
		}
	}
	return channel, selectGroup, nil
}

func getRandomSatisfiedChannelWithCircuit(param *RetryParam, group string) (*model.Channel, error) {
	for {
		channel, err := model.GetRandomSatisfiedChannelWithExclusions(group, param.ModelName, 0, param.RequestPath, param.ImageResolutionTier, param.Attempted)
		if err != nil || channel == nil {
			return channel, err
		}
		if AllowChannelHealthAttempt(param.Ctx, channel, param.ModelName, param.RequestPath) {
			return channel, nil
		}
		logger.LogDebug(param.Ctx, "channel health unavailable, skipping channel #%d for model %s path %s", channel.Id, param.ModelName, param.RequestPath)
		param.ExcludeChannel(channel)
	}
}

// HighestRoutableChannelPriority returns the highest currently admissible
// priority in one resolved group without reserving route capacity.
func HighestRoutableChannelPriority(group string, param *RetryParam) (int64, bool, error) {
	if param == nil {
		return 0, false, nil
	}
	probe := &RetryParam{
		Ctx:                 param.Ctx,
		ModelName:           param.ModelName,
		RequestPath:         param.RequestPath,
		ImageResolutionTier: param.ImageResolutionTier,
	}
	for {
		channel, err := model.GetRandomSatisfiedChannelWithExclusions(group, probe.ModelName, 0, probe.RequestPath, probe.ImageResolutionTier, probe.Attempted)
		if err != nil || channel == nil {
			return 0, false, err
		}
		if IsChannelHealthAvailable(channel, probe.ModelName, probe.RequestPath) {
			return channel.GetPriority(), true, nil
		}
		probe.ExcludeChannel(channel)
	}
}

func FirstRoutableChannelGroup(groups []string, param *RetryParam) (string, bool, error) {
	for _, group := range groups {
		_, found, err := HighestRoutableChannelPriority(group, param)
		if err != nil {
			return group, false, err
		}
		if found {
			return group, true, nil
		}
	}
	return "", false, nil
}
