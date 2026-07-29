package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const (
	channelHealthWindow                  = 2 * time.Minute
	channelHealthBucketDuration          = 5 * time.Second
	channelHealthBucketCount             = int(channelHealthWindow / channelHealthBucketDuration)
	channelHealthOpenFor                 = 2 * time.Minute
	channelHealthStateTTL                = 30 * time.Minute
	channelHealthShardCount              = 32
	channelHealthEstablishedCapacity     = 128
	channelHealthNewCapacity             = 16
	channelHealthMinCapacity             = 1
	channelHealthMaxCapacity             = 512
	channelHealthNewChannelAge           = 10 * time.Minute
	channelHealthKeyFatalOpenFor         = 10 * time.Minute
	channelHealthSuspectWindow           = 30 * time.Second
	channelHealthSuspectMinimumSamples   = 20
	channelHealthSuspectFailureRate      = 0.90
	channelHealthSuspectMinimumDuration  = 10 * time.Second
	channelHealthSlowSingleRouteFailures = 5
	channelHealthSlowMultiRouteFailures  = 3
	channelHealthSlowMultiRouteCount     = 2
	channelHealthAggregateMinimumSamples = 50
	channelHealthAggregateFailureRate    = 0.80
	channelHealthLatencyMinimumSamples   = 20
	channelHealthLatencyTimeoutRate      = 0.50
	channelHealthLatencyMaxSuccessRate   = 0.30
	channelHealthRateLimitConfirmFor     = 2 * time.Minute
	channelHealthRecoverySuccessTarget   = 0
	channelHealthProbeLease              = 20 * time.Second
	channelHealthImageProbeLease         = 75 * time.Second

	ginKeyChannelHealthReservation = "channel_health_reservation"
)

var channelCircuitNow = time.Now
var channelHealthProbeSequence atomic.Uint64

type ChannelHealthProbeScope string

const (
	ChannelHealthProbeScopeRoute   ChannelHealthProbeScope = "route"
	ChannelHealthProbeScopeChannel ChannelHealthProbeScope = "channel"
)

type ChannelHealthProbeType string

const (
	ChannelHealthProbeTypeInitial  ChannelHealthProbeType = "initial"
	ChannelHealthProbeTypeRecovery ChannelHealthProbeType = "recovery"
)

type channelHealthBucket struct {
	Epoch        int64
	Successes    int
	Failures     int
	Timeouts     int
	PoolFailures int
	RateLimits   int
}

type channelRouteHealthState struct {
	Buckets                [channelHealthBucketCount]channelHealthBucket
	ChannelID              int
	Fingerprint            string
	RouteLabel             string
	InitialCapacity        int
	OpenUntil              time.Time
	ProbeInFlight          bool
	ProbeGeneration        uint64
	ProbeID                uint64
	ProbeType              ChannelHealthProbeType
	ProbeTriggerClass      ChannelFailureClass
	ProbeLeaseUntil        time.Time
	InFlight               int
	Capacity               int
	SuccessesSinceIncrease int
	Suspect                bool
	ProbeDue               time.Time
	RecoveryTargetCapacity int
	RecoverySuccesses      int
	RecoveryFailures       int
	CapacityBeforeOpen     int
	RateLimitSince         time.Time
	LastFailureClass       ChannelFailureClass
	LastFailureReason      string
	LastFailureStatusCode  int
	LastFailureAt          time.Time
	LastSuccessAt          time.Time
	BurstFailureStartedAt  time.Time
	NoSuccessFailureAt     time.Time
	FailuresSinceSuccess   int
	LastRecoveryAt         time.Time
	LastTouched            time.Time
}

type channelAggregateHealthState struct {
	Buckets                  [channelHealthBucketCount]channelHealthBucket
	ChannelID                int
	Fingerprint              string
	OpenUntil                time.Time
	Suspect                  bool
	ProbeDue                 time.Time
	ProbeInFlight            bool
	ProbeRevision            uint64
	ProbeID                  uint64
	ProbeType                ChannelHealthProbeType
	ProbeTriggerClass        ChannelFailureClass
	ProbeScope               ChannelHealthProbeScope
	ProbeRouteLabel          string
	ProbeRouteKey            string
	ProbeLeaseUntil          time.Time
	NoSuccessFailureAt       time.Time
	FailuresSinceSuccess     int
	FailedRoutesSinceSuccess map[string]struct{}
	RecentFailureRoutes      map[string]time.Time
	UnhealthyRoutes          map[string]time.Time
	LastFailureReason        string
	LastFailureStatusCode    int
	LastFailureAt            time.Time
	LastSuccessAt            time.Time
	LastTouched              time.Time
}

type channelKeyHealthState struct {
	ChannelID              int
	Fingerprint            string
	RouteLabel             string
	KeyIndex               int
	Scope                  string
	OpenUntil              time.Time
	InFlight               int
	Capacity               int
	SuccessesSinceIncrease int
	LastTouched            time.Time
}

type channelRouteHealthShard struct {
	sync.Mutex
	Routes      map[string]*channelRouteHealthState
	LastCleanup time.Time
}

type channelAggregateHealthShard struct {
	sync.Mutex
	States      map[string]*channelAggregateHealthState
	LastCleanup time.Time
}

type channelHealthIdentity struct {
	ChannelID       int
	Fingerprint     string
	ChannelKey      string
	RouteKey        string
	RouteLabel      string
	InitialCapacity int
	Keys            []string
}

type channelHealthReservation struct {
	Identity          channelHealthIdentity
	SelectedKeyHealth string
}

type observedChannelConfig struct {
	Fingerprint     string
	InitialCapacity int
	LastTouched     time.Time
}

var memoryChannelHealth struct {
	RouteShards   [channelHealthShardCount]channelRouteHealthShard
	ChannelShards [channelHealthShardCount]channelAggregateHealthShard
	Keys          struct {
		sync.RWMutex
		States      map[string]channelKeyHealthState
		LastCleanup time.Time
	}
	Configs struct {
		sync.RWMutex
		Observed    map[int]observedChannelConfig
		LastCleanup time.Time
	}
}

func init() {
	resetMemoryChannelHealth()
}

func resetMemoryChannelHealth() {
	for i := range memoryChannelHealth.RouteShards {
		shard := &memoryChannelHealth.RouteShards[i]
		shard.Lock()
		shard.Routes = make(map[string]*channelRouteHealthState)
		shard.LastCleanup = time.Time{}
		shard.Unlock()
	}
	for i := range memoryChannelHealth.ChannelShards {
		shard := &memoryChannelHealth.ChannelShards[i]
		shard.Lock()
		shard.States = make(map[string]*channelAggregateHealthState)
		shard.LastCleanup = time.Time{}
		shard.Unlock()
	}
	memoryChannelHealth.Keys.Lock()
	memoryChannelHealth.Keys.States = make(map[string]channelKeyHealthState)
	memoryChannelHealth.Keys.LastCleanup = time.Time{}
	memoryChannelHealth.Keys.Unlock()
	memoryChannelHealth.Configs.Lock()
	memoryChannelHealth.Configs.Observed = make(map[int]observedChannelConfig)
	memoryChannelHealth.Configs.LastCleanup = time.Time{}
	memoryChannelHealth.Configs.Unlock()
}

func shortChannelHealthHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

func channelConfigFingerprint(channel *model.Channel) string {
	if channel == nil {
		return "legacy"
	}
	baseURL := ""
	if channel.BaseURL != nil {
		baseURL = *channel.BaseURL
	}
	parts := []string{
		strconv.Itoa(channel.Type),
		baseURL,
		channel.Key,
		channel.Models,
		channel.GetModelMapping(),
		channel.Other,
		channel.OtherSettings,
	}
	for _, value := range []*string{
		channel.OpenAIOrganization,
		channel.TestModel,
		channel.Setting,
		channel.ParamOverride,
		channel.HeaderOverride,
		channel.StatusCodeMapping,
	} {
		if value == nil {
			parts = append(parts, "")
		} else {
			parts = append(parts, *value)
		}
	}
	return shortChannelHealthHash(strings.Join(parts, "\x00"))
}

