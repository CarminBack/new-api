package model

import (
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
)

var filterEvalOrder = []dto.ChannelFilterKind{
	dto.FilterExcludedChannels,
	dto.FilterRequestPath,
	dto.FilterImageResolution,
	dto.FilterTaskPluginIdentity,
	dto.FilterResponsesWebSocket,
}

// ChannelSatisfiesFilters reports whether ch passes every filter.
// On false, it returns the kind of the first violated filter (request_path
// then task_plugin_identity) for error attribution.
func ChannelSatisfiesFilters(ch *Channel, modelName string, filters []dto.ChannelFilter) (bool, dto.ChannelFilterKind) {
	if ch == nil {
		return false, ""
	}
	for _, kind := range filterEvalOrder {
		for _, filter := range filters {
			if filter.Kind != kind {
				continue
			}
			if !channelMatchesFilter(ch, modelName, filter) {
				return false, kind
			}
		}
	}
	return true, ""
}

// filterCandidateIDs applies filters to a cached candidate id list.
// Caller must hold channelSyncLock (read lock). The input slice is never mutated.
// A missing id in channelsIDM is kept for request_path (downstream consistency
// error) and dropped for task_plugin_identity, matching the previous filters.
func filterCandidateIDs(ids []int, modelName string, filters []dto.ChannelFilter) (kept []int, emptiedBy dto.ChannelFilterKind) {
	if len(ids) == 0 {
		return ids, ""
	}
	kept = ids
	for _, kind := range filterEvalOrder {
		kindFilters := filtersByKind(filters, kind)
		if len(kindFilters) == 0 {
			continue
		}
		if kind == dto.FilterImageResolution {
			kept = filterCandidateIDsByImageResolution(kept, modelName, kindFilters)
			if len(kept) == 0 {
				return kept, kind
			}
			continue
		}
		next := make([]int, 0, len(kept))
		for _, id := range kept {
			channel, exists := channelsIDM[id]
			if candidatePassesKindFilters(channel, exists, modelName, kind, kindFilters) {
				next = append(next, id)
			}
		}
		if len(kept) > 0 && len(next) == 0 {
			return next, kind
		}
		kept = next
	}
	return kept, ""
}

func filterCandidateIDsByImageResolution(ids []int, modelName string, filters []dto.ChannelFilter) []int {
	if len(ids) == 0 || len(filters) == 0 {
		return ids
	}
	tier := filters[0].ImageResolutionTier
	if tier == "" {
		return ids
	}
	declared := false
	for _, id := range ids {
		if channel, ok := channelsIDM[id]; ok && channel != nil {
			_, hasDeclaration := channel.GetSetting().ImageResolutionTierSupport(modelName, tier)
			declared = declared || hasDeclaration
		}
	}
	if !declared {
		return ids
	}
	filtered := make([]int, 0, len(ids))
	for _, id := range ids {
		if channel, ok := channelsIDM[id]; ok && channel != nil {
			if supported, hasDeclaration := channel.GetSetting().ImageResolutionTierSupport(modelName, tier); hasDeclaration && supported {
				filtered = append(filtered, id)
			}
		}
	}
	return filtered
}

func filtersByKind(filters []dto.ChannelFilter, kind dto.ChannelFilterKind) []dto.ChannelFilter {
	var matched []dto.ChannelFilter
	for _, filter := range filters {
		if filter.Kind == kind {
			matched = append(matched, filter)
		}
	}
	return matched
}

func candidatePassesKindFilters(ch *Channel, exists bool, modelName string, kind dto.ChannelFilterKind, filters []dto.ChannelFilter) bool {
	if kind == dto.FilterRequestPath && !exists {
		return true
	}
	if !exists || ch == nil {
		return false
	}
	for _, filter := range filters {
		if !channelMatchesFilter(ch, modelName, filter) {
			return false
		}
	}
	return true
}

func channelMatchesFilter(ch *Channel, modelName string, filter dto.ChannelFilter) bool {
	switch filter.Kind {
	case dto.FilterExcludedChannels:
		_, excluded := filter.ExcludedChannelIDs[ch.Id]
		return !excluded
	case dto.FilterRequestPath:
		if filter.RequestPath == "" {
			return true
		}
		if !constant.IsAdvancedCustomChannel(ch.Type) {
			return true
		}
		config := ch.GetOtherSettings().AdvancedCustom
		return config != nil && config.SupportsPathForModel(filter.RequestPath, modelName)
	case dto.FilterImageResolution:
		// Undeclared channels remain eligible here. Candidate-pool filtering
		// activates this constraint only when at least one candidate declares
		// capabilities, preserving legacy routing for unconfigured groups.
		if tier := strings.TrimSpace(filter.ImageResolutionTier); tier != "" {
			if supported, declared := ch.GetSetting().ImageResolutionTierSupport(modelName, tier); declared {
				return supported
			}
		}
		return true
	case dto.FilterTaskPluginIdentity:
		if ch.Type == constant.ChannelTypeTaskPlugin {
			key := ch.GetSetting().TaskPluginKey
			return filter.TaskPluginKey != "" && (key == filter.TaskPluginKey || slices.Contains(filter.TaskPluginKeys, key))
		}
		return filter.TaskPluginKey == "" || slices.Contains(filter.TaskPluginChannelTypes, ch.Type)
	case dto.FilterResponsesWebSocket:
		if !ch.GetSetting().ResponsesWebSocketEnabled {
			return false
		}
		switch ch.Type {
		case constant.ChannelTypeOpenAI, constant.ChannelTypeCodex, constant.ChannelTypeSub2API, constant.ChannelTypeNewAPI:
			return true
		case constant.ChannelTypeAdvancedCustom:
			// The session forwards native Responses events without protocol
			// conversion, so only a converter-free /v1/responses route qualifies.
			route, ok := ch.GetOtherSettings().AdvancedCustom.MatchPathForModel("/v1/responses", modelName)
			return ok && route.IsNative()
		default:
			return false
		}
	default:
		return true
	}
}
