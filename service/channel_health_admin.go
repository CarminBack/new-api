package service

import (
	"errors"
	"sort"
	"strings"
	"time"

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
	ModelName             string              `json:"model_name"`
	RequestPath           string              `json:"request_path"`
	State                 string              `json:"state"`
	OpenUntil             int64               `json:"open_until"`
	ProbeInFlight         bool                `json:"probe_in_flight"`
	InFlight              int                 `json:"in_flight"`
	Capacity              int                 `json:"capacity"`
	InitialCapacity       int                 `json:"initial_capacity"`
	Successes             int                 `json:"successes"`
	Failures              int                 `json:"failures"`
	PoolFailures          int                 `json:"pool_failures"`
	RateLimits            int                 `json:"rate_limits"`
	LastFailureClass      ChannelFailureClass `json:"last_failure_class,omitempty"`
	LastFailureReason     string              `json:"last_failure_reason,omitempty"`
	LastFailureStatusCode int                 `json:"last_failure_status_code,omitempty"`
	LastFailureAt         int64               `json:"last_failure_at,omitempty"`
	LastSuccessAt         int64               `json:"last_success_at,omitempty"`
	LastRecoveryAt        int64               `json:"last_recovery_at,omitempty"`
	LastTouched           int64               `json:"last_touched"`
	fingerprint           string
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
	ChannelProbeInFlight bool                         `json:"channel_probe_in_flight"`
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
	if now.Before(state.OpenUntil) {
		return ChannelHealthStateCircuitOpen
	}
	if state.ProbeInFlight && !state.OpenUntil.IsZero() {
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
				ModelName:             modelName,
				RequestPath:           requestPath,
				State:                 stateName,
				OpenUntil:             unixTime(state.OpenUntil),
				ProbeInFlight:         state.ProbeInFlight,
				InFlight:              state.InFlight,
				Capacity:              state.Capacity,
				InitialCapacity:       state.InitialCapacity,
				Successes:             successes,
				Failures:              failures,
				PoolFailures:          poolFailures,
				RateLimits:            rateLimits,
				LastFailureClass:      state.LastFailureClass,
				LastFailureReason:     state.LastFailureReason,
				LastFailureStatusCode: state.LastFailureStatusCode,
				LastFailureAt:         unixTime(state.LastFailureAt),
				LastSuccessAt:         unixTime(state.LastSuccessAt),
				LastRecoveryAt:        unixTime(state.LastRecoveryAt),
				LastTouched:           unixTime(state.LastTouched),
				fingerprint:           state.Fingerprint,
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
			if now.Before(state.OpenUntil) {
				stateName = ChannelHealthStateCircuitOpen
			} else if state.ProbeInFlight && !state.OpenUntil.IsZero() {
				stateName = ChannelHealthStateHalfOpen
			}
			if !includeHealthy && stateName == ChannelHealthStateHealthy {
				continue
			}
			entry := channelHealthSnapshotEntry(byChannel, state.ChannelID, state.Fingerprint)
			entry.ChannelState = stateName
			entry.ChannelOpenUntil = unixTime(state.OpenUntil)
			entry.ChannelProbeInFlight = state.ProbeInFlight
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
	state.Buckets = [channelHealthBucketCount]channelHealthBucket{}
	state.OpenUntil = time.Time{}
	state.ProbeInFlight = false
	state.SuccessesSinceIncrease = 0
	state.LastDecreaseEpoch = 0
	state.LastRecoveryAt = now
	state.LastTouched = now
	state.Capacity = channelHealthRecoveryCapacity
	if state.InitialCapacity > 0 && state.Capacity > state.InitialCapacity {
		state.Capacity = state.InitialCapacity
	}
	if state.Capacity < channelHealthMinCapacity {
		state.Capacity = channelHealthMinCapacity
	}
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
		identity := buildChannelHealthIdentity(channel, 0, request.ModelName, request.RequestPath, now)
		shard := channelHealthShardFor(identity.RouteKey)
		shard.Lock()
		if state := shard.Routes[identity.RouteKey]; state != nil {
			resetRouteHealthState(state, now)
			result.ChangedItems++
			result.Capacity = state.Capacity
		}
		shard.Unlock()

		aggregateShard := channelAggregateHealthShardFor(identity.ChannelKey)
		aggregateShard.Lock()
		if state := aggregateShard.States[identity.ChannelKey]; state != nil {
			if _, exists := state.UnhealthyRoutes[identity.RouteLabel]; exists {
				delete(state.UnhealthyRoutes, identity.RouteLabel)
				result.ChangedItems++
			}
			if len(state.UnhealthyRoutes) < 2 {
				state.OpenUntil = time.Time{}
				state.ProbeInFlight = false
			}
			state.LastTouched = now
		}
		aggregateShard.Unlock()

	case ChannelHealthRecoveryScopeChannel:
		fingerprint := channelConfigFingerprint(channel)
		for index := range memoryChannelHealth.RouteShards {
			shard := &memoryChannelHealth.RouteShards[index]
			shard.Lock()
			for _, state := range shard.Routes {
				if state.ChannelID == channel.Id && state.Fingerprint == fingerprint {
					resetRouteHealthState(state, now)
					result.ChangedItems++
					result.Capacity = state.Capacity
				}
			}
			shard.Unlock()
		}
		identity := buildChannelHealthIdentity(channel, 0, "", "", now)
		aggregateShard := channelAggregateHealthShardFor(identity.ChannelKey)
		aggregateShard.Lock()
		if state := aggregateShard.States[identity.ChannelKey]; state != nil {
			if !state.OpenUntil.IsZero() || state.ProbeInFlight || len(state.UnhealthyRoutes) > 0 {
				result.ChangedItems++
			}
			state.OpenUntil = time.Time{}
			state.ProbeInFlight = false
			state.UnhealthyRoutes = make(map[string]time.Time)
			state.LastTouched = now
		}
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