func initialChannelHealthCapacity(channel *model.Channel, fingerprint string, now time.Time) int {
	if channel == nil {
		return channelHealthEstablishedCapacity
	}
	memoryChannelHealth.Configs.RLock()
	observed, exists := memoryChannelHealth.Configs.Observed[channel.Id]
	maintenanceDue := memoryChannelHealth.Configs.LastCleanup.IsZero() || now.Sub(memoryChannelHealth.Configs.LastCleanup) >= time.Minute
	touchDue := exists && now.Sub(observed.LastTouched) >= time.Minute
	if exists && observed.Fingerprint == fingerprint && !maintenanceDue && !touchDue {
		memoryChannelHealth.Configs.RUnlock()
		return observed.InitialCapacity
	}
	memoryChannelHealth.Configs.RUnlock()
	memoryChannelHealth.Configs.Lock()
	defer memoryChannelHealth.Configs.Unlock()
	if memoryChannelHealth.Configs.LastCleanup.IsZero() || now.Sub(memoryChannelHealth.Configs.LastCleanup) >= time.Minute {
		for channelID, candidate := range memoryChannelHealth.Configs.Observed {
			if now.Sub(candidate.LastTouched) >= channelHealthStateTTL {
				delete(memoryChannelHealth.Configs.Observed, channelID)
			}
		}
		memoryChannelHealth.Configs.LastCleanup = now
	}
	observed, exists = memoryChannelHealth.Configs.Observed[channel.Id]
	if exists && observed.Fingerprint == fingerprint {
		observed.LastTouched = now
		memoryChannelHealth.Configs.Observed[channel.Id] = observed
		return observed.InitialCapacity
	}
	if exists && observed.Fingerprint != fingerprint {
		memoryChannelHealth.Configs.Observed[channel.Id] = observedChannelConfig{Fingerprint: fingerprint, InitialCapacity: channelHealthNewCapacity, LastTouched: now}
		return channelHealthNewCapacity
	}
	if channel.CreatedTime > 0 && now.Sub(time.Unix(channel.CreatedTime, 0)) < channelHealthNewChannelAge {
		memoryChannelHealth.Configs.Observed[channel.Id] = observedChannelConfig{Fingerprint: fingerprint, InitialCapacity: channelHealthNewCapacity, LastTouched: now}
		return channelHealthNewCapacity
	}
	memoryChannelHealth.Configs.Observed[channel.Id] = observedChannelConfig{Fingerprint: fingerprint, InitialCapacity: channelHealthEstablishedCapacity, LastTouched: now}
	return channelHealthEstablishedCapacity
}

func buildChannelHealthIdentity(channel *model.Channel, channelID int, modelName string, requestPath string, now time.Time) channelHealthIdentity {
	fingerprint := channelConfigFingerprint(channel)
	if channel != nil {
		channelID = channel.Id
	}
	channelKey := fmt.Sprintf("%d:%s", channelID, fingerprint)
	routeLabel := modelName + "\x00" + requestPath
	identity := channelHealthIdentity{
		ChannelID:       channelID,
		Fingerprint:     fingerprint,
		ChannelKey:      channelKey + ":all",
		RouteKey:        channelKey + ":route:" + shortChannelHealthHash(routeLabel),
		RouteLabel:      routeLabel,
		InitialCapacity: initialChannelHealthCapacity(channel, fingerprint, now),
	}
	if channel != nil {
		if channel.ChannelInfo.IsMultiKey {
			identity.Keys = channel.GetKeys()
		} else if channel.Key != "" {
			identity.Keys = []string{channel.Key}
		}
	}
	return identity
}

func channelHealthShardFor(key string) *channelRouteHealthShard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return &memoryChannelHealth.RouteShards[int(h.Sum32())%channelHealthShardCount]
}

func channelAggregateHealthShardFor(key string) *channelAggregateHealthShard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return &memoryChannelHealth.ChannelShards[int(h.Sum32())%channelHealthShardCount]
}

func cleanupRouteHealthShardLocked(shard *channelRouteHealthShard, now time.Time) {
	if !shard.LastCleanup.IsZero() && now.Sub(shard.LastCleanup) < time.Minute {
		return
	}
	for key, state := range shard.Routes {
		if state.InFlight == 0 && !state.ProbeInFlight && !state.Suspect && state.OpenUntil.IsZero() &&
			now.Sub(state.LastTouched) >= channelHealthStateTTL {
			delete(shard.Routes, key)
		}
	}
	shard.LastCleanup = now
}

func getRouteHealthStateLocked(shard *channelRouteHealthShard, identity channelHealthIdentity, now time.Time) *channelRouteHealthState {
	cleanupRouteHealthShardLocked(shard, now)
	state := shard.Routes[identity.RouteKey]
	if state == nil {
		capacity := identity.InitialCapacity
		if capacity < channelHealthMinCapacity {
			capacity = channelHealthMinCapacity
		}
		state = &channelRouteHealthState{Capacity: capacity}
		shard.Routes[identity.RouteKey] = state
	}
	state.ChannelID = identity.ChannelID
	state.Fingerprint = identity.Fingerprint
	state.RouteLabel = identity.RouteLabel
	state.InitialCapacity = identity.InitialCapacity
	state.LastTouched = now
	return state
}

func healthBucketEpoch(now time.Time) int64 {
	return now.UnixNano() / channelHealthBucketDuration.Nanoseconds()
}

func currentRouteHealthBucketLocked(state *channelRouteHealthState, now time.Time) *channelHealthBucket {
	epoch := healthBucketEpoch(now)
	index := int(epoch % int64(channelHealthBucketCount))
	bucket := &state.Buckets[index]
	if bucket.Epoch != epoch {
		*bucket = channelHealthBucket{Epoch: epoch}
	}
	return bucket
}

func recordRouteHealthSuccessLocked(state *channelRouteHealthState, now time.Time) {
	currentRouteHealthBucketLocked(state, now).Successes++
}

func recordRouteHealthFailureLocked(state *channelRouteHealthState, now time.Time, class ChannelFailureClass) {
	bucket := currentRouteHealthBucketLocked(state, now)
	switch class {
	case ChannelFailureRateLimited:
		bucket.RateLimits++
	case ChannelFailureKeyCapability, ChannelFailurePoolAccount:
		bucket.PoolFailures++
	default:
		bucket.Failures++
	}
}

func summarizeRouteHealthLocked(state *channelRouteHealthState, now time.Time) (successes int, failures int, poolFailures int, rateLimits int) {
	currentEpoch := healthBucketEpoch(now)
	for i := range state.Buckets {
		bucket := state.Buckets[i]
		if bucket.Epoch <= 0 || currentEpoch-bucket.Epoch < 0 || currentEpoch-bucket.Epoch >= int64(channelHealthBucketCount) {
			continue
		}
		successes += bucket.Successes
		failures += bucket.Failures
		poolFailures += bucket.PoolFailures
		rateLimits += bucket.RateLimits
	}
	return
}

func summarizeRecentRouteHealthLocked(state *channelRouteHealthState, now time.Time, window time.Duration) (successes int, failures int, rateLimits int) {
	currentEpoch := healthBucketEpoch(now)
	bucketCount := int(window / channelHealthBucketDuration)
	if window%channelHealthBucketDuration != 0 {
		bucketCount++
	}
	for i := range state.Buckets {
		bucket := state.Buckets[i]
		if bucket.Epoch <= 0 || currentEpoch-bucket.Epoch < 0 || currentEpoch-bucket.Epoch >= int64(bucketCount) {
			continue
		}
		successes += bucket.Successes
		failures += bucket.Failures
		rateLimits += bucket.RateLimits
	}
	return
}

func isInfrastructureFailureClass(class ChannelFailureClass) bool {
	return class == ChannelFailureTransient || class == ChannelFailureUncertain || class == ChannelFailureChannelFatal
}

func isProbeEligibleFailureClass(class ChannelFailureClass) bool {
	return isInfrastructureFailureClass(class) || class == ChannelFailurePoolAccount
}

func isConclusiveProbeFailure(target ChannelHealthProbeTarget, class ChannelFailureClass) bool {
	switch class {
	case ChannelFailureTransient, ChannelFailureUncertain, ChannelFailureChannelFatal:
		return true
	case ChannelFailurePoolAccount:
		return target.wasOpen || target.triggerClass == ChannelFailurePoolAccount
	default:
		return false
	}
}

