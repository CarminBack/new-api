package service

import (
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

type textLatencyKey struct {
	channel     int
	model, path string
}
type textLatencySample struct {
	count   int
	ewma    float64
	updated time.Time
}

var textChannelLatency = struct {
	sync.Mutex
	samples map[textLatencyKey]textLatencySample
}{samples: make(map[textLatencyKey]textLatencySample)}

// RecordTextChannelLatency uses the successful attempt's first meaningful
// output, not end-to-end FRT (which includes failed attempts).
func RecordTextChannelLatency(c *gin.Context, info *relaycommon.RelayInfo, channelID int) {
	if !IsTextRelayRequest(c) || c.Request.Context().Err() != nil || info == nil || info.ChannelMeta == nil || !info.IsStream || info.IsChannelTest || len(info.UpstreamTimings) == 0 {
		return
	}
	if info.StreamStatus != nil && (!info.StreamStatus.IsNormalEnd() || info.StreamStatus.HasErrors()) {
		return
	}
	sample, ok := info.UpstreamTimings[len(info.UpstreamTimings)-1].Snapshot()["first_meaningful_data_ms"].(int64)
	if !ok || sample < 0 {
		return
	}
	channel, err := model.CacheGetChannel(channelID)
	if err != nil || channel == nil || channelInImageGroup(channel) {
		return
	}
	recordTextLatencySample(textLatencyKey{channelID, info.OriginModelName, c.Request.URL.Path}, sample)
}

func recordTextLatencySample(key textLatencyKey, sample int64) {
	now := channelCircuitNow()
	textChannelLatency.Lock()
	defer textChannelLatency.Unlock()
	for k, value := range textChannelLatency.samples {
		if now.Sub(value.updated) >= 10*time.Minute {
			delete(textChannelLatency.samples, k)
		}
	}
	current := textChannelLatency.samples[key]
	if current.count == 0 {
		if len(textChannelLatency.samples) >= 4096 {
			return
		}
		current.ewma = float64(sample)
	} else {
		current.ewma = current.ewma*0.8 + float64(sample)*0.2
	}
	current.count = min(1000, current.count+1)
	current.updated = now
	textChannelLatency.samples[key] = current
}

// Return a snapshot callback: no model/cache locks are acquired during the
// weighted draw, and sampling cannot change weights halfway through it.
func textChannelWeightFactor(param *RetryParam) func(int) float64 {
	if !common.TextAdaptiveRoutingEnabled || !IsTextRelayRequest(param.Ctx) {
		return nil
	}
	now := channelCircuitNow()
	factors := make(map[int]float64)
	textChannelLatency.Lock()
	for key, sample := range textChannelLatency.samples {
		if key.model != param.ModelName || key.path != param.RequestPath || sample.count < 8 || now.Sub(sample.updated) >= 10*time.Minute {
			continue
		}
		threshold := float64(common.TextSlowFirstContentSeconds) * 1000
		if threshold > 0 && sample.ewma > threshold {
			factors[key.channel] = max(0.25, threshold/sample.ewma)
		}
	}
	textChannelLatency.Unlock()
	return func(channelID int) float64 {
		if factor, exists := factors[channelID]; exists {
			return factor
		}
		return 1
	}
}
