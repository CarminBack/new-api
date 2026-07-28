package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const (
	channelHealthWindow                   = 2 * time.Minute
	channelHealthBucketDuration           = 5 * time.Second
	channelHealthBucketCount              = int(channelHealthWindow / channelHealthBucketDuration)
	channelHealthOpenFor                  = 2 * time.Minute
	channelHealthStateTTL                 = 30 * time.Minute
	channelHealthShardCount               = 32
	channelHealthEstablishedCapacity      = 128
	channelHealthNewCapacity              = 16
	channelHealthRecoveryCapacity         = 8
	channelHealthMinCapacity              = 1
	channelHealthMaxCapacity              = 512
	channelHealthNewChannelAge            = 10 * time.Minute
	channelHealthKeyFatalOpenFor          = 10 * time.Minute
	channelHealthSuspectWindow            = 30 * time.Second
	channelHealthSuspectMinimumFailures   = 5
	channelHealthSuspectMinimumSamples    = 20
	channelHealthSuspectFailureRate       = 0.90
	channelHealthRateLimitConfirmFor      = 2 * time.Minute
	channelHealthRecoverySuccessTarget    = 3
	channelHealthRecoveryMaxStartCapacity = 64

	ginKeyChannelHealthReservation = "channel_health_reservation"
)

var channelCircuitNow = time.Now

type channelHealthBucket struct {
	Epoch        int64
	Successes    int
	Failures     int
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
	LastRecoveryAt         time.Time
	LastTouched            time.Time
}