func shouldScheduleChannelProbeLocked(state *channelRouteHealthState, now time.Time, class ChannelFailureClass) bool {
	if state.Suspect || !state.OpenUntil.IsZero() || state.ProbeInFlight {
		return false
	}
	_, requestPath := splitChannelRouteLabel(state.RouteLabel)
	if !ChannelHealthProbeSupportsPath(requestPath) {
		return false
	}
	if class == ChannelFailureRateLimited {
		_, _, rateLimits := summarizeRecentRouteHealthLocked(state, now, channelHealthWindow)
		return !state.RateLimitSince.IsZero() && now.Sub(state.RateLimitSince) >= channelHealthRateLimitConfirmFor && rateLimits >= channelHealthSuspectMinimumSamples
	}
	if class == ChannelFailureChannelFatal {
		return true
	}
	if class != ChannelFailureTransient && class != ChannelFailureUncertain && class != ChannelFailurePoolAccount {
		return false
	}
	slowFailure := state.FailuresSinceSuccess >= channelHealthSlowSingleRouteFailures &&
		!state.NoSuccessFailureAt.IsZero() && now.Sub(state.NoSuccessFailureAt) >= channelHealthWindow
	if class == ChannelFailurePoolAccount {
		return slowFailure
	}
	successes, failures, _ := summarizeRecentRouteHealthLocked(state, now, channelHealthSuspectWindow)
	total := successes + failures
	burstFailure := total >= channelHealthSuspectMinimumSamples &&
		float64(failures)/float64(total) >= channelHealthSuspectFailureRate &&
		!state.BurstFailureStartedAt.IsZero() && now.Sub(state.BurstFailureStartedAt) >= channelHealthSuspectMinimumDuration
	return burstFailure || slowFailure
}

func ChannelHealthProbeSupportsPath(requestPath string) bool {
	switch requestPath {
	case "/v1/chat/completions", "/v1/completions", "/v1/responses", "/v1/responses/compact",
		"/v1/messages", "/v1/embeddings", "/v1/rerank", "/rerank", "/v1/images/generations":
		return true
	default:
		return false
	}
}

func channelHealthProbeLeaseForPath(requestPath string) time.Duration {
	if requestPath == "/v1/images/generations" {
		return channelHealthImageProbeLease
	}
	return channelHealthProbeLease
}

func startRouteRecoveryLocked(state *channelRouteHealthState, now time.Time) {
	target := state.CapacityBeforeOpen
	if target <= 0 {
		target = state.Capacity
	}
	if target < state.InitialCapacity {
		target = state.InitialCapacity
	}
	state.Buckets = [channelHealthBucketCount]channelHealthBucket{}
	state.OpenUntil = time.Time{}
	state.ProbeDue = time.Time{}
	state.ProbeInFlight = false
	state.ProbeID = 0
	state.ProbeType = ""
	state.ProbeTriggerClass = ""
	state.ProbeLeaseUntil = time.Time{}
	state.Suspect = false
	state.RateLimitSince = time.Time{}
	state.BurstFailureStartedAt = time.Time{}
	state.NoSuccessFailureAt = time.Time{}
	state.FailuresSinceSuccess = 0
	state.CapacityBeforeOpen = target
	state.RecoveryTargetCapacity = 0
	state.Capacity = target
	state.RecoverySuccesses = 0
	state.RecoveryFailures = 0
	state.SuccessesSinceIncrease = 0
	state.LastRecoveryAt = now
}

func increaseRouteCapacityLocked(state *channelRouteHealthState) {
	if state.Capacity >= channelHealthMaxCapacity {
		return
	}
	state.SuccessesSinceIncrease++
	threshold := state.Capacity / 8
	if threshold < 4 {
		threshold = 4
	}
	if state.SuccessesSinceIncrease >= threshold {
		state.Capacity++
		state.SuccessesSinceIncrease = 0
	}
}

func keyHealthGlobalKey(identity channelHealthIdentity, key string) string {
	return identity.ChannelKey + ":key:" + shortChannelHealthHash(key)
}

func keyHealthRouteKey(identity channelHealthIdentity, key string) string {
	return identity.RouteKey + ":key:" + shortChannelHealthHash(key)
}

func channelHealthKeyIndex(identity channelHealthIdentity, selectedKey string) int {
	for index, key := range identity.Keys {
		if key == selectedKey {
			return index
		}
	}
	return -1
}

func keyHealthOpenLocked(key string, now time.Time) bool {
	state, ok := memoryChannelHealth.Keys.States[key]
	return ok && now.Before(state.OpenUntil)
}

func unhealthyChannelKeyIndexes(identity channelHealthIdentity, now time.Time) map[int]struct{} {
	if len(identity.Keys) == 0 {
		return nil
	}
	excluded := make(map[int]struct{})
	memoryChannelHealth.Keys.RLock()
	defer memoryChannelHealth.Keys.RUnlock()
	for index, key := range identity.Keys {
		routeState := memoryChannelHealth.Keys.States[keyHealthRouteKey(identity, key)]
		if keyHealthOpenLocked(keyHealthGlobalKey(identity, key), now) || keyHealthOpenLocked(keyHealthRouteKey(identity, key), now) ||
			(routeState.Capacity > 0 && routeState.InFlight >= routeState.Capacity) {
			excluded[index] = struct{}{}
		}
	}
	return excluded
}

func hasHealthyChannelKey(channel *model.Channel, identity channelHealthIdentity, now time.Time) bool {
	if channel == nil {
		return true
	}
	excluded := unhealthyChannelKeyIndexes(identity, now)
	for index := range identity.Keys {
		status := common.ChannelStatusEnabled
		if channel.ChannelInfo.IsMultiKey && channel.ChannelInfo.MultiKeyStatusList != nil {
			if configured, ok := channel.ChannelInfo.MultiKeyStatusList[index]; ok {
				status = configured
			}
		}
		if status != common.ChannelStatusEnabled {
			continue
		}
		if _, unhealthy := excluded[index]; !unhealthy {
			return true
		}
	}
	return false
}

func setChannelKeyHealth(identity channelHealthIdentity, selectedKey string, routeScoped bool, openFor time.Duration, now time.Time) {
	if selectedKey == "" {
		return
	}
	key := keyHealthGlobalKey(identity, selectedKey)
	if routeScoped {
		key = keyHealthRouteKey(identity, selectedKey)
	}
	memoryChannelHealth.Keys.Lock()
	defer memoryChannelHealth.Keys.Unlock()
	if memoryChannelHealth.Keys.LastCleanup.IsZero() || now.Sub(memoryChannelHealth.Keys.LastCleanup) >= time.Minute {
		for stateKey, state := range memoryChannelHealth.Keys.States {
			if now.Sub(state.LastTouched) >= channelHealthStateTTL && !now.Before(state.OpenUntil) {
				delete(memoryChannelHealth.Keys.States, stateKey)
			}
		}
		memoryChannelHealth.Keys.LastCleanup = now
	}
	state := memoryChannelHealth.Keys.States[key]
	state.ChannelID = identity.ChannelID
	state.Fingerprint = identity.Fingerprint
	state.KeyIndex = channelHealthKeyIndex(identity, selectedKey)
	state.Scope = "channel"
	if routeScoped {
		state.RouteLabel = identity.RouteLabel
		state.Scope = "route"
	}
	state.OpenUntil = now.Add(openFor)
	state.LastTouched = now
	memoryChannelHealth.Keys.States[key] = state
}

