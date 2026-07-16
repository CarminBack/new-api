package model

import (
	"strings"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

func GetModelEnableGroups(modelName string) []string {
	// 确保缓存最新
	GetPricing()

	if modelName == "" {
		return make([]string, 0)
	}

	modelEnableGroupsLock.RLock()
	groups, ok := modelEnableGroups[modelName]
	modelEnableGroupsLock.RUnlock()
	if !ok {
		return make([]string, 0)
	}
	return groups
}

func IsVideoTaskModel(modelName string) bool {
	GetPricing()

	modelEnableGroupsLock.RLock()
	_, ok := videoTaskModelMap[modelName]
	if !ok {
		_, ok = videoTaskModelMap[ratio_setting.FormatMatchingModelName(modelName)]
	}
	modelEnableGroupsLock.RUnlock()
	return ok
}

func hasVideoGroup(groups []string) bool {
	for _, group := range groups {
		if strings.EqualFold(strings.TrimSpace(group), "video") {
			return true
		}
	}
	return false
}

// GetModelQuotaTypes 返回指定模型的计费类型集合（来自缓存）
func GetModelQuotaTypes(modelName string) []int {
	GetPricing()

	modelEnableGroupsLock.RLock()
	quota, ok := modelQuotaTypeMap[modelName]
	modelEnableGroupsLock.RUnlock()
	if !ok {
		return []int{}
	}
	return []int{quota}
}
