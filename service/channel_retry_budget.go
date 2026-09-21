package service

import (
	"math"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	channelRetryBudgetWindow       = 2 * time.Minute
	channelRetryBudgetBucket       = 5 * time.Second
	channelRetryBudgetBucketCount  = int(channelRetryBudgetWindow / channelRetryBudgetBucket)
	channelRetryBudgetRatio        = 0.20
	channelRetryEmergencyRatio     = 0.05
	channelRetryBudgetMinimumBurst = 10
	ginKeyChannelPrimaryRecorded   = "channel_retry_primary_recorded"
)

type channelRetryBudgetBucketState struct {
	Epoch            int64
	Primary          int
	Retries          int
	EmergencyRetries int
	Routes           map[string]channelRetryBudgetRouteState
}

type channelRetryBudgetRouteState struct {
	Primary          int
	Retries          int
	EmergencyRetries int
	RetriesByClass   map[string]int
	EmergencyByClass map[string]int
}

var channelRetryBudgetState struct {
	sync.Mutex
	Buckets [channelRetryBudgetBucketCount]channelRetryBudgetBucketState
}

func retryBudgetEpoch(now time.Time) int64 {
	return now.UnixNano() / channelRetryBudgetBucket.Nanoseconds()
}

func currentRetryBudgetBucketLocked(now time.Time) *channelRetryBudgetBucketState {
	epoch := retryBudgetEpoch(now)
	index := int(epoch % int64(channelRetryBudgetBucketCount))
	bucket := &channelRetryBudgetState.Buckets[index]
	if bucket.Epoch != epoch {
		*bucket = channelRetryBudgetBucketState{Epoch: epoch, Routes: make(map[string]channelRetryBudgetRouteState)}
	}
	if bucket.Routes == nil {
		bucket.Routes = make(map[string]channelRetryBudgetRouteState)
	}
	return bucket
}

func summarizeRetryBudgetLocked(now time.Time) (primary int, retries int, emergencyRetries int, routes map[string]channelRetryBudgetRouteState) {
	currentEpoch := retryBudgetEpoch(now)
	routes = make(map[string]channelRetryBudgetRouteState)
	for _, bucket := range channelRetryBudgetState.Buckets {
		if bucket.Epoch <= 0 || currentEpoch-bucket.Epoch < 0 || currentEpoch-bucket.Epoch >= int64(channelRetryBudgetBucketCount) {
			continue
		}
		primary += bucket.Primary
		retries += bucket.Retries
		emergencyRetries += bucket.EmergencyRetries
		for key, state := range bucket.Routes {
			total := routes[key]
			total.Primary += state.Primary
			total.Retries += state.Retries
			total.EmergencyRetries += state.EmergencyRetries
			if len(state.RetriesByClass) > 0 {
				if total.RetriesByClass == nil {
					total.RetriesByClass = make(map[string]int)
				}
				for class, count := range state.RetriesByClass {
					total.RetriesByClass[class] += count
				}
			}
			if len(state.EmergencyByClass) > 0 {
				if total.EmergencyByClass == nil {
					total.EmergencyByClass = make(map[string]int)
				}
				for class, count := range state.EmergencyByClass {
					total.EmergencyByClass[class] += count
				}
			}
			routes[key] = total
		}
	}
	return primary, retries, emergencyRetries, routes
}

func RecordChannelPrimaryRequest(c *gin.Context) {
	modelName := ""
	requestPath := ""
	if c != nil {
		modelName = c.GetString("original_model")
		if c.Request != nil && c.Request.URL != nil {
			requestPath = c.Request.URL.Path
		}
	}
	RecordChannelPrimaryRequestFor(c, modelName, requestPath)
}

func RecordChannelPrimaryRequestFor(c *gin.Context, modelName string, requestPath string) {
	if c != nil {
		if recorded, ok := c.Get(ginKeyChannelPrimaryRecorded); ok && recorded == true {
			return
		}
		c.Set(ginKeyChannelPrimaryRecorded, true)
	}
	now := channelCircuitNow()
	channelRetryBudgetState.Lock()
	bucket := currentRetryBudgetBucketLocked(now)
	bucket.Primary++
	route := normalizeRetryBudgetRoute(modelName, requestPath)
	state := bucket.Routes[route]
	state.Primary++
	bucket.Routes[route] = state
	channelRetryBudgetState.Unlock()
}