func AcquireChannelHealthKey(c *gin.Context, key string) bool {
	if c == nil || key == "" {
		return true
	}
	value, ok := c.Get(ginKeyChannelHealthReservation)
	if !ok {
		return true
	}
	reservation, ok := value.(channelHealthReservation)
	if !ok {
		return true
	}
	now := channelCircuitNow()
	globalKey := keyHealthGlobalKey(reservation.Identity, key)
	routeKey := keyHealthRouteKey(reservation.Identity, key)
	memoryChannelHealth.Keys.Lock()
	defer memoryChannelHealth.Keys.Unlock()
	if keyHealthOpenLocked(globalKey, now) || keyHealthOpenLocked(routeKey, now) {
		return false
	}
	state := memoryChannelHealth.Keys.States[routeKey]
	state.ChannelID = reservation.Identity.ChannelID
	state.Fingerprint = reservation.Identity.Fingerprint
	state.RouteLabel = reservation.Identity.RouteLabel
	state.KeyIndex = channelHealthKeyIndex(reservation.Identity, key)
	state.Scope = "route"
	if state.Capacity == 0 {
		state.Capacity = reservation.Identity.InitialCapacity
		if state.Capacity < channelHealthMinCapacity {
			state.Capacity = channelHealthMinCapacity
		}
	}
	// Health exclusions steer new requests away from a full key. A request that
	// raced with that check is still admitted so setup does not fail solely due
	// to a transient capacity race.
	state.InFlight++
	state.LastTouched = now
	memoryChannelHealth.Keys.States[routeKey] = state
	reservation.SelectedKeyHealth = routeKey
	c.Set(ginKeyChannelHealthReservation, reservation)
	return true
}

func finishChannelHealthKey(reservation channelHealthReservation, class ChannelFailureClass, now time.Time) {
	if reservation.SelectedKeyHealth == "" {
		return
	}
	memoryChannelHealth.Keys.Lock()
	defer memoryChannelHealth.Keys.Unlock()
	state := memoryChannelHealth.Keys.States[reservation.SelectedKeyHealth]
	if state.InFlight > 0 {
		state.InFlight--
	}
	if class == "success" {
		if state.Capacity < channelHealthMaxCapacity {
			state.SuccessesSinceIncrease++
			threshold := state.Capacity / 8
			if threshold < 4 {
				threshold = 4
			}
			if state.SuccessesSinceIncrease >= threshold {
				state.Capacity++
				state.SuccessesSinceIncrease = 0
			}
		}
	}
	state.LastTouched = now
	memoryChannelHealth.Keys.States[reservation.SelectedKeyHealth] = state
}

func getAggregateHealthStateLocked(shard *channelAggregateHealthShard, identity channelHealthIdentity, now time.Time) *channelAggregateHealthState {
	if shard.LastCleanup.IsZero() || now.Sub(shard.LastCleanup) >= time.Minute {
		for key, candidate := range shard.States {
			if !candidate.ProbeInFlight && !candidate.Suspect && candidate.OpenUntil.IsZero() && len(candidate.UnhealthyRoutes) == 0 &&
				now.Sub(candidate.LastTouched) >= channelHealthStateTTL {
				delete(shard.States, key)
			}
		}
		shard.LastCleanup = now
	}
	state := shard.States[identity.ChannelKey]
	if state == nil {
		state = &channelAggregateHealthState{
			FailedRoutesSinceSuccess: make(map[string]struct{}),
			RecentFailureRoutes:      make(map[string]time.Time),
			UnhealthyRoutes:          make(map[string]time.Time),
		}
		shard.States[identity.ChannelKey] = state
	}
	if state.FailedRoutesSinceSuccess == nil {
		state.FailedRoutesSinceSuccess = make(map[string]struct{})
	}
	if state.RecentFailureRoutes == nil {
		state.RecentFailureRoutes = make(map[string]time.Time)
	}
	if state.UnhealthyRoutes == nil {
		state.UnhealthyRoutes = make(map[string]time.Time)
	}
	state.ChannelID = identity.ChannelID
	state.Fingerprint = identity.Fingerprint
	for route, expiry := range state.UnhealthyRoutes {
		if !now.Before(expiry) {
			delete(state.UnhealthyRoutes, route)
		}
	}
	state.LastTouched = now
	return state
}

func currentAggregateHealthBucketLocked(state *channelAggregateHealthState, now time.Time) *channelHealthBucket {
	epoch := healthBucketEpoch(now)
	index := int(epoch % int64(channelHealthBucketCount))
	bucket := &state.Buckets[index]
	if bucket.Epoch != epoch {
		*bucket = channelHealthBucket{Epoch: epoch}
	}
	return bucket
}

func summarizeAggregateHealthLocked(state *channelAggregateHealthState, now time.Time) (successes int, failures int, timeouts int) {
	currentEpoch := healthBucketEpoch(now)
	for i := range state.Buckets {
		bucket := state.Buckets[i]
		if bucket.Epoch <= 0 || currentEpoch-bucket.Epoch < 0 || currentEpoch-bucket.Epoch >= int64(channelHealthBucketCount) {
			continue
		}
		successes += bucket.Successes
		failures += bucket.Failures
		timeouts += bucket.Timeouts
	}
	return
}

func shouldScheduleAggregateProbeLocked(state *channelAggregateHealthState, now time.Time, class ChannelFailureClass) bool {
	if state.Suspect || !state.OpenUntil.IsZero() || state.ProbeInFlight {
		return false
	}
	noSuccess := state.FailuresSinceSuccess >= channelHealthSlowMultiRouteFailures &&
		len(state.FailedRoutesSinceSuccess) >= channelHealthSlowMultiRouteCount &&
		!state.NoSuccessFailureAt.IsZero() && now.Sub(state.NoSuccessFailureAt) >= channelHealthWindow
	if class == ChannelFailurePoolAccount {
		return noSuccess
	}
	successes, failures, timeouts := summarizeAggregateHealthLocked(state, now)
	total := successes + failures
	multiRouteWindow := len(state.RecentFailureRoutes) >= channelHealthSlowMultiRouteCount
	poorSuccessRate := multiRouteWindow && total >= channelHealthAggregateMinimumSamples &&
		float64(failures)/float64(total) >= channelHealthAggregateFailureRate
	latencyFailure := multiRouteWindow && total >= channelHealthLatencyMinimumSamples &&
		float64(timeouts)/float64(total) >= channelHealthLatencyTimeoutRate &&
		float64(successes)/float64(total) <= channelHealthLatencyMaxSuccessRate
	return noSuccess || poorSuccessRate || latencyFailure
}

func recordAggregateFailure(identity channelHealthIdentity, now time.Time, class ChannelFailureClass, reason string, statusCode int) bool {
	if !isProbeEligibleFailureClass(class) {
		return false
	}
	_, requestPath := splitChannelRouteLabel(identity.RouteLabel)
	if !ChannelHealthProbeSupportsPath(requestPath) {
		return false
	}
	shard := channelAggregateHealthShardFor(identity.ChannelKey)
	shard.Lock()
	defer shard.Unlock()
	state := getAggregateHealthStateLocked(shard, identity, now)
	bucket := currentAggregateHealthBucketLocked(state, now)
	if class == ChannelFailurePoolAccount {
		bucket.PoolFailures++
	} else {
		bucket.Failures++
		if statusCode == http.StatusGatewayTimeout || statusCode == 524 {
			bucket.Timeouts++
		}
	}
	if state.NoSuccessFailureAt.IsZero() {
		state.NoSuccessFailureAt = now
	}
	state.FailuresSinceSuccess++
	state.FailedRoutesSinceSuccess[identity.RouteLabel] = struct{}{}
	state.RecentFailureRoutes[identity.RouteLabel] = now
	for routeLabel, failedAt := range state.RecentFailureRoutes {
		if now.Sub(failedAt) >= channelHealthWindow {
			delete(state.RecentFailureRoutes, routeLabel)
		}
	}
	state.LastFailureReason = reason
	state.LastFailureStatusCode = statusCode
	state.LastFailureAt = now
	if !shouldScheduleAggregateProbeLocked(state, now, class) {
		return false
	}
	state.Suspect = true
	state.ProbeDue = now
	state.ProbeType = ChannelHealthProbeTypeInitial
	state.ProbeTriggerClass = class
	state.ProbeScope = ChannelHealthProbeScopeChannel
	state.ProbeRouteLabel = identity.RouteLabel
	state.ProbeRouteKey = identity.RouteKey
	persistAggregateHealthStateLocked(identity, state, persistentChannelHealthSuspect)
	return true
}

