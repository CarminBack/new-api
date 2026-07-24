package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"math"
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
	channelHealthWindow               = 2 * time.Minute
	channelHealthBucketDuration       = 5 * time.Second
	channelHealthBucketCount          = int(channelHealthWindow / channelHealthBucketDuration)
	channelHealthOpenFor              = 2 * time.Minute
	channelHealthStateTTL             = 30 * time.Minute
	channelHealthShardCount           = 32
	channelHealthEstablishedCapacity  = 128
	channelHealthNewCapacity          = 16
	channelHealthRecoveryCapacity     = 8
	channelHealthMinCapacity          = 1
	channelHealthMaxCapacity          = 512
	channelHealthNewChannelAge        = 10 * time.Minute
	channelHealthWarningLowerBound    = 0.10
	channelHealthOpenLowerBound       = 0.50
	channelHealthKeyFatalOpenFor      = 10 * time.Minute
	channelHealthKeyCapabilityOpenFor = 15 * time.Minute

	ginKeyChannelHealthReservation = "channel_health_reservation"
)

var channelCircuitNow = time.Now

type channelHealthBucket struct {
	Epoch     int64
	Successes int
	Failures  int
}

type channelRouteHealthState struct {
	Buckets                [channelHealthBucketCount]channelHealthBucket
	OpenUntil              time.Time
	ProbeInFlight          bool
	InFlight               int
	Capacity               int
	SuccessesSinceIncrease int
	LastDecreaseEpoch      int64
	LastTouched            time.Time
}

type channelAggregateHealthState struct {
	OpenUntil       time.Time
	ProbeInFlight   bool
	UnhealthyRoutes map[string]time.Time
	LastTouched     time.Time
}

type channelKeyHealthState struct {
	OpenUntil              time.Time
	InFlight               int
	Capacity               int
	SuccessesSinceIncrease int
	LastDecreaseEpoch      int64
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
	RouteProbe        bool
	ChannelProbe      bool
	SelectedKeyHealth string
}

