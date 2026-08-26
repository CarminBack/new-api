package common

import (
	"strings"
	"sync/atomic"
	"time"
)

var upstreamUsageLogDeadline atomic.Int64

func initUpstreamUsageLog(now time.Time) {
	upstreamUsageLogDeadline.Store(0)
	rawDuration := strings.TrimSpace(GetEnvOrDefaultString("UPSTREAM_USAGE_LOG_DURATION", ""))
	if rawDuration == "" {
		return
	}
	duration, err := time.ParseDuration(rawDuration)
	if err != nil || duration <= 0 {
		SysError("invalid UPSTREAM_USAGE_LOG_DURATION, upstream usage logging disabled")
		return
	}
	upstreamUsageLogDeadline.Store(now.Add(duration).UnixNano())
}

func IsUpstreamUsageLogEnabled() bool {
	return isUpstreamUsageLogEnabledAt(time.Now())
}

func isUpstreamUsageLogEnabledAt(now time.Time) bool {
	deadline := upstreamUsageLogDeadline.Load()
	return deadline > 0 && now.UnixNano() < deadline
}