func recordAggregateSuccess(identity channelHealthIdentity, now time.Time) {
	shard := channelAggregateHealthShardFor(identity.ChannelKey)
	shard.Lock()
	defer shard.Unlock()
	state := getAggregateHealthStateLocked(shard, identity, now)
	hadPersistentState := state.Suspect || state.ProbeInFlight || !state.OpenUntil.IsZero()
	currentAggregateHealthBucketLocked(state, now).Successes++
	state.NoSuccessFailureAt = time.Time{}
	state.FailuresSinceSuccess = 0
	clear(state.FailedRoutesSinceSuccess)
	state.ProbeTriggerClass = ""
	state.LastSuccessAt = now
	state.Suspect = false
	state.ProbeDue = time.Time{}
	if state.ProbeInFlight && (state.ProbeScope == ChannelHealthProbeScopeChannel || state.ProbeRouteLabel == identity.RouteLabel) {
		state.ProbeRevision++
		state.ProbeInFlight = false
		state.ProbeID = 0
		state.ProbeLeaseUntil = time.Time{}
	}
	if !state.OpenUntil.IsZero() {
		state.OpenUntil = time.Time{}
		clear(state.UnhealthyRoutes)
	}
	if hadPersistentState {
		deletePersistentHealthState(identity.ChannelKey)
	}
}

func recoverChannelRoutesAfterProbe(channelID int, fingerprint string, now time.Time) {
	for index := range memoryChannelHealth.RouteShards {
		shard := &memoryChannelHealth.RouteShards[index]
		shard.Lock()
		for _, state := range shard.Routes {
			if state.ChannelID != channelID || state.Fingerprint != fingerprint {
				continue
			}
			if !state.OpenUntil.IsZero() || state.Suspect || state.ProbeInFlight {
				state.ProbeGeneration++
				startRouteRecoveryLocked(state, now)
			}
		}
		shard.Unlock()
	}
}

func allowAggregateChannel(identity channelHealthIdentity, now time.Time) bool {
	shard := channelAggregateHealthShardFor(identity.ChannelKey)
	shard.Lock()
	defer shard.Unlock()
	state := getAggregateHealthStateLocked(shard, identity, now)
	if !state.OpenUntil.IsZero() {
		return false
	}
	return true
}

func markAggregateRouteUnhealthy(identity channelHealthIdentity, now time.Time) {
	shard := channelAggregateHealthShardFor(identity.ChannelKey)
	shard.Lock()
	defer shard.Unlock()
	state := shard.States[identity.ChannelKey]
	if state == nil {
		state = &channelAggregateHealthState{UnhealthyRoutes: make(map[string]time.Time)}
		shard.States[identity.ChannelKey] = state
	}
	state.ChannelID = identity.ChannelID
	state.Fingerprint = identity.Fingerprint
	for route, expiry := range state.UnhealthyRoutes {
		if !now.Before(expiry) {
			delete(state.UnhealthyRoutes, route)
		}
	}
	state.UnhealthyRoutes[identity.RouteLabel] = now.Add(channelHealthOpenFor)
	state.LastTouched = now
	if len(state.UnhealthyRoutes) >= 2 {
		state.OpenUntil = now.Add(channelHealthOpenFor)
		state.ProbeDue = state.OpenUntil
		state.ProbeType = ChannelHealthProbeTypeRecovery
		state.ProbeScope = ChannelHealthProbeScopeChannel
		state.ProbeRouteLabel = identity.RouteLabel
		state.ProbeRouteKey = identity.RouteKey
		state.ProbeInFlight = false
		persistAggregateHealthStateLocked(identity, state, persistentChannelHealthOpen)
	}
}

func markAggregateRouteHealthy(identity channelHealthIdentity) {
	shard := channelAggregateHealthShardFor(identity.ChannelKey)
	shard.Lock()
	defer shard.Unlock()
	if state := shard.States[identity.ChannelKey]; state != nil {
		delete(state.UnhealthyRoutes, identity.RouteLabel)
		state.ProbeInFlight = false
		if len(state.UnhealthyRoutes) < 2 {
			state.OpenUntil = time.Time{}
			state.ProbeDue = time.Time{}
			deletePersistentHealthState(identity.ChannelKey)
		}
	}
}

func allowChannelHealthAttempt(c *gin.Context, channel *model.Channel, channelID int, modelName string, requestPath string) bool {
	if channelID <= 0 && channel == nil {
		return true
	}
	now := channelCircuitNow()
	identity := buildChannelHealthIdentity(channel, channelID, modelName, requestPath, now)
	if !hasHealthyChannelKey(channel, identity, now) {
		return false
	}
	if !allowAggregateChannel(identity, now) {
		return false
	}
	shard := channelHealthShardFor(identity.RouteKey)
	shard.Lock()
	state := getRouteHealthStateLocked(shard, identity, now)
	if !state.OpenUntil.IsZero() {
		shard.Unlock()
		return false
	}
	if state.InFlight >= state.Capacity {
		shard.Unlock()
		return false
	}
	state.InFlight++
	shard.Unlock()
	if c != nil {
		c.Set(ginKeyChannelHealthReservation, channelHealthReservation{Identity: identity})
	}
	return true
}

// AllowChannelCircuitAttempt is retained for callers and tests that only have a
// channel ID. Production selection should use AllowChannelHealthAttempt so key
// and configuration fingerprints participate in health isolation.
func AllowChannelCircuitAttempt(c *gin.Context, channelID int, modelName string, requestPath string) bool {
	return allowChannelHealthAttempt(c, nil, channelID, modelName, requestPath)
}

func AllowChannelHealthAttempt(c *gin.Context, channel *model.Channel, modelName string, requestPath string) bool {
	return allowChannelHealthAttempt(c, channel, 0, modelName, requestPath)
}

func currentHealthReservation(c *gin.Context, channelID int, modelName string, requestPath string) channelHealthReservation {
	if c != nil {
		if value, ok := c.Get(ginKeyChannelHealthReservation); ok {
			if reservation, ok := value.(channelHealthReservation); ok && reservation.Identity.ChannelID == channelID {
				return reservation
			}
		}
	}
	return channelHealthReservation{Identity: buildChannelHealthIdentity(nil, channelID, modelName, requestPath, channelCircuitNow())}
}

func clearHealthReservationContext(c *gin.Context) {
	if c != nil {
		c.Set(ginKeyChannelHealthReservation, nil)
	}
}

func releaseRouteReservation(reservation channelHealthReservation) {
	finishChannelHealthKey(reservation, ChannelFailureTerminal, channelCircuitNow())
	shard := channelHealthShardFor(reservation.Identity.RouteKey)
	shard.Lock()
	if state := shard.Routes[reservation.Identity.RouteKey]; state != nil {
		if state.InFlight > 0 {
			state.InFlight--
		}
	}
	shard.Unlock()
}

func recordChannelCircuitFailure(c *gin.Context, channelID int, modelName string, requestPath string, class ChannelFailureClass, reason string, statusCode int) {
	if channelID <= 0 {
		return
	}
	now := channelCircuitNow()
	reservation := currentHealthReservation(c, channelID, modelName, requestPath)
	identity := reservation.Identity
	selectedKey := ""
	if c != nil {
		selectedKey = common.GetContextKeyString(c, constant.ContextKeyChannelKey)
	}
	if class == ChannelFailureChannelFatal {
		setChannelKeyHealth(identity, selectedKey, false, channelHealthKeyFatalOpenFor, now)
	}
	finishChannelHealthKey(reservation, class, now)

	shard := channelHealthShardFor(identity.RouteKey)
	opened := false
	suspected := false
	aggregateEligible := isProbeEligibleFailureClass(class) && (class != ChannelFailureChannelFatal || selectedKey == "")
	shard.Lock()
	state := getRouteHealthStateLocked(shard, identity, now)
	previousFailureAt := state.LastFailureAt
	state.LastFailureClass = class
	state.LastFailureReason = reason
	state.LastFailureStatusCode = statusCode
	state.LastFailureAt = now
	if state.InFlight > 0 {
		state.InFlight--
	}
	if class == ChannelFailureRateLimited {
		if state.RateLimitSince.IsZero() {
			state.RateLimitSince = now
		}
	} else {
		state.RateLimitSince = time.Time{}
	}
	switch class {
	case ChannelFailureRateLimited, ChannelFailureKeyCapability, ChannelFailurePoolAccount,
		ChannelFailureUncertain, ChannelFailureTransient:
		recordRouteHealthFailureLocked(state, now, class)
	case ChannelFailureChannelFatal:
		if selectedKey == "" {
			recordRouteHealthFailureLocked(state, now, class)
		}
	}
	if aggregateEligible {
		if state.BurstFailureStartedAt.IsZero() || (!previousFailureAt.IsZero() && now.Sub(previousFailureAt) > channelHealthSuspectMinimumDuration) {
			state.BurstFailureStartedAt = now
		}
		if state.NoSuccessFailureAt.IsZero() {
			state.NoSuccessFailureAt = now
		}
		state.FailuresSinceSuccess++
	}
	if !opened && (class != ChannelFailureChannelFatal || selectedKey == "") && shouldScheduleChannelProbeLocked(state, now, class) {
		state.Suspect = true
		state.ProbeDue = now
		state.ProbeType = ChannelHealthProbeTypeInitial
		state.ProbeTriggerClass = class
		persistRouteHealthStateLocked(identity, state, persistentChannelHealthSuspect)
		suspected = true
	}
	state.LastTouched = now
	shard.Unlock()
	channelSuspected := false
	if aggregateEligible {
		channelSuspected = recordAggregateFailure(identity, now, class, reason, statusCode)
	}

	if opened {
		markAggregateRouteUnhealthy(identity, now)
		logger.LogWarn(c, fmt.Sprintf("adaptive channel route opened: channel #%d model %s path %s", channelID, modelName, requestPath))
	} else if suspected {
		logger.LogWarn(c, fmt.Sprintf("adaptive channel route pending verification: channel #%d model %s path %s", channelID, modelName, requestPath))
	}
	if channelSuspected {
		logger.LogWarn(c, fmt.Sprintf("adaptive channel pending verification: channel #%d after failures across routes", channelID))
	}
	clearHealthReservationContext(c)
}

