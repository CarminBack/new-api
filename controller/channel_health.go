package controller

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type channelPersistentKeyHealth struct {
	KeyIndex     int    `json:"key_index"`
	Status       int    `json:"status"`
	Reason       string `json:"reason,omitempty"`
	DisabledTime int64  `json:"disabled_time,omitempty"`
}

type channelHealthItem struct {
	ChannelID      int                                   `json:"channel_id"`
	ChannelName    string                                `json:"channel_name"`
	ChannelType    int                                   `json:"channel_type"`
	ChannelStatus  int                                   `json:"channel_status"`
	TestModel      string                                `json:"test_model,omitempty"`
	StatusReason   string                                `json:"status_reason,omitempty"`
	StatusTime     int64                                 `json:"status_time,omitempty"`
	PersistentKeys []channelPersistentKeyHealth          `json:"persistent_keys"`
	Adaptive       service.ChannelAdaptiveHealthSnapshot `json:"adaptive"`
}

type channelHealthSummary struct {
	AutoDisabledChannels int `json:"auto_disabled_channels"`
	CircuitOpenChannels  int `json:"circuit_open_channels"`
	RecoveringChannels   int `json:"recovering_channels"`
	CircuitOpenRoutes    int `json:"circuit_open_routes"`
	DegradedRoutes       int `json:"degraded_routes"`
	RecoveringRoutes     int `json:"recovering_routes"`
	SaturatedRoutes      int `json:"saturated_routes"`
	IsolatedKeys         int `json:"isolated_keys"`
}

type channelHealthRecoveryPayload struct {
	Scope       string `json:"scope"`
	ModelName   string `json:"model_name,omitempty"`
	RequestPath string `json:"request_path,omitempty"`
	KeyIndex    *int   `json:"key_index,omitempty"`
}

func channelHealthTimestamp(value interface{}) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case string:
		parsed, _ := strconv.ParseInt(typed, 10, 64)
		return parsed
	default:
		return 0
	}
}

func channelStatusMetadata(channel *model.Channel) (string, int64) {
	if channel == nil || strings.TrimSpace(channel.OtherInfo) == "" {
		return "", 0
	}
	info := channel.GetOtherInfo()
	reason, _ := info["status_reason"].(string)
	return reason, channelHealthTimestamp(info["status_time"])
}

func channelPersistentKeyHealthItems(channel *model.Channel) ([]channelPersistentKeyHealth, bool) {
	if channel == nil || !channel.ChannelInfo.IsMultiKey {
		return nil, false
	}
	items := make([]channelPersistentKeyHealth, 0, len(channel.ChannelInfo.MultiKeyStatusList))
	hasAutoDisabled := false
	for index, status := range channel.ChannelInfo.MultiKeyStatusList {
		if status == common.ChannelStatusEnabled {
			continue
		}
		item := channelPersistentKeyHealth{KeyIndex: index, Status: status}
		if channel.ChannelInfo.MultiKeyDisabledReason != nil {
			item.Reason = channel.ChannelInfo.MultiKeyDisabledReason[index]
		}
		if channel.ChannelInfo.MultiKeyDisabledTime != nil {
			item.DisabledTime = channel.ChannelInfo.MultiKeyDisabledTime[index]
		}
		items = append(items, item)
		if status == common.ChannelStatusAutoDisabled {
			hasAutoDisabled = true
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].KeyIndex < items[j].KeyIndex })
	return items, hasAutoDisabled
}

func updateChannelHealthSummary(summary *channelHealthSummary, item channelHealthItem) {
	if item.ChannelStatus == common.ChannelStatusAutoDisabled {
		summary.AutoDisabledChannels++
	}
	channelOpen := item.Adaptive.ChannelState == service.ChannelHealthStateCircuitOpen ||
		item.Adaptive.ChannelState == service.ChannelHealthStateHalfOpen
	channelRecovering := false
	for _, route := range item.Adaptive.Routes {
		switch route.State {
		case service.ChannelHealthStateCircuitOpen, service.ChannelHealthStateHalfOpen:
			summary.CircuitOpenRoutes++
			channelOpen = true
		case service.ChannelHealthStateDegraded:
			summary.DegradedRoutes++
		case service.ChannelHealthStateRecovering:
			summary.RecoveringRoutes++
			channelRecovering = true
		case service.ChannelHealthStateSaturated:
			summary.SaturatedRoutes++
		}
	}
	if channelOpen {
		summary.CircuitOpenChannels++
	} else if channelRecovering {
		summary.RecoveringChannels++
	}
	for _, key := range item.Adaptive.Keys {
		if key.State == service.ChannelHealthStateIsolated {
			summary.IsolatedKeys++
		}
	}
}

