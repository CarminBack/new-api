/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/
package controller

import (
	"net/http"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type channelLatencyChannel struct {
	ChannelID      int    `json:"channel_id"`
	ChannelName    string `json:"channel_name"`
	ChannelStatus  int    `json:"channel_status"`
	ResponseTimeMs int    `json:"response_time_ms"`
	TestTime       int64  `json:"test_time"`
}

type channelLatencyGroup struct {
	Group                 string                  `json:"group"`
	ChannelCount          int                     `json:"channel_count"`
	EnabledCount          int                     `json:"enabled_count"`
	TestedCount           int                     `json:"tested_count"`
	AverageResponseTimeMs float64                 `json:"average_response_time_ms"`
	MinResponseTimeMs     int                     `json:"min_response_time_ms"`
	MaxResponseTimeMs     int                     `json:"max_response_time_ms"`
	LastTestTime          int64                   `json:"last_test_time"`
	Channels              []channelLatencyChannel `json:"channels"`
	latencyTotalMs        int64
}

// GetChannelLatency returns the latest channel probe latency grouped by the
// configured channel groups. It is intentionally read-only; probing is still
// performed by the existing channel test endpoints.
func GetChannelLatency(c *gin.Context) {
	channels, err := model.GetChannelLatencyMetadata()
	if err != nil {
		common.ApiError(c, err)
		return
	}

	groupsByName := make(map[string]*channelLatencyGroup)
	for _, channel := range channels {
		groupNames := strings.Split(channel.Group, ",")
		seenGroups := make(map[string]struct{}, len(groupNames))
		for _, rawGroup := range groupNames {
			groupName := strings.TrimSpace(rawGroup)
			if groupName == "" {
				groupName = "default"
			}
			if _, seen := seenGroups[groupName]; seen {
				continue
			}
			seenGroups[groupName] = struct{}{}

			group, ok := groupsByName[groupName]
			if !ok {
				group = &channelLatencyGroup{Group: groupName}
				groupsByName[groupName] = group
			}

			group.ChannelCount++
			if channel.Status == common.ChannelStatusEnabled {
				group.EnabledCount++
			}
			if channel.TestTime > group.LastTestTime {
				group.LastTestTime = channel.TestTime
			}
			group.Channels = append(group.Channels, channelLatencyChannel{
				ChannelID:      channel.Id,
				ChannelName:    channel.Name,
				ChannelStatus:  channel.Status,
				ResponseTimeMs: channel.ResponseTime,
				TestTime:       channel.TestTime,
			})

			if channel.TestTime > 0 && channel.ResponseTime > 0 {
				group.TestedCount++
				group.latencyTotalMs += int64(channel.ResponseTime)
				if group.MinResponseTimeMs == 0 || channel.ResponseTime < group.MinResponseTimeMs {
					group.MinResponseTimeMs = channel.ResponseTime
				}
				if channel.ResponseTime > group.MaxResponseTimeMs {
					group.MaxResponseTimeMs = channel.ResponseTime
				}
			}
		}
	}

	groups := make([]channelLatencyGroup, 0, len(groupsByName))
	for _, group := range groupsByName {
		if group.TestedCount > 0 {
			group.AverageResponseTimeMs = float64(group.latencyTotalMs) / float64(group.TestedCount)
		}
		group.latencyTotalMs = 0
		sort.SliceStable(group.Channels, func(i, j int) bool {
			left, right := group.Channels[i], group.Channels[j]
			leftTested := left.TestTime > 0 && left.ResponseTimeMs > 0
			rightTested := right.TestTime > 0 && right.ResponseTimeMs > 0
			if leftTested != rightTested {
				return leftTested
			}
			if left.ResponseTimeMs != right.ResponseTimeMs {
				return left.ResponseTimeMs < right.ResponseTimeMs
			}
			return left.ChannelID < right.ChannelID
		})
		groups = append(groups, *group)
	}
	sort.Slice(groups, func(i, j int) bool {
		return strings.ToLower(groups[i].Group) < strings.ToLower(groups[j].Group)
	})

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"generated_at": common.GetTimestamp(),
			"groups":       groups,
		},
	})
}