func RecordChannelCircuitFailure(c *gin.Context, channelID int, modelName string, requestPath string, class ChannelFailureClass) {
	recordChannelCircuitFailure(c, channelID, modelName, requestPath, class, "", 0)
}

func RecordChannelCircuitFailureDecision(c *gin.Context, channelID int, modelName string, requestPath string, decision ChannelFailureDecision, statusCode int) {
	recordChannelCircuitFailure(c, channelID, modelName, requestPath, decision.Class, decision.Reason, statusCode)
}

func RecordChannelCircuitSuccess(c *gin.Context, channelID int, modelName string, requestPath string) {
	if channelID <= 0 {
		return
	}
	now := channelCircuitNow()
	reservation := currentHealthReservation(c, channelID, modelName, requestPath)
	identity := reservation.Identity
	finishChannelHealthKey(reservation, "success", now)
	recordAggregateSuccess(identity, now)
	shard := channelHealthShardFor(identity.RouteKey)
	recovered := false
	shard.Lock()
	state := getRouteHealthStateLocked(shard, identity, now)
	hadPersistentState := state.Suspect || state.ProbeInFlight || !state.OpenUntil.IsZero()
	if state.InFlight > 0 {
		state.InFlight--
	}
	recordRouteHealthSuccessLocked(state, now)
	state.LastSuccessAt = now
	state.NoSuccessFailureAt = time.Time{}
	state.FailuresSinceSuccess = 0
	state.RateLimitSince = time.Time{}
	if state.ProbeInFlight {
		state.ProbeGeneration++
		state.ProbeInFlight = false
		state.ProbeID = 0
		state.ProbeLeaseUntil = time.Time{}
	}
	if !state.OpenUntil.IsZero() {
		startRouteRecoveryLocked(state, now)
		recovered = true
	}
	if state.OpenUntil.IsZero() {
		state.Suspect = false
		state.ProbeDue = time.Time{}
	}
	if !recovered {
		increaseRouteCapacityLocked(state)
	}
	state.LastTouched = now
	if hadPersistentState {
		deletePersistentHealthState(identity.RouteKey)
	}
	shard.Unlock()
	if recovered {
		markAggregateRouteHealthy(identity)
		logger.LogInfo(c, fmt.Sprintf("adaptive channel route recovered: channel #%d model %s path %s", channelID, modelName, requestPath))
	}
	clearHealthReservationContext(c)
}

// ReleaseChannelCircuitProbe releases reserved capacity when setup fails before
// an upstream attempt. Active health probes are managed separately.
func ReleaseChannelCircuitProbe(c *gin.Context, channelID int, modelName string, requestPath string) {
	if channelID <= 0 {
		return
	}
	reservation := currentHealthReservation(c, channelID, modelName, requestPath)
	releaseRouteReservation(reservation)
	clearHealthReservationContext(c)
}

func ReleaseCurrentChannelHealthReservation(c *gin.Context) {
	if c == nil {
		return
	}
	value, ok := c.Get(ginKeyChannelHealthReservation)
	if !ok {
		return
	}
	reservation, ok := value.(channelHealthReservation)
	if !ok {
		return
	}
	releaseRouteReservation(reservation)
	clearHealthReservationContext(c)
}

func ChannelHealthKeyExclusions(channel *model.Channel, modelName string, requestPath string, existing map[int]struct{}) map[int]struct{} {
	if channel == nil {
		return existing
	}
	identity := buildChannelHealthIdentity(channel, 0, modelName, requestPath, channelCircuitNow())
	healthExcluded := unhealthyChannelKeyIndexes(identity, channelCircuitNow())
	if len(healthExcluded) == 0 {
		return existing
	}
	merged := make(map[int]struct{}, len(existing)+len(healthExcluded))
	for index := range existing {
		merged[index] = struct{}{}
	}
	for index := range healthExcluded {
		merged[index] = struct{}{}
	}
	return merged
}

type ChannelHealthProbeTarget struct {
	ChannelID    int
	ModelName    string
	RequestPath  string
	DueAt        time.Time
	Scope        ChannelHealthProbeScope
	ProbeType    ChannelHealthProbeType
	Revision     uint64
	ProbeID      uint64
	identity     channelHealthIdentity
	wasOpen      bool
	triggerClass ChannelFailureClass
	generation   uint64
}

type ChannelHealthProbeResult struct {
	Success    bool
	Class      ChannelFailureClass
	Reason     string
	StatusCode int
}

func HasDueChannelHealthProbe() bool {
	now := channelCircuitNow()
	blockedChannels := make(map[int]struct{})
	for index := range memoryChannelHealth.ChannelShards {
		shard := &memoryChannelHealth.ChannelShards[index]
		shard.Lock()
		for _, state := range shard.States {
			dueAt := state.ProbeDue
			if !state.OpenUntil.IsZero() {
				dueAt = state.OpenUntil
			}
			probeAvailable := !state.ProbeInFlight || !state.ProbeLeaseUntil.After(now)
			if probeAvailable && !dueAt.IsZero() && !dueAt.After(now) && (state.Suspect || !state.OpenUntil.IsZero()) {
				shard.Unlock()
				return true
			}
			if state.Suspect || !state.OpenUntil.IsZero() || (state.ProbeInFlight && state.ProbeLeaseUntil.After(now)) {
				blockedChannels[state.ChannelID] = struct{}{}
			}
		}
		shard.Unlock()
	}
	for index := range memoryChannelHealth.RouteShards {
		shard := &memoryChannelHealth.RouteShards[index]
		shard.Lock()
		for _, state := range shard.Routes {
			if _, blocked := blockedChannels[state.ChannelID]; blocked {
				continue
			}
			due := (state.Suspect && !state.ProbeDue.After(now)) || (!state.OpenUntil.IsZero() && !state.OpenUntil.After(now))
			probeAvailable := !state.ProbeInFlight || !state.ProbeLeaseUntil.After(now)
			if due && probeAvailable {
				shard.Unlock()
				return true
			}
		}
		shard.Unlock()
	}
	return false
}