func GetChannelHealth(c *gin.Context) {
	includeHealthy, _ := strconv.ParseBool(c.Query("include_healthy"))
	adaptiveSnapshots := service.GetChannelAdaptiveHealthSnapshots(includeHealthy)
	adaptiveByChannel := make(map[int]service.ChannelAdaptiveHealthSnapshot, len(adaptiveSnapshots))
	for _, snapshot := range adaptiveSnapshots {
		adaptiveByChannel[snapshot.ChannelID] = snapshot
	}

	channels, err := model.GetChannelHealthMetadata()
	if err != nil {
		common.ApiError(c, err)
		return
	}

	items := make([]channelHealthItem, 0, len(channels))
	summary := channelHealthSummary{}
	for _, channel := range channels {
		adaptive, hasAdaptive := adaptiveByChannel[channel.Id]
		persistentKeys, hasAutoDisabledKey := channelPersistentKeyHealthItems(channel)
		isAbnormal := channel.Status == common.ChannelStatusAutoDisabled || hasAutoDisabledKey || hasAdaptive
		if !includeHealthy && !isAbnormal {
			continue
		}
		statusReason, statusTime := channelStatusMetadata(channel)
		testModel := ""
		if channel.TestModel != nil {
			testModel = *channel.TestModel
		}
		item := channelHealthItem{
			ChannelID:      channel.Id,
			ChannelName:    channel.Name,
			ChannelType:    channel.Type,
			ChannelStatus:  channel.Status,
			TestModel:      testModel,
			StatusReason:   statusReason,
			StatusTime:     statusTime,
			PersistentKeys: persistentKeys,
			Adaptive:       adaptive,
		}
		updateChannelHealthSummary(&summary, item)
		items = append(items, item)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"generated_at": time.Now().Unix(),
			"state_scope":  "process",
			"summary":      summary,
			"items":        items,
		},
	})
}

func validateChannelHealthRecoveryPayload(payload channelHealthRecoveryPayload) error {
	switch payload.Scope {
	case service.ChannelHealthRecoveryScopeChannel:
		return nil
	case service.ChannelHealthRecoveryScopeRoute:
		if strings.TrimSpace(payload.ModelName) == "" || strings.TrimSpace(payload.RequestPath) == "" {
			return errors.New("model_name and request_path are required for route recovery")
		}
		return nil
	case service.ChannelHealthRecoveryScopeKey:
		if payload.KeyIndex == nil || *payload.KeyIndex < 0 {
			return errors.New("valid key_index is required for key recovery")
		}
		return nil
	default:
		return errors.New("scope must be channel, route, or key")
	}
}

func RecoverChannelHealth(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil || channelID <= 0 {
		common.ApiError(c, errors.New("invalid channel id"))
		return
	}
	payload := channelHealthRecoveryPayload{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := validateChannelHealthRecoveryPayload(payload); err != nil {
		common.ApiError(c, err)
		return
	}
	channel, err := model.GetChannelById(channelID, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	result, err := service.RecoverChannelHealth(channel, service.ChannelHealthRecoveryRequest{
		Scope:       payload.Scope,
		ModelName:   strings.TrimSpace(payload.ModelName),
		RequestPath: strings.TrimSpace(payload.RequestPath),
		KeyIndex:    payload.KeyIndex,
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "channel.health_recover", map[string]interface{}{
		"id":            channelID,
		"scope":         payload.Scope,
		"model_name":    payload.ModelName,
		"request_path":  payload.RequestPath,
		"key_index":     payload.KeyIndex,
		"changed_items": result.ChangedItems,
	})
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": result})
}