type channelAggregateHealthState struct {
	ChannelID       int
	Fingerprint     string
	OpenUntil       time.Time
	ProbeInFlight   bool
	UnhealthyRoutes map[string]time.Time
	LastTouched     time.Time
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
	return shortChannelHealthHash(strconv.Itoa(channel.Type) + "\x00" + baseURL + "\x00" + channel.Key + "\x00" + channel.Models + "\x00" + channel.GetModelMapping())
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
	if class != ChannelFailureTransient && class != ChannelFailureUncertain {
		return false
	}
	successes, failures, _ := summarizeRecentRouteHealthLocked(state, now, channelHealthSuspectWindow)
	total := successes + failures
	return (failures >= channelHealthSuspectMinimumFailures && successes == 0) ||
		(total >= channelHealthSuspectMinimumSamples && float64(failures)/float64(total) >= channelHealthSuspectFailureRate)
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

func channelRecoveryStartCapacity(state *channelRouteHealthState) int {
	target := state.CapacityBeforeOpen
	if target <= 0 {
		target = state.InitialCapacity
	}
	if target <= 0 {
		target = channelHealthMinCapacity
	}
	capacity := target / 2
	if capacity < channelHealthRecoveryCapacity {
		capacity = channelHealthRecoveryCapacity
	}
	if capacity > channelHealthRecoveryMaxStartCapacity {
		capacity = channelHealthRecoveryMaxStartCapacity
	}
	if capacity > target {
		capacity = target
	}
	if capacity < channelHealthMinCapacity {
		capacity = channelHealthMinCapacity
	}
	return capacity
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
	state.Suspect = false
	state.RateLimitSince = time.Time{}
	state.CapacityBeforeOpen = target
	state.RecoveryTargetCapacity = target
	state.Capacity = channelRecoveryStartCapacity(state)
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

func allowAggregateChannel(identity channelHealthIdentity, now time.Time) bool {
	shard := channelAggregateHealthShardFor(identity.ChannelKey)
	shard.Lock()
	defer shard.Unlock()
	if shard.LastCleanup.IsZero() || now.Sub(shard.LastCleanup) >= time.Minute {
		for key, candidate := range shard.States {
			if !candidate.ProbeInFlight && candidate.OpenUntil.IsZero() && len(candidate.UnhealthyRoutes) == 0 &&
				now.Sub(candidate.LastTouched) >= channelHealthStateTTL {
				delete(shard.States, key)
			}
		}
		shard.LastCleanup = now
	}
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
	state.LastTouched = now
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
		state.ProbeInFlight = false
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
	shard.Lock()
	state := getRouteHealthStateLocked(shard, identity, now)
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
	if state.OpenUntil.IsZero() && state.RecoveryTargetCapacity > 0 &&
		(class == ChannelFailureTransient || class == ChannelFailureUncertain) {
		state.RecoveryFailures++
		state.RecoverySuccesses = 0
		if state.RecoveryFailures >= 2 {
			state.CapacityBeforeOpen = state.RecoveryTargetCapacity
			state.RecoveryTargetCapacity = 0
			state.OpenUntil = now.Add(channelHealthOpenFor)
			state.ProbeDue = state.OpenUntil
			state.ProbeInFlight = false
			state.Suspect = false
			opened = true
		}
	}
	if !opened && shouldScheduleChannelProbeLocked(state, now, class) {
		state.Suspect = true
		state.ProbeDue = now
		suspected = true
	}
	state.LastTouched = now
	shard.Unlock()

	if opened {
		markAggregateRouteUnhealthy(identity, now)
		logger.LogWarn(c, fmt.Sprintf("adaptive channel route opened: channel #%d model %s path %s", channelID, modelName, requestPath))
	} else if suspected {
		logger.LogWarn(c, fmt.Sprintf("adaptive channel route pending verification: channel #%d model %s path %s", channelID, modelName, requestPath))
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
	shard := channelHealthShardFor(identity.RouteKey)
	recovered := false
	shard.Lock()
	state := getRouteHealthStateLocked(shard, identity, now)
	if state.InFlight > 0 {
		state.InFlight--
	}
	recordRouteHealthSuccessLocked(state, now)
	state.LastSuccessAt = now
	state.RateLimitSince = time.Time{}
	if !state.OpenUntil.IsZero() {
		startRouteRecoveryLocked(state, now)
		recovered = true
	}
	if state.OpenUntil.IsZero() {
		state.Suspect = false
		state.ProbeDue = time.Time{}
	}
	if state.RecoveryTargetCapacity > 0 && !recovered {
		state.RecoveryFailures = 0
		state.RecoverySuccesses++
		if state.RecoverySuccesses >= channelHealthRecoverySuccessTarget {
			state.Capacity = state.RecoveryTargetCapacity
			state.RecoveryTargetCapacity = 0
			state.RecoverySuccesses = 0
			state.CapacityBeforeOpen = 0
		}
	} else if !recovered {
		increaseRouteCapacityLocked(state)
	}
	state.LastTouched = now
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
	ChannelID   int
	ModelName   string
	RequestPath string
	DueAt       time.Time
	identity    channelHealthIdentity
	wasOpen     bool
	generation  uint64
}

type ChannelHealthProbeResult struct {
	Success    bool
	Class      ChannelFailureClass
	Reason     string
	StatusCode int
}

func HasDueChannelHealthProbe() bool {
	now := channelCircuitNow()
	for index := range memoryChannelHealth.RouteShards {
		shard := &memoryChannelHealth.RouteShards[index]
		shard.Lock()
		for _, state := range shard.Routes {
			due := (state.Suspect && !state.ProbeDue.After(now)) || (!state.OpenUntil.IsZero() && !state.OpenUntil.After(now))
			if due && !state.ProbeInFlight {
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
	for index := range memoryChannelHealth.RouteShards {
		shard := &memoryChannelHealth.RouteShards[index]
		claimedBeforeShard := len(targets)
		shard.Lock()
		for routeKey, state := range shard.Routes {
			if len(targets) >= limit {
				break
			}
			if _, claimed := claimedChannels[state.ChannelID]; claimed || state.ProbeInFlight {
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
			state.ProbeInFlight = true
			state.ProbeGeneration++
			state.LastTouched = now
			targets = append(targets, ChannelHealthProbeTarget{
				ChannelID:   state.ChannelID,
				ModelName:   modelName,
				RequestPath: requestPath,
				DueAt:       dueAt,
				identity:    identity,
				wasOpen:     wasOpen,
				generation:  state.ProbeGeneration,
			})
			claimedChannels[state.ChannelID] = struct{}{}
		}
		shard.Unlock()
		for _, target := range targets[claimedBeforeShard:] {
			aggregateShard := channelAggregateHealthShardFor(target.identity.ChannelKey)
			aggregateShard.Lock()
			if aggregate := aggregateShard.States[target.identity.ChannelKey]; aggregate != nil && !aggregate.OpenUntil.IsZero() {
				aggregate.ProbeInFlight = true
			}
			aggregateShard.Unlock()
		}
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
	shard := channelHealthShardFor(target.identity.RouteKey)
	shard.Lock()
	state := shard.Routes[target.identity.RouteKey]
	if state == nil || state.ChannelID != target.ChannelID || state.Fingerprint != target.identity.Fingerprint ||
		!state.ProbeInFlight || state.ProbeGeneration != target.generation {
		shard.Unlock()
		return
	}
	state.ProbeInFlight = false
	state.LastTouched = now
	if result.Success {
		state.Suspect = false
		state.ProbeDue = time.Time{}
		state.RateLimitSince = time.Time{}
		state.LastSuccessAt = now
		recordRouteHealthSuccessLocked(state, now)
		if target.wasOpen || !state.OpenUntil.IsZero() {
			startRouteRecoveryLocked(state, now)
		}
		shard.Unlock()
		markAggregateRouteHealthy(target.identity)
		logger.LogInfo(context.Background(), fmt.Sprintf("adaptive channel probe succeeded: channel #%d model %s path %s", target.ChannelID, target.ModelName, target.RequestPath))
		return
	}
	if !target.wasOpen && !state.Suspect {
		shard.Unlock()
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
	shard.Unlock()
	markAggregateRouteUnhealthy(target.identity, now)
	logger.LogWarn(context.Background(), fmt.Sprintf("adaptive channel probe failed: channel #%d model %s path %s", target.ChannelID, target.ModelName, target.RequestPath))
}

func ReleaseChannelHealthProbe(target ChannelHealthProbeTarget) {
	if target.ChannelID <= 0 || target.identity.RouteKey == "" {
		return
	}
	shard := channelHealthShardFor(target.identity.RouteKey)
	released := false
	shard.Lock()
	if state := shard.Routes[target.identity.RouteKey]; state != nil && state.ChannelID == target.ChannelID &&
		state.Fingerprint == target.identity.Fingerprint && state.ProbeInFlight && state.ProbeGeneration == target.generation {
		state.ProbeInFlight = false
		state.LastTouched = channelCircuitNow()
		released = true
	}
	shard.Unlock()
	if !released {
		return
	}

	aggregateShard := channelAggregateHealthShardFor(target.identity.ChannelKey)
	aggregateShard.Lock()
	if state := aggregateShard.States[target.identity.ChannelKey]; state != nil {
		state.ProbeInFlight = false
	}
	aggregateShard.Unlock()
}
