package service

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const (
	ChannelHealthStateHealthy     = "healthy"
	ChannelHealthStateCircuitOpen = "circuit_open"
	ChannelHealthStateHalfOpen    = "half_open"
	ChannelHealthStateDegraded    = "degraded"
	ChannelHealthStateRecovering  = "recovering"
	ChannelHealthStateSaturated   = "saturated"
	ChannelHealthStateIsolated    = "isolated"
)

const (
	ChannelHealthRecoveryScopeChannel = "channel"
	ChannelHealthRecoveryScopeRoute   = "route"
	ChannelHealthRecoveryScopeKey     = "key"
)

type ChannelRouteHealthSnapshot struct {
	ModelName              string              `json:"model_name"`
	RequestPath            string              `json:"request_path"`
	State                  string              `json:"state"`
	OpenUntil              int64               `json:"open_until"`
	NextProbeAt            int64               `json:"next_probe_at"`
	ProbeInFlight          bool                `json:"probe_in_flight"`
	InFlight               int                 `json:"in_flight"`
	Capacity               int                 `json:"capacity"`
	InitialCapacity        int                 `json:"initial_capacity"`
	RecoveryTargetCapacity int                 `json:"recovery_target_capacity,omitempty"`
	RecoverySuccesses      int                 `json:"recovery_successes,omitempty"`
	RecoverySuccessTarget  int                 `json:"recovery_success_target,omitempty"`
	Successes              int                 `json:"successes"`
	Failures               int                 `json:"failures"`
	PoolFailures           int                 `json:"pool_failures"`
	RateLimits             int                 `json:"rate_limits"`
	LastFailureClass       ChannelFailureClass `json:"last_failure_class,omitempty"`
	LastFailureReason      string              `json:"last_failure_reason,omitempty"`
	LastFailureStatusCode  int                 `json:"last_failure_status_code,omitempty"`
	LastFailureAt          int64               `json:"last_failure_at,omitempty"`
	LastSuccessAt          int64               `json:"last_success_at,omitempty"`
	LastRecoveryAt         int64               `json:"last_recovery_at,omitempty"`
	LastTouched            int64               `json:"last_touched"`
	fingerprint            string
}

type ChannelKeyHealthSnapshot struct {
	KeyIndex    int    `json:"key_index"`
	Scope       string `json:"scope"`
	ModelName   string `json:"model_name,omitempty"`
	RequestPath string `json:"request_path,omitempty"`
	State       string `json:"state"`
	OpenUntil   int64  `json:"open_until"`
	InFlight    int    `json:"in_flight"`
	Capacity    int    `json:"capacity"`
	LastTouched int64  `json:"last_touched"`
	fingerprint string
}

type ChannelAdaptiveHealthSnapshot struct {
	ChannelID            int                          `json:"channel_id"`
	ChannelState         string                       `json:"channel_state"`
	ChannelOpenUntil     int64                        `json:"channel_open_until"`
	ChannelNextProbeAt   int64                        `json:"channel_next_probe_at"`
	ChannelProbeInFlight bool                         `json:"channel_probe_in_flight"`
	ChannelFailureReason string                       `json:"channel_failure_reason,omitempty"`
	Routes               []ChannelRouteHealthSnapshot `json:"routes"`
	Keys                 []ChannelKeyHealthSnapshot   `json:"keys"`
	fingerprint          string
}

type ChannelHealthRecoveryRequest struct {
	Scope       string
	ModelName   string
	RequestPath string
	KeyIndex    *int
}

type ChannelHealthRecoveryResult struct {
	Scope        string `json:"scope"`
	ChangedItems int    `json:"changed_items"`
	Capacity     int    `json:"capacity,omitempty"`
}

type channelHealthSnapshotKey struct {
	ChannelID   int
	Fingerprint string
}

func channelHealthSnapshotEntry(byChannel map[channelHealthSnapshotKey]*ChannelAdaptiveHealthSnapshot, channelID int, fingerprint string) *ChannelAdaptiveHealthSnapshot {
	key := channelHealthSnapshotKey{ChannelID: channelID, Fingerprint: fingerprint}
	entry := byChannel[key]
	if entry == nil {
		entry = &ChannelAdaptiveHealthSnapshot{
			ChannelID:    channelID,
			ChannelState: ChannelHealthStateHealthy,
			fingerprint:  fingerprint,
		}
		byChannel[key] = entry
	}
	return entry
}

