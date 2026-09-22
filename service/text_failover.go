package service

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const textRetryAfterKey = "text_upstream_retry_after"
const textRecoveryCanaryKey = "text_recovery_canary"

// CaptureTextRetryAfter must run for every response so hints cannot leak across attempts.
func CaptureTextRetryAfter(c *gin.Context, header http.Header) {
	c.Set(textRetryAfterKey, time.Duration(0))
	value := strings.TrimSpace(header.Get("Retry-After"))
	var delay time.Duration
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		delay = time.Duration(min(max(seconds, 0), 120)) * time.Second
	} else if until, err := http.ParseTime(value); err == nil {
		delay = until.Sub(channelCircuitNow())
	}
	if delay > 0 {
		c.Set(textRetryAfterKey, min(delay, 2*time.Minute))
	}
}

// IsGeminiTextPath recognizes generation actions only, never media/task submission.
func IsGeminiTextPath(path string) bool {
	return (strings.HasPrefix(path, "/v1beta/models/") || strings.HasPrefix(path, "/v1/models/")) &&
		(strings.HasSuffix(path, ":generateContent") || strings.HasSuffix(path, ":streamGenerateContent"))
}

// Host is only a best-effort fault-domain hint. Distinct hosts may share an account pool.
func channelFaultDomain(channel *model.Channel) string {
	if channel == nil {
		return ""
	}
	parsed, err := url.Parse(channel.GetBaseURL())
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

// ReserveRecoveryCanary lets bound sessions contribute recovery samples without
// changing their affinity. At most one in ten opportunities, one per second,
// and the route's normal capacity reservation all have to permit the request.
func reserveRecoveryCanary(c *gin.Context, channel *model.Channel, modelName, path string) bool {
	if !IsChannelHealthAvailable(channel, modelName, path) {
		return false
	}
	now := channelCircuitNow()
	identity := buildChannelHealthIdentity(channel, 0, modelName, path, now)
	shard := channelHealthShardFor(identity.RouteKey)
	shard.Lock()
	state := getRouteHealthStateLocked(shard, identity, now)
	if state.Suspect || (state.RecoveryTargetCapacity == 0 && !now.Before(state.AffinityReadyAt)) {
		shard.Unlock()
		return false
	}
	state.AffinityTrials++
	allowed := state.AffinityTrials%10 == 1 && !now.Before(state.TrialNextAt)
	if allowed {
		state.TrialNextAt = now.Add(time.Second)
	}
	shard.Unlock()
	if !allowed || !EnsureChannelHealthReservation(c, channel, modelName, path) {
		return false
	}
	c.Set(textRecoveryCanaryKey, true)
	RequestPolicy(c).AddEvent(PolicyEvent{ChannelID: channel.Id, Decision: PolicyDecision{Action: "select", Reason: "recovery_canary", Source: "channel_health"}})
	return true
}