func ClaimDueChannelHealthProbes(limit int) []ChannelHealthProbeTarget {
	if limit <= 0 {
		limit = 1
	}
	now := channelCircuitNow()
	targets := make([]ChannelHealthProbeTarget, 0, limit)
	claimedChannels := make(map[int]struct{})
	for index := range memoryChannelHealth.ChannelShards {
		shard := &memoryChannelHealth.ChannelShards[index]
		shard.Lock()
		for channelKey, state := range shard.States {
			if len(targets) >= limit {
				break
			}
			if _, claimed := claimedChannels[state.ChannelID]; claimed {
				continue
			}
			dueAt := state.ProbeDue
			wasOpen := !state.OpenUntil.IsZero()
			if wasOpen {
				dueAt = state.OpenUntil
			}
			if dueAt.IsZero() || dueAt.After(now) || (!state.Suspect && !wasOpen) || (state.ProbeInFlight && state.ProbeLeaseUntil.After(now)) {
				continue
			}
			modelName, requestPath := splitChannelRouteLabel(state.ProbeRouteLabel)
			if state.ProbeRouteKey == "" || !ChannelHealthProbeSupportsPath(requestPath) {
				continue
			}
			probeType := state.ProbeType
			if wasOpen || probeType == "" {
				if wasOpen {
					probeType = ChannelHealthProbeTypeRecovery
				} else {
					probeType = ChannelHealthProbeTypeInitial
				}
			}
			state.ProbeRevision++
			state.ProbeID = channelHealthProbeSequence.Add(1)
			state.ProbeType = probeType
			state.ProbeScope = ChannelHealthProbeScopeChannel
			state.ProbeInFlight = true
			state.ProbeLeaseUntil = now.Add(channelHealthProbeLeaseForPath(requestPath))
			state.LastTouched = now
			identity := channelHealthIdentity{
				ChannelID:   state.ChannelID,
				Fingerprint: state.Fingerprint,
				ChannelKey:  channelKey,
				RouteKey:    state.ProbeRouteKey,
				RouteLabel:  state.ProbeRouteLabel,
			}
			targets = append(targets, ChannelHealthProbeTarget{
				ChannelID: state.ChannelID, ModelName: modelName, RequestPath: requestPath, DueAt: dueAt,
				Scope: ChannelHealthProbeScopeChannel, ProbeType: probeType, Revision: state.ProbeRevision,
				ProbeID: state.ProbeID, identity: identity, wasOpen: wasOpen, triggerClass: state.ProbeTriggerClass,
			})
			persistAggregateHealthStateLocked(identity, state, persistentChannelHealthProbing)
			claimedChannels[state.ChannelID] = struct{}{}
		}
		shard.Unlock()
		if len(targets) >= limit {
			return targets
		}
	}
	for index := range memoryChannelHealth.RouteShards {
		shard := &memoryChannelHealth.RouteShards[index]
		shard.Lock()
		for routeKey, state := range shard.Routes {
			if len(targets) >= limit {
				break
			}
			if _, claimed := claimedChannels[state.ChannelID]; claimed || (state.ProbeInFlight && state.ProbeLeaseUntil.After(now)) {
				continue
			}
			dueAt := state.ProbeDue
			wasOpen := !state.OpenUntil.IsZero()
			if wasOpen {
				dueAt = state.OpenUntil
			}
			if dueAt.IsZero() || dueAt.After(now) || (!state.Suspect && !wasOpen) {
				continue
			}
			modelName, requestPath := splitChannelRouteLabel(state.RouteLabel)
			identity := channelHealthIdentity{
				ChannelID:       state.ChannelID,
				Fingerprint:     state.Fingerprint,
				RouteKey:        routeKey,
				RouteLabel:      state.RouteLabel,
				InitialCapacity: state.InitialCapacity,
			}
			identity.ChannelKey = fmt.Sprintf("%d:%s:all", state.ChannelID, state.Fingerprint)
			channelID := state.ChannelID
			probeType := ChannelHealthProbeTypeInitial
			if wasOpen {
				probeType = ChannelHealthProbeTypeRecovery
			}
			probeID := channelHealthProbeSequence.Add(1)
			state.ProbeInFlight = true
			state.ProbeGeneration++
			state.ProbeID = probeID
			state.ProbeType = probeType
			probeLease := channelHealthProbeLeaseForPath(requestPath)
			state.ProbeLeaseUntil = now.Add(probeLease)
			state.LastTouched = now
			target := ChannelHealthProbeTarget{
				ChannelID:    state.ChannelID,
				ModelName:    modelName,
				RequestPath:  requestPath,
				DueAt:        dueAt,
				Scope:        ChannelHealthProbeScopeRoute,
				ProbeType:    probeType,
				ProbeID:      probeID,
				identity:     identity,
				wasOpen:      wasOpen,
				triggerClass: state.ProbeTriggerClass,
				generation:   state.ProbeGeneration,
			}
			persistRouteHealthStateLocked(identity, state, persistentChannelHealthProbing)
			shard.Unlock()

			aggregateShard := channelAggregateHealthShardFor(identity.ChannelKey)
			aggregateShard.Lock()
			aggregate := getAggregateHealthStateLocked(aggregateShard, identity, now)
			coordinatorAvailable := (!aggregate.ProbeInFlight || !aggregate.ProbeLeaseUntil.After(now)) &&
				!aggregate.Suspect && aggregate.OpenUntil.IsZero()
			if coordinatorAvailable {
				aggregate.ProbeRevision++
				aggregate.ProbeID = probeID
				aggregate.ProbeType = probeType
				aggregate.ProbeScope = ChannelHealthProbeScopeRoute
				aggregate.ProbeRouteLabel = identity.RouteLabel
				aggregate.ProbeRouteKey = identity.RouteKey
				aggregate.ProbeInFlight = true
				aggregate.ProbeLeaseUntil = now.Add(probeLease)
				target.Revision = aggregate.ProbeRevision
			}
			aggregateShard.Unlock()

			if !coordinatorAvailable {
				shard.Lock()
				if current := shard.Routes[routeKey]; current != nil && current.ProbeInFlight && current.ProbeID == probeID {
					current.ProbeInFlight = false
					current.ProbeID = 0
					current.ProbeLeaseUntil = time.Time{}
					persistentState := persistentChannelHealthSuspect
					if !current.OpenUntil.IsZero() || probeType == ChannelHealthProbeTypeRecovery {
						persistentState = persistentChannelHealthOpen
					}
					persistRouteHealthStateLocked(identity, current, persistentState)
				}
				continue
			}
			targets = append(targets, target)
			claimedChannels[channelID] = struct{}{}
			shard.Lock()
		}
		shard.Unlock()
		if len(targets) >= limit {
			break
		}
	}
	return targets
}