func splitChannelRouteLabel(label string) (string, string) {
	modelName, requestPath, _ := strings.Cut(label, "\x00")
	return modelName, requestPath
}

func unixTime(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.Unix()
}

func routeHealthStateName(state *channelRouteHealthState, now time.Time) string {
	if !state.OpenUntil.IsZero() {
		if state.ProbeInFlight {
			return ChannelHealthStateHalfOpen
		}
		return ChannelHealthStateCircuitOpen
	}
	if state.Suspect || state.ProbeInFlight {
		return ChannelHealthStateHalfOpen
	}
	if state.Capacity < state.InitialCapacity {
		if !state.LastRecoveryAt.IsZero() || state.LastSuccessAt.After(state.LastFailureAt) {
			return ChannelHealthStateRecovering
		}
		return ChannelHealthStateDegraded
	}
	if state.Capacity > 0 && state.InFlight >= state.Capacity {
		return ChannelHealthStateSaturated
	}
	return ChannelHealthStateHealthy
}

func keyHealthStateName(state channelKeyHealthState, now time.Time) string {
	if now.Before(state.OpenUntil) {
		return ChannelHealthStateIsolated
	}
	if state.Capacity > 0 && state.InFlight >= state.Capacity {
		return ChannelHealthStateSaturated
	}
	return ChannelHealthStateHealthy
}

func currentChannelHealthFingerprint(channelID int) (string, bool) {
	channel, err := model.CacheGetChannel(channelID)
	if err != nil || channel == nil {
		return "", false
	}
	return channelConfigFingerprint(channel), true
}

