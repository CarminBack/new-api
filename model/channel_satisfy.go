package model

import (
	"slices"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

func IsChannelEnabledForGroupModel(group string, modelName string, channelID int) bool {
	if group == "" || modelName == "" || channelID <= 0 {
		return false
	}
	if !common.MemoryCacheEnabled {
		return isChannelEnabledForGroupModelDB(group, modelName, channelID)
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	if group2model2channels == nil {
		return false
	}

	if isChannelIDInList(group2model2channels[group][modelName], channelID) {
		return true
	}
	normalized := ratio_setting.RoutingMatchModelName(modelName)
	if normalized != "" && normalized != modelName {
		return isChannelIDInList(group2model2channels[group][normalized], channelID)
	}
	return false
}

func IsChannelEnabledForGroupModelWithImageResolution(group string, modelName string, imageResolutionTier string, channelID int) bool {
	if !IsChannelEnabledForGroupModel(group, modelName, channelID) {
		return false
	}
	if imageResolutionTier == "" {
		return true
	}
	filter := dto.ChannelFilter{Kind: dto.FilterImageResolution, ImageResolutionTier: imageResolutionTier}
	if !common.MemoryCacheEnabled {
		var abilities []Ability
		queryModel := modelName
		if err := DB.Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, queryModel, true).Find(&abilities).Error; err != nil {
			return false
		}
		if len(abilities) == 0 {
			queryModel = ratio_setting.RoutingMatchModelName(modelName)
			if err := DB.Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, queryModel, true).Find(&abilities).Error; err != nil {
				return false
			}
		}
		filtered := filterAbilitiesByConstraints(abilities, modelName, []dto.ChannelFilter{filter})
		for _, ability := range filtered {
			if ability.ChannelId == channelID {
				return true
			}
		}
		return false
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()
	channels := group2model2channels[group][modelName]
	if len(channels) == 0 {
		channels = group2model2channels[group][ratio_setting.RoutingMatchModelName(modelName)]
	}
	channels = filterCandidateIDsByImageResolution(channels, modelName, []dto.ChannelFilter{filter})
	return isChannelIDInList(channels, channelID)
}

func IsChannelEnabledForAnyGroupModel(groups []string, modelName string, channelID int) bool {
	if len(groups) == 0 {
		return false
	}
	for _, g := range groups {
		if IsChannelEnabledForGroupModel(g, modelName, channelID) {
			return true
		}
	}
	return false
}

func isChannelEnabledForGroupModelDB(group string, modelName string, channelID int) bool {
	var count int64
	err := DB.Model(&Ability{}).
		Where(commonGroupCol+" = ? and model = ? and channel_id = ? and enabled = ?", group, modelName, channelID, true).
		Count(&count).Error
	if err == nil && count > 0 {
		return true
	}
	normalized := ratio_setting.RoutingMatchModelName(modelName)
	if normalized == "" || normalized == modelName {
		return false
	}
	count = 0
	err = DB.Model(&Ability{}).
		Where(commonGroupCol+" = ? and model = ? and channel_id = ? and enabled = ?", group, normalized, channelID, true).
		Count(&count).Error
	return err == nil && count > 0
}

func isChannelIDInList(list []int, channelID int) bool {
	return slices.Contains(list, channelID)
}