func CompleteChannelHealthProbe(target ChannelHealthProbeTarget, result ChannelHealthProbeResult) {
	if target.ChannelID <= 0 || target.identity.RouteKey == "" {
		return
	}
	now := channelCircuitNow()
	aggregateShard := channelAggregateHealthShardFor(target.identity.ChannelKey)
	aggregateShard.Lock()
	aggregate := aggregateShard.States[target.identity.ChannelKey]
	if aggregate == nil || !aggregate.ProbeInFlight || aggregate.ProbeRevision != target.Revision ||
		aggregate.ProbeID != target.ProbeID || aggregate.ProbeType != target.ProbeType || aggregate.ProbeScope != target.Scope {
		aggregateShard.Unlock()
		return
	}
	if target.Scope == ChannelHealthProbeScopeChannel {
		aggregate.ProbeInFlight = false
		aggregate.ProbeID = 0
		aggregate.ProbeLeaseUntil = time.Time{}
		aggregate.LastTouched = now
		if result.Success {
			aggregate.Buckets = [channelHealthBucketCount]channelHealthBucket{}
			aggregate.Suspect = false
			aggregate.ProbeDue = time.Time{}
			aggregate.OpenUntil = time.Time{}
			aggregate.NoSuccessFailureAt = time.Time{}
			aggregate.FailuresSinceSuccess = 0
			clear(aggregate.FailedRoutesSinceSuccess)
			clear(aggregate.RecentFailureRoutes)
			clear(aggregate.UnhealthyRoutes)
			aggregate.ProbeTriggerClass = ""
			aggregate.LastSuccessAt = now
			recoverChannelRoutesAfterProbe(target.ChannelID, target.identity.Fingerprint, now)
			deletePersistentChannelHealthStates(target.ChannelID)
			aggregateShard.Unlock()
			logger.LogInfo(context.Background(), fmt.Sprintf("adaptive channel probe succeeded: channel #%d model %s path %s", target.ChannelID, target.ModelName, target.RequestPath))
			return
		}
		if !isConclusiveProbeFailure(target, result.Class) {
			aggregate.LastFailureReason = result.Reason
			aggregate.LastFailureStatusCode = result.StatusCode
			aggregate.LastFailureAt = now
			if target.wasOpen {
				aggregate.Suspect = false
				aggregate.OpenUntil = now.Add(channelHealthOpenFor)
				aggregate.ProbeDue = aggregate.OpenUntil
				aggregate.ProbeType = ChannelHealthProbeTypeRecovery
				persistAggregateHealthStateLocked(target.identity, aggregate, persistentChannelHealthOpen)
			} else {
				aggregate.Suspect = true
				aggregate.OpenUntil = time.Time{}
				aggregate.ProbeDue = now.Add(channelHealthOpenFor)
				aggregate.ProbeType = ChannelHealthProbeTypeInitial
				persistAggregateHealthStateLocked(target.identity, aggregate, persistentChannelHealthSuspect)
			}
			aggregateShard.Unlock()
			logger.LogWarn(context.Background(), fmt.Sprintf("adaptive channel probe inconclusive: channel #%d model %s path %s", target.ChannelID, target.ModelName, target.RequestPath))
			return
		}
		aggregate.Suspect = false
		aggregate.OpenUntil = now.Add(channelHealthOpenFor)
		aggregate.ProbeDue = aggregate.OpenUntil
		aggregate.ProbeType = ChannelHealthProbeTypeRecovery
		aggregate.LastFailureReason = result.Reason
		aggregate.LastFailureStatusCode = result.StatusCode
		aggregate.LastFailureAt = now
		persistAggregateHealthStateLocked(target.identity, aggregate, persistentChannelHealthOpen)
		aggregateShard.Unlock()
		logger.LogWarn(context.Background(), fmt.Sprintf("adaptive channel probe failed: channel #%d model %s path %s", target.ChannelID, target.ModelName, target.RequestPath))
		return
	}

	shard := channelHealthShardFor(target.identity.RouteKey)
	shard.Lock()
	state := shard.Routes[target.identity.RouteKey]
	if state == nil || state.ChannelID != target.ChannelID || state.Fingerprint != target.identity.Fingerprint ||
		!state.ProbeInFlight || state.ProbeGeneration != target.generation || state.ProbeID != target.ProbeID || state.ProbeType != target.ProbeType {
		shard.Unlock()
		aggregate.ProbeInFlight = false
		aggregate.ProbeID = 0
		aggregate.ProbeLeaseUntil = time.Time{}
		aggregateShard.Unlock()
		return
	}
	state.ProbeInFlight = false
	state.ProbeID = 0
	state.ProbeLeaseUntil = time.Time{}
	aggregate.ProbeInFlight = false
	aggregate.ProbeID = 0
	aggregate.ProbeLeaseUntil = time.Time{}
	state.LastTouched = now
	if result.Success {
		state.Suspect = false
		state.ProbeDue = time.Time{}
		state.RateLimitSince = time.Time{}
		state.ProbeTriggerClass = ""
		state.LastSuccessAt = now
		recordRouteHealthSuccessLocked(state, now)
		currentAggregateHealthBucketLocked(aggregate, now).Successes++
		aggregate.NoSuccessFailureAt = time.Time{}
		aggregate.FailuresSinceSuccess = 0
		clear(aggregate.FailedRoutesSinceSuccess)
		aggregate.LastSuccessAt = now
		if target.wasOpen || !state.OpenUntil.IsZero() {
			startRouteRecoveryLocked(state, now)
		}
		deletePersistentHealthState(target.identity.RouteKey)
		shard.Unlock()
		aggregateShard.Unlock()
		markAggregateRouteHealthy(target.identity)
		logger.LogInfo(context.Background(), fmt.Sprintf("adaptive channel probe succeeded: channel #%d model %s path %s", target.ChannelID, target.ModelName, target.RequestPath))
		return
	}
	if !target.wasOpen && !state.Suspect {
		shard.Unlock()
		aggregateShard.Unlock()
		return
	}
	if !isConclusiveProbeFailure(target, result.Class) {
		state.LastFailureClass = result.Class
		state.LastFailureReason = result.Reason
		state.LastFailureStatusCode = result.StatusCode
		state.LastFailureAt = now
		if target.wasOpen {
			state.Suspect = false
			state.OpenUntil = now.Add(channelHealthOpenFor)
			state.ProbeDue = state.OpenUntil
			state.ProbeType = ChannelHealthProbeTypeRecovery
			persistRouteHealthStateLocked(target.identity, state, persistentChannelHealthOpen)
		} else {
			state.Suspect = true
			state.OpenUntil = time.Time{}
			state.ProbeDue = now.Add(channelHealthOpenFor)
			state.ProbeType = ChannelHealthProbeTypeInitial
			persistRouteHealthStateLocked(target.identity, state, persistentChannelHealthSuspect)
		}
		shard.Unlock()
		aggregateShard.Unlock()
		logger.LogWarn(context.Background(), fmt.Sprintf("adaptive channel probe inconclusive: channel #%d model %s path %s", target.ChannelID, target.ModelName, target.RequestPath))
		return
	}
	if !target.wasOpen {
		state.CapacityBeforeOpen = state.Capacity
	}
	state.Suspect = false
	state.OpenUntil = now.Add(channelHealthOpenFor)
	state.ProbeDue = state.OpenUntil
	state.RecoveryTargetCapacity = 0
	state.RecoverySuccesses = 0
	state.RecoveryFailures = 0
	state.LastFailureClass = result.Class
	state.LastFailureReason = result.Reason
	state.LastFailureStatusCode = result.StatusCode
	state.LastFailureAt = now
	state.ProbeType = ChannelHealthProbeTypeRecovery
	persistRouteHealthStateLocked(target.identity, state, persistentChannelHealthOpen)
	shard.Unlock()
	aggregateShard.Unlock()
	markAggregateRouteUnhealthy(target.identity, now)
	logger.LogWarn(context.Background(), fmt.Sprintf("adaptive channel probe failed: channel #%d model %s path %s", target.ChannelID, target.ModelName, target.RequestPath))
}

func ReleaseChannelHealthProbe(target ChannelHealthProbeTarget) {
	if target.ChannelID <= 0 || target.identity.RouteKey == "" {
		return
	}
	aggregateShard := channelAggregateHealthShardFor(target.identity.ChannelKey)
	aggregateShard.Lock()
	aggregate := aggregateShard.States[target.identity.ChannelKey]
	if aggregate == nil || !aggregate.ProbeInFlight || aggregate.ProbeRevision != target.Revision ||
		aggregate.ProbeID != target.ProbeID || aggregate.ProbeType != target.ProbeType || aggregate.ProbeScope != target.Scope {
		aggregateShard.Unlock()
		return
	}
	aggregate.ProbeInFlight = false
	aggregate.ProbeID = 0
	aggregate.ProbeLeaseUntil = time.Time{}
	if target.Scope == ChannelHealthProbeScopeChannel {
		persistentState := persistentChannelHealthSuspect
		if !aggregate.OpenUntil.IsZero() || target.ProbeType == ChannelHealthProbeTypeRecovery {
			persistentState = persistentChannelHealthOpen
		}
		persistAggregateHealthStateLocked(target.identity, aggregate, persistentState)
	}
	if target.Scope == ChannelHealthProbeScopeRoute {
		shard := channelHealthShardFor(target.identity.RouteKey)
		shard.Lock()
		if state := shard.Routes[target.identity.RouteKey]; state != nil && state.ChannelID == target.ChannelID &&
			state.Fingerprint == target.identity.Fingerprint && state.ProbeInFlight && state.ProbeGeneration == target.generation &&
			state.ProbeID == target.ProbeID && state.ProbeType == target.ProbeType {
			state.ProbeInFlight = false
			state.ProbeID = 0
			state.ProbeLeaseUntil = time.Time{}
			state.LastTouched = channelCircuitNow()
			persistentState := persistentChannelHealthSuspect
			if !state.OpenUntil.IsZero() || target.ProbeType == ChannelHealthProbeTypeRecovery {
				persistentState = persistentChannelHealthOpen
			}
			persistRouteHealthStateLocked(target.identity, state, persistentState)
		}
		shard.Unlock()
	}
	aggregateShard.Unlock()
}