func GetChannelAdaptiveHealthSnapshots(includeHealthy bool) []ChannelAdaptiveHealthSnapshot {
	now := channelCircuitNow()
	byChannel := make(map[channelHealthSnapshotKey]*ChannelAdaptiveHealthSnapshot)

	for index := range memoryChannelHealth.RouteShards {
		shard := &memoryChannelHealth.RouteShards[index]
		shard.Lock()
		for _, state := range shard.Routes {
			stateName := routeHealthStateName(state, now)
			if !includeHealthy && stateName == ChannelHealthStateHealthy {
				continue
			}
			modelName, requestPath := splitChannelRouteLabel(state.RouteLabel)
			successes, failures, poolFailures, rateLimits := summarizeRouteHealthLocked(state, now)
			entry := channelHealthSnapshotEntry(byChannel, state.ChannelID, state.Fingerprint)
			entry.Routes = append(entry.Routes, ChannelRouteHealthSnapshot{
				ModelName:              modelName,
				RequestPath:            requestPath,
				State:                  stateName,
				OpenUntil:              unixTime(state.OpenUntil),
				NextProbeAt:            unixTime(state.ProbeDue),
				ProbeInFlight:          state.ProbeInFlight,
				InFlight:               state.InFlight,
				Capacity:               state.Capacity,
				InitialCapacity:        state.InitialCapacity,
				RecoveryTargetCapacity: state.RecoveryTargetCapacity,
				RecoverySuccesses:      state.RecoverySuccesses,
				RecoverySuccessTarget:  channelHealthRecoverySuccessTarget,
				Successes:              successes,
				Failures:               failures,
				PoolFailures:           poolFailures,
				RateLimits:             rateLimits,
				LastFailureClass:       state.LastFailureClass,
				LastFailureReason:      state.LastFailureReason,
				LastFailureStatusCode:  state.LastFailureStatusCode,
				LastFailureAt:          unixTime(state.LastFailureAt),
				LastSuccessAt:          unixTime(state.LastSuccessAt),
				LastRecoveryAt:         unixTime(state.LastRecoveryAt),
				LastTouched:            unixTime(state.LastTouched),
				fingerprint:            state.Fingerprint,
			})
		}
		shard.Unlock()
	}

	memoryChannelHealth.Keys.RLock()
	for _, state := range memoryChannelHealth.Keys.States {
		stateName := keyHealthStateName(state, now)
		if stateName == ChannelHealthStateHealthy {
			continue
		}
		modelName, requestPath := splitChannelRouteLabel(state.RouteLabel)
		entry := channelHealthSnapshotEntry(byChannel, state.ChannelID, state.Fingerprint)
		entry.Keys = append(entry.Keys, ChannelKeyHealthSnapshot{
			KeyIndex:    state.KeyIndex,
			Scope:       state.Scope,
			ModelName:   modelName,
			RequestPath: requestPath,
			State:       stateName,
			OpenUntil:   unixTime(state.OpenUntil),
			InFlight:    state.InFlight,
			Capacity:    state.Capacity,
			LastTouched: unixTime(state.LastTouched),
			fingerprint: state.Fingerprint,
		})
	}
	memoryChannelHealth.Keys.RUnlock()

	for index := range memoryChannelHealth.ChannelShards {
		shard := &memoryChannelHealth.ChannelShards[index]
		shard.Lock()
		for _, state := range shard.States {
			stateName := ChannelHealthStateHealthy
			if !state.OpenUntil.IsZero() {
				if state.ProbeInFlight {
					stateName = ChannelHealthStateHalfOpen
				} else {
					stateName = ChannelHealthStateCircuitOpen
				}
			} else if state.Suspect || state.ProbeInFlight {
				stateName = ChannelHealthStateHalfOpen
			}
			if !includeHealthy && stateName == ChannelHealthStateHealthy {
				continue
			}
			entry := channelHealthSnapshotEntry(byChannel, state.ChannelID, state.Fingerprint)
			entry.ChannelState = stateName
			entry.ChannelOpenUntil = unixTime(state.OpenUntil)
			entry.ChannelNextProbeAt = unixTime(state.ProbeDue)
			entry.ChannelProbeInFlight = state.ProbeInFlight
			entry.ChannelFailureReason = state.LastFailureReason
		}
		shard.Unlock()
	}

	result := make([]ChannelAdaptiveHealthSnapshot, 0, len(byChannel))
	for _, snapshot := range byChannel {
		if snapshot.ChannelID <= 0 {
			continue
		}
		if snapshot.fingerprint != "legacy" {
			fingerprint, exists := currentChannelHealthFingerprint(snapshot.ChannelID)
			if exists && fingerprint != snapshot.fingerprint {
				continue
			}
		}
		sort.Slice(snapshot.Routes, func(i, j int) bool {
			if snapshot.Routes[i].ModelName == snapshot.Routes[j].ModelName {
				return snapshot.Routes[i].RequestPath < snapshot.Routes[j].RequestPath
			}
			return snapshot.Routes[i].ModelName < snapshot.Routes[j].ModelName
		})
		sort.Slice(snapshot.Keys, func(i, j int) bool {
			if snapshot.Keys[i].KeyIndex == snapshot.Keys[j].KeyIndex {
				return snapshot.Keys[i].Scope < snapshot.Keys[j].Scope
			}
			return snapshot.Keys[i].KeyIndex < snapshot.Keys[j].KeyIndex
		})
		result = append(result, *snapshot)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ChannelID < result[j].ChannelID })
	return result
}

func resetRouteHealthState(state *channelRouteHealthState, now time.Time) {
	if state.CapacityBeforeOpen <= 0 {
		state.CapacityBeforeOpen = state.Capacity
	}
	state.ProbeGeneration++
	state.ProbeInFlight = false
	state.ProbeID = 0
	state.ProbeLeaseUntil = time.Time{}
	state.ProbeType = ChannelHealthProbeTypeRecovery
	state.Suspect = false
	state.OpenUntil = now
	state.ProbeDue = now
	state.LastTouched = now
}