func AllowChannelRetry() bool {
	return allowChannelRetry("", "", ChannelFailureTransient)
}

// AllowChannelRetryFor reserves a retry from both a model/path budget and the
// process-wide guard. The emergency reserve is used only after normal budget
// is exhausted, preventing one noisy route from consuming all retry capacity.
func AllowChannelRetryFor(_ *gin.Context, modelName string, requestPath string, class ChannelFailureClass, _ int) bool {
	return allowChannelRetry(modelName, requestPath, class)
}

func allowChannelRetry(modelName string, requestPath string, class ChannelFailureClass) bool {
	now := channelCircuitNow()
	channelRetryBudgetState.Lock()
	defer channelRetryBudgetState.Unlock()
	primary, retries, emergencyRetries, routes := summarizeRetryBudgetLocked(now)
	routeKey := normalizeRetryBudgetRoute(modelName, requestPath)
	route := routes[routeKey]
	classKey := retryBudgetClassKey(class)
	normalRouteAllowance := retryBudgetAllowance(route.Primary, channelRetryBudgetRatio, channelRetryBudgetMinimumBurst)
	normalGlobalAllowance := retryBudgetAllowance(primary, channelRetryBudgetRatio, channelRetryBudgetMinimumBurst)
	if retries < normalGlobalAllowance && route.RetriesByClass[classKey] < normalRouteAllowance {
		bucket := currentRetryBudgetBucketLocked(now)
		bucket.Retries++
		state := bucket.Routes[routeKey]
		state.Retries++
		if state.RetriesByClass == nil {
			state.RetriesByClass = make(map[string]int)
		}
		state.RetriesByClass[classKey]++
		bucket.Routes[routeKey] = state
		return true
	}
	// Keep a small reserve for otherwise-valid requests during a localized
	// outage. It is bounded globally and per route, so it cannot become an
	// unbounded retry multiplier.
	emergencyRouteAllowance := retryBudgetAllowance(route.Primary, channelRetryEmergencyRatio, 2)
	emergencyGlobalAllowance := retryBudgetAllowance(primary, channelRetryEmergencyRatio, 5)
	if emergencyRetries >= emergencyGlobalAllowance || route.EmergencyByClass[classKey] >= emergencyRouteAllowance {
		return false
	}
	if class == ChannelFailureTerminal || class == ChannelFailureChannelFatal {
		return false
	}
	bucket := currentRetryBudgetBucketLocked(now)
	bucket.EmergencyRetries++
	state := bucket.Routes[routeKey]
	state.EmergencyRetries++
	if state.EmergencyByClass == nil {
		state.EmergencyByClass = make(map[string]int)
	}
	state.EmergencyByClass[classKey]++
	bucket.Routes[routeKey] = state
	return true
}

func retryBudgetAllowance(primary int, ratio float64, minimum int) int {
	allowance := int(math.Ceil(float64(primary) * ratio))
	if allowance < minimum {
		allowance = minimum
	}
	return allowance
}

func normalizeRetryBudgetRoute(modelName string, requestPath string) string {
	modelName = strings.TrimSpace(modelName)
	requestPath = strings.TrimSpace(requestPath)
	if modelName == "" {
		modelName = "_unknown_model"
	}
	if requestPath == "" {
		requestPath = "_unknown_path"
	}
	return modelName + "\x00" + requestPath
}

func retryBudgetClassKey(class ChannelFailureClass) string {
	switch class {
	case ChannelFailureRateLimited:
		return "rate_limited"
	case ChannelFailureUncertain:
		return "uncertain"
	case ChannelFailureTransient:
		return "transient"
	default:
		return "other"
	}
}

func resetChannelRetryBudgetForTest() {
	channelRetryBudgetState.Lock()
	channelRetryBudgetState.Buckets = [channelRetryBudgetBucketCount]channelRetryBudgetBucketState{}
	channelRetryBudgetState.Unlock()
}
