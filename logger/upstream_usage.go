package logger

import (
	"context"
	"fmt"

	"github.com/QuantumNous/new-api/common"

	"github.com/tidwall/gjson"
)

var upstreamUsagePaths = []string{
	"response.usage",
	"usage",
	"message.usage",
	"usageMetadata",
	"usage_metadata",
}

type upstreamUsageEntry struct {
	path string
	raw  string
}

func extractUpstreamUsage(payload []byte) []upstreamUsageEntry {
	entries := make([]upstreamUsageEntry, 0, 1)
	for _, path := range upstreamUsagePaths {
		usage := gjson.GetBytes(payload, path)
		if usage.Exists() && usage.IsObject() {
			entries = append(entries, upstreamUsageEntry{path: path, raw: usage.Raw})
		}
	}
	return entries
}

func LogUpstreamUsage(ctx context.Context, channelID int, model string, payload []byte) {
	if !common.IsUpstreamUsageLogEnabled() || len(payload) == 0 {
		return
	}
	for _, entry := range extractUpstreamUsage(payload) {
		LogInfo(ctx, fmt.Sprintf("upstream usage: channel_id=%d model=%s path=%s usage=%s", channelID, model, entry.path, entry.raw))
	}
}