func defaultChannelProbeIdentity(channel *model.Channel, now time.Time) (channelHealthIdentity, bool) {
	if channel == nil || channel.Id <= 0 {
		return channelHealthIdentity{}, false
	}
	modelName := ""
	if channel.TestModel != nil {
		modelName = strings.TrimSpace(*channel.TestModel)
	}
	if modelName == "" {
		for _, candidate := range channel.GetModels() {
			if modelName = strings.TrimSpace(candidate); modelName != "" {
				break
			}
		}
	}
	if modelName == "" {
		return channelHealthIdentity{}, false
	}
	for _, endpointType := range common.GetEndpointTypesByChannelType(channel.Type, modelName) {
		endpoint, ok := common.GetDefaultEndpointInfo(endpointType)
		if !ok || !ChannelHealthProbeSupportsPath(endpoint.Path) {
			continue
		}
		return buildChannelHealthIdentity(channel, 0, modelName, endpoint.Path, now), true
	}
	return channelHealthIdentity{}, false
}

func RecoverChannelHealth(channel *model.Channel, request ChannelHealthRecoveryRequest) (ChannelHealthRecoveryResult, error) {
	result := ChannelHealthRecoveryResult{Scope: request.Scope}
	if channel == nil || channel.Id <= 0 {
		return result, errors.New("channel is required")
	}
	now := channelCircuitNow()

	switch request.Scope {
	case ChannelHealthRecoveryScopeRoute:
		if strings.TrimSpace(request.ModelName) == "" || strings.TrimSpace(request.RequestPath) == "" {
			return result, errors.New("model_name and request_path are required for route recovery")
		}
		if !ChannelHealthProbeSupportsPath(request.RequestPath) {
			return result, errors.New("route does not support health probing")
		}
		identity := buildChannelHealthIdentity(channel, 0, request.ModelName, request.RequestPath, now)
		shard := channelHealthShardFor(identity.RouteKey)
		shard.Lock()
		if state := shard.Routes[identity.RouteKey]; state != nil {
			resetRouteHealthState(state, now)
			persistRouteHealthStateLocked(identity, state, persistentChannelHealthRecoveryPending)
			result.ChangedItems++
			result.Capacity = state.Capacity
		}
		shard.Unlock()

		aggregateShard := channelAggregateHealthShardFor(identity.ChannelKey)
		aggregateShard.Lock()
		if state := aggregateShard.States[identity.ChannelKey]; state != nil {
			state.ProbeRevision++
			state.ProbeInFlight = false
			state.ProbeID = 0
			state.ProbeLeaseUntil = time.Time{}
			state.LastTouched = now
		}
		aggregateShard.Unlock()

	case ChannelHealthRecoveryScopeChannel:
		fingerprint := channelConfigFingerprint(channel)
		var probeIdentity channelHealthIdentity
		latestFailure := time.Time{}
		for index := range memoryChannelHealth.RouteShards {
			shard := &memoryChannelHealth.RouteShards[index]
			shard.Lock()
			for routeKey, state := range shard.Routes {
				if state.ChannelID == channel.Id && state.Fingerprint == fingerprint {
					state.ProbeGeneration++
					state.ProbeInFlight = false
					state.ProbeID = 0
					state.ProbeLeaseUntil = time.Time{}
					result.ChangedItems++
					result.Capacity = state.Capacity
					if probeIdentity.RouteKey == "" || state.LastFailureAt.After(latestFailure) {
						probeIdentity = channelHealthIdentity{
							ChannelID: channel.Id, Fingerprint: fingerprint, RouteKey: routeKey,
							RouteLabel: state.RouteLabel, InitialCapacity: state.InitialCapacity,
						}
						probeIdentity.ChannelKey = fmt.Sprintf("%d:%s:all", channel.Id, fingerprint)
						latestFailure = state.LastFailureAt
					}
				}
			}
			shard.Unlock()
		}
		identity := probeIdentity
		if identity.RouteKey == "" {
			var ok bool
			identity, ok = defaultChannelProbeIdentity(channel, now)
			if !ok {
				return result, errors.New("channel has no supported route to probe")
			}
		}
		aggregateShard := channelAggregateHealthShardFor(identity.ChannelKey)
		aggregateShard.Lock()
		state := getAggregateHealthStateLocked(aggregateShard, identity, now)
		state.ProbeRevision++
		state.ProbeInFlight = false
		state.ProbeID = 0
		state.ProbeLeaseUntil = time.Time{}
		state.Suspect = false
		state.OpenUntil = now
		state.ProbeDue = now
		state.ProbeType = ChannelHealthProbeTypeRecovery
		state.ProbeScope = ChannelHealthProbeScopeChannel
		state.ProbeRouteLabel = identity.RouteLabel
		state.ProbeRouteKey = identity.RouteKey
		state.LastTouched = now
		persistAggregateHealthStateLocked(identity, state, persistentChannelHealthRecoveryPending)
		result.ChangedItems++
		aggregateShard.Unlock()

	case ChannelHealthRecoveryScopeKey:
		if request.KeyIndex == nil || *request.KeyIndex < 0 || *request.KeyIndex >= len(channel.GetKeys()) {
			return result, errors.New("valid key_index is required for key recovery")
		}
		fingerprint := channelConfigFingerprint(channel)
		memoryChannelHealth.Keys.Lock()
		for stateKey, state := range memoryChannelHealth.Keys.States {
			if state.ChannelID == channel.Id && state.Fingerprint == fingerprint && state.KeyIndex == *request.KeyIndex {
				delete(memoryChannelHealth.Keys.States, stateKey)
				result.ChangedItems++
			}
		}
		memoryChannelHealth.Keys.Unlock()

	default:
		return result, errors.New("scope must be channel, route, or key")
	}

	return result, nil
}