type observedChannelConfig struct {
	Fingerprint     string
	InitialCapacity int
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
		Observed map[int]observedChannelConfig
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
	if exists && observed.Fingerprint == fingerprint {
		memoryChannelHealth.Configs.RUnlock()
		return observed.InitialCapacity
	}
	memoryChannelHealth.Configs.RUnlock()
	memoryChannelHealth.Configs.Lock()
	defer memoryChannelHealth.Configs.Unlock()
	observed, exists = memoryChannelHealth.Configs.Observed[channel.Id]
	if exists && observed.Fingerprint == fingerprint {
		return observed.InitialCapacity
	}
	if exists && observed.Fingerprint != fingerprint {
		memoryChannelHealth.Configs.Observed[channel.Id] = observedChannelConfig{Fingerprint: fingerprint, InitialCapacity: channelHealthNewCapacity}
		return channelHealthNewCapacity
	}
	if channel.CreatedTime > 0 && now.Sub(time.Unix(channel.CreatedTime, 0)) < channelHealthNewChannelAge {
		memoryChannelHealth.Configs.Observed[channel.Id] = observedChannelConfig{Fingerprint: fingerprint, InitialCapacity: channelHealthNewCapacity}
		return channelHealthNewCapacity
	}
	memoryChannelHealth.Configs.Observed[channel.Id] = observedChannelConfig{Fingerprint: fingerprint, InitialCapacity: channelHealthEstablishedCapacity}
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
		if state.InFlight == 0 && !state.ProbeInFlight && now.Sub(state.LastTouched) >= channelHealthStateTTL && !now.Before(state.OpenUntil) {
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
	state.LastTouched = now
	return state
}

func healthBucketEpoch(now time.Time) int64 {
	return now.UnixNano() / channelHealthBucketDuration.Nanoseconds()
}

func recordRouteHealthOutcomeLocked(state *channelRouteHealthState, now time.Time, success bool) {
	epoch := healthBucketEpoch(now)
	index := int(epoch % int64(channelHealthBucketCount))
	bucket := &state.Buckets[index]
	if bucket.Epoch != epoch {
		*bucket = channelHealthBucket{Epoch: epoch}
	}
	if success {
		bucket.Successes++
	} else {
		bucket.Failures++
	}
}

func summarizeRouteHealthLocked(state *channelRouteHealthState, now time.Time) (successes int, failures int) {
	currentEpoch := healthBucketEpoch(now)
	for i := range state.Buckets {
		bucket := state.Buckets[i]
		if bucket.Epoch <= 0 || currentEpoch-bucket.Epoch < 0 || currentEpoch-bucket.Epoch >= int64(channelHealthBucketCount) {
			continue
		}
		successes += bucket.Successes
		failures += bucket.Failures
	}
	return successes, failures
}

func wilsonLowerBound(failures int, total int) float64 {
	if failures <= 0 || total <= 0 {
		return 0
	}
	z := 1.96
	p := float64(failures) / float64(total)
	z2 := z * z
	denominator := 1 + z2/float64(total)
	center := p + z2/(2*float64(total))
	margin := z * math.Sqrt((p*(1-p)+z2/(4*float64(total)))/float64(total))
	return (center - margin) / denominator
}

func decreaseRouteCapacityLocked(state *channelRouteHealthState, now time.Time, factor float64) {
	epoch := healthBucketEpoch(now)
	if state.LastDecreaseEpoch == epoch {
		return
	}
	state.LastDecreaseEpoch = epoch
	capacity := int(math.Floor(float64(state.Capacity) * factor))
	if capacity < channelHealthMinCapacity {
		capacity = channelHealthMinCapacity
	}
	state.Capacity = capacity
	state.SuccessesSinceIncrease = 0
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
	switch class {
	case "success":
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
	case ChannelFailureRateLimited:
		epoch := healthBucketEpoch(now)
		if state.LastDecreaseEpoch != epoch {
			state.LastDecreaseEpoch = epoch
			capacity := int(math.Floor(float64(state.Capacity) * 0.8))
			if capacity < channelHealthMinCapacity {
				capacity = channelHealthMinCapacity
			}
			state.Capacity = capacity
			state.SuccessesSinceIncrease = 0
		}
	}
	state.LastTouched = now
	memoryChannelHealth.Keys.States[reservation.SelectedKeyHealth] = state
}

func allowAggregateChannel(identity channelHealthIdentity, now time.Time) (allowed bool, probe bool) {
	shard := channelAggregateHealthShardFor(identity.ChannelKey)
	shard.Lock()
	defer shard.Unlock()
	if shard.LastCleanup.IsZero() || now.Sub(shard.LastCleanup) >= time.Minute {
		for key, candidate := range shard.States {
			if !candidate.ProbeInFlight && now.Sub(candidate.LastTouched) >= channelHealthStateTTL && !now.Before(candidate.OpenUntil) {
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
	for route, expiry := range state.UnhealthyRoutes {
		if !now.Before(expiry) {
			delete(state.UnhealthyRoutes, route)
		}
	}
	state.LastTouched = now
	if now.Before(state.OpenUntil) {
		return false, false
	}
	if state.OpenUntil.IsZero() {
		return true, false
	}
	if state.ProbeInFlight {
		return false, false
	}
	state.ProbeInFlight = true
	return true, true
}

func releaseAggregateChannelProbe(identity channelHealthIdentity, clearOpen bool) {
	shard := channelAggregateHealthShardFor(identity.ChannelKey)
	shard.Lock()
	defer shard.Unlock()
	if state := shard.States[identity.ChannelKey]; state != nil {
		state.ProbeInFlight = false
		if clearOpen {
			state.OpenUntil = time.Time{}
		}
	}
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
	}
}

func reopenAggregateChannel(identity channelHealthIdentity, now time.Time) {
	shard := channelAggregateHealthShardFor(identity.ChannelKey)
	shard.Lock()
	defer shard.Unlock()
	if state := shard.States[identity.ChannelKey]; state != nil {
		state.OpenUntil = now.Add(channelHealthOpenFor)
		state.ProbeInFlight = false
		state.LastTouched = now
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
	channelAllowed, channelProbe := allowAggregateChannel(identity, now)
	if !channelAllowed {
		return false
	}
	shard := channelHealthShardFor(identity.RouteKey)
	shard.Lock()
	state := getRouteHealthStateLocked(shard, identity, now)
	routeProbe := false
	if now.Before(state.OpenUntil) {
		shard.Unlock()
		if channelProbe {
			releaseAggregateChannelProbe(identity, false)
		}
		return false
	}
	if !state.OpenUntil.IsZero() {
		if state.ProbeInFlight {
			shard.Unlock()
			if channelProbe {
				releaseAggregateChannelProbe(identity, false)
			}
			return false
		}
		state.ProbeInFlight = true
		routeProbe = true
	}
	if !routeProbe && state.InFlight >= state.Capacity {
		shard.Unlock()
		if channelProbe {
			releaseAggregateChannelProbe(identity, false)
		}
		return false
	}
	state.InFlight++
	shard.Unlock()
	if c != nil {
		c.Set(ginKeyChannelHealthReservation, channelHealthReservation{Identity: identity, RouteProbe: routeProbe, ChannelProbe: channelProbe})
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

func releaseRouteReservation(reservation channelHealthReservation, releaseProbe bool) {
	finishChannelHealthKey(reservation, ChannelFailureTerminal, channelCircuitNow())
	shard := channelHealthShardFor(reservation.Identity.RouteKey)
	shard.Lock()
	if state := shard.Routes[reservation.Identity.RouteKey]; state != nil {
		if state.InFlight > 0 {
			state.InFlight--
		}
		if releaseProbe && reservation.RouteProbe {
			state.ProbeInFlight = false
		}
	}
	shard.Unlock()
	if releaseProbe && reservation.ChannelProbe {
		releaseAggregateChannelProbe(reservation.Identity, false)
	}
}

func RecordChannelCircuitFailure(c *gin.Context, channelID int, modelName string, requestPath string, class ChannelFailureClass) {
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
	if class == ChannelFailureKeyCapability {
		setChannelKeyHealth(identity, selectedKey, true, channelHealthKeyCapabilityOpenFor, now)
	} else if class == ChannelFailureChannelFatal {
		setChannelKeyHealth(identity, selectedKey, false, channelHealthKeyFatalOpenFor, now)
	}
	finishChannelHealthKey(reservation, class, now)

	shard := channelHealthShardFor(identity.RouteKey)
	opened := false
	shard.Lock()
	state := getRouteHealthStateLocked(shard, identity, now)
	if state.InFlight > 0 {
		state.InFlight--
	}
	if reservation.RouteProbe {
		state.ProbeInFlight = false
		state.OpenUntil = now.Add(channelHealthOpenFor)
		state.Capacity = channelHealthRecoveryCapacity
		opened = true
	} else {
		failureCapacityFactor := 0.0
		switch class {
		case ChannelFailureRateLimited:
		case ChannelFailureUncertain:
			recordRouteHealthOutcomeLocked(state, now, false)
			failureCapacityFactor = 0.5
		case ChannelFailureTransient:
			recordRouteHealthOutcomeLocked(state, now, false)
			failureCapacityFactor = 0.7
		case ChannelFailureChannelFatal:
			if selectedKey == "" {
				recordRouteHealthOutcomeLocked(state, now, false)
				failureCapacityFactor = 0.5
			}
		}
		successes, failures := summarizeRouteHealthLocked(state, now)
		failureLowerBound := wilsonLowerBound(failures, successes+failures)
		if failureCapacityFactor > 0 && failureLowerBound > channelHealthWarningLowerBound {
			decreaseRouteCapacityLocked(state, now, failureCapacityFactor)
		}
		if failures > 0 && failureLowerBound > channelHealthOpenLowerBound {
			state.OpenUntil = now.Add(channelHealthOpenFor)
			state.ProbeInFlight = false
			state.Capacity = channelHealthRecoveryCapacity
			opened = true
		}
	}
	state.LastTouched = now
	shard.Unlock()

	if reservation.ChannelProbe {
		reopenAggregateChannel(identity, now)
	}
	if opened {
		markAggregateRouteUnhealthy(identity, now)
		logger.LogWarn(c, fmt.Sprintf("adaptive channel route opened: channel #%d model %s path %s", channelID, modelName, requestPath))
	}
	clearHealthReservationContext(c)
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
	recordRouteHealthOutcomeLocked(state, now, true)
	if reservation.RouteProbe {
		state.ProbeInFlight = false
		state.OpenUntil = time.Time{}
		state.Capacity = channelHealthRecoveryCapacity
		state.SuccessesSinceIncrease = 0
		recovered = true
	} else {
		increaseRouteCapacityLocked(state)
	}
	state.LastTouched = now
	shard.Unlock()
	if reservation.ChannelProbe {
		releaseAggregateChannelProbe(identity, true)
	}
	if recovered {
		markAggregateRouteHealthy(identity)
		logger.LogInfo(c, fmt.Sprintf("adaptive channel route recovered: channel #%d model %s path %s", channelID, modelName, requestPath))
	}
	clearHealthReservationContext(c)
}

// ReleaseChannelCircuitProbe releases admission when setup failed before an
// upstream attempt. It also prevents an abandoned half-open probe from blocking
// the route until process restart.
func ReleaseChannelCircuitProbe(c *gin.Context, channelID int, modelName string, requestPath string) {
	if channelID <= 0 {
		return
	}
	reservation := currentHealthReservation(c, channelID, modelName, requestPath)
	releaseRouteReservation(reservation, true)
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
	releaseRouteReservation(reservation, true)
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

func routeHealthLowerBound(channelID int, modelName string, requestPath string) float64 {
	identity := buildChannelHealthIdentity(nil, channelID, modelName, requestPath, channelCircuitNow())
	shard := channelHealthShardFor(identity.RouteKey)
	shard.Lock()
	defer shard.Unlock()
	state := shard.Routes[identity.RouteKey]
	if state == nil {
		return 0
	}
	successes, failures := summarizeRouteHealthLocked(state, channelCircuitNow())
	return wilsonLowerBound(failures, successes+failures)
}

func routeHealthWarning(channelID int, modelName string, requestPath string) bool {
	return routeHealthLowerBound(channelID, modelName, requestPath) > channelHealthWarningLowerBound
}