func ScheduleManualChannelRecovery(channel *model.Channel) bool {
	if channel == nil || channel.Id <= 0 {
		return false
	}
	if err := RestorePersistentChannelHealthForChannel(channel); err != nil {
		return false
	}
	_, err := RecoverChannelHealth(channel, ChannelHealthRecoveryRequest{Scope: ChannelHealthRecoveryScopeChannel})
	return err == nil
}

func SuspendChannelHealth(channel *model.Channel) {
	if channel == nil || channel.Id <= 0 {
		return
	}
	now := channelCircuitNow()
	fingerprint := channelConfigFingerprint(channel)
	var latestState channelRouteHealthState
	var latestIdentity channelHealthIdentity
	for index := range memoryChannelHealth.RouteShards {
		shard := &memoryChannelHealth.RouteShards[index]
		shard.Lock()
		for routeKey, state := range shard.Routes {
			if state.ChannelID != channel.Id || state.Fingerprint != fingerprint {
				continue
			}
			_, requestPath := splitChannelRouteLabel(state.RouteLabel)
			if ChannelHealthProbeSupportsPath(requestPath) && (latestIdentity.RouteKey == "" || state.LastTouched.After(latestState.LastTouched)) {
				latestState = *state
				latestIdentity = channelHealthIdentity{
					ChannelID: channel.Id, Fingerprint: fingerprint, RouteKey: routeKey,
					RouteLabel: state.RouteLabel, InitialCapacity: state.InitialCapacity,
				}
				latestIdentity.ChannelKey = fmt.Sprintf("%d:%s:all", channel.Id, fingerprint)
			}
			delete(shard.Routes, routeKey)
		}
		shard.Unlock()
	}
	if latestIdentity.RouteKey != "" {
		latestState.ProbeGeneration++
		latestState.ProbeInFlight = false
		latestState.ProbeID = 0
		latestState.ProbeLeaseUntil = time.Time{}
		latestState.ProbeType = ChannelHealthProbeTypeRecovery
		latestState.Suspect = false
		latestState.OpenUntil = now
		latestState.ProbeDue = now
		latestState.LastTouched = now
		persistRouteHealthStateLocked(latestIdentity, &latestState, persistentChannelHealthRecoveryPending)
	}
	identity := buildChannelHealthIdentity(channel, 0, "", "", now)
	aggregateShard := channelAggregateHealthShardFor(identity.ChannelKey)
	aggregateShard.Lock()
	delete(aggregateShard.States, identity.ChannelKey)
	aggregateShard.Unlock()
	memoryChannelHealth.Keys.Lock()
	for key, state := range memoryChannelHealth.Keys.States {
		if state.ChannelID == channel.Id && state.Fingerprint == fingerprint {
			delete(memoryChannelHealth.Keys.States, key)
		}
	}
	memoryChannelHealth.Keys.Unlock()
}
