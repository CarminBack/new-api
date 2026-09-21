package service

import (
	"fmt"
	"hash/fnv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
)

const (
	persistentChannelHealthSuspect         = "suspect"
	persistentChannelHealthProbing         = "probing"
	persistentChannelHealthOpen            = "open"
	persistentChannelHealthRecoveryPending = "recovery_pending"
)

var channelHealthPersistenceEnabled = true

func persistentTime(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.Unix()
}

func persistRouteHealthStateLocked(identity channelHealthIdentity, state *channelRouteHealthState, persistentState string) {
	if !channelHealthPersistenceEnabled || model.DB == nil || state == nil {
		return
	}
	nextProbeAt := state.ProbeDue
	if !state.OpenUntil.IsZero() {
		nextProbeAt = state.OpenUntil
	}
	modelName, requestPath := splitChannelRouteLabel(state.RouteLabel)
	record := &model.ChannelHealthState{
		ScopeKey:               identity.RouteKey,
		ChannelID:              state.ChannelID,
		Fingerprint:            state.Fingerprint,
		Scope:                  string(ChannelHealthProbeScopeRoute),
		ModelName:              modelName,
		RequestPath:            requestPath,
		State:                  persistentState,
		Reason:                 state.LastFailureReason,
		OpenedAt:               persistentTime(state.LastFailureAt),
		NextProbeAt:            persistentTime(nextProbeAt),
		Revision:               state.ProbeGeneration,
		ProbeID:                state.ProbeID,
		ProbeType:              string(state.ProbeType),
		ProbeLeaseEnd:          persistentTime(state.ProbeLeaseUntil),
		RecoveryTargetCapacity: state.RecoveryTargetCapacity,
		RecoveryCapacity:       state.Capacity,
		RecoverySuccesses:      state.RecoverySuccesses,
		RecoveryStartedAt:      persistentTime(state.LastRecoveryAt),
		ProbeFailures:          state.ProbeFailures,
	}
	if err := model.SaveChannelHealthState(record); err != nil {
		logger.LogError(nil, fmt.Sprintf("persist route health state failed: channel #%d: %v", state.ChannelID, err))
	}
}

func persistAggregateHealthStateLocked(identity channelHealthIdentity, state *channelAggregateHealthState, persistentState string) {
	if !channelHealthPersistenceEnabled || model.DB == nil || state == nil {
		return
	}
	nextProbeAt := state.ProbeDue
	if !state.OpenUntil.IsZero() {
		nextProbeAt = state.OpenUntil
	}
	modelName, requestPath := splitChannelRouteLabel(state.ProbeRouteLabel)
	record := &model.ChannelHealthState{
		ScopeKey:      identity.ChannelKey,
		ChannelID:     state.ChannelID,
		Fingerprint:   state.Fingerprint,
		Scope:         string(ChannelHealthProbeScopeChannel),
		ModelName:     modelName,
		RequestPath:   requestPath,
		State:         persistentState,
		Reason:        state.LastFailureReason,
		OpenedAt:      persistentTime(state.LastFailureAt),
		NextProbeAt:   persistentTime(nextProbeAt),
		Revision:      state.ProbeRevision,
		ProbeID:       state.ProbeID,
		ProbeType:     string(state.ProbeType),
		ProbeLeaseEnd: persistentTime(state.ProbeLeaseUntil),
		ProbeFailures: state.ProbeFailures,
	}
	if err := model.SaveChannelHealthState(record); err != nil {
		logger.LogError(nil, fmt.Sprintf("persist channel health state failed: channel #%d: %v", state.ChannelID, err))
	}
}

func deletePersistentHealthState(scopeKey string) {
	if !channelHealthPersistenceEnabled || model.DB == nil || scopeKey == "" {
		return
	}
	if err := model.DeleteChannelHealthState(scopeKey); err != nil {
		logger.LogError(nil, fmt.Sprintf("delete channel health state failed: %v", err))
	}
}

func deletePersistentChannelHealthStates(channelID int) {
	if !channelHealthPersistenceEnabled || model.DB == nil || channelID <= 0 {
		return
	}
	if err := model.DeleteChannelHealthStatesForChannel(channelID); err != nil {
		logger.LogError(nil, fmt.Sprintf("delete channel health states failed: channel #%d: %v", channelID, err))
	}
}

func startupProbeJitter(scopeKey string) time.Duration {
	h := fnv.New32a()
	_, _ = h.Write([]byte(scopeKey))
	return time.Duration(h.Sum32()%11) * time.Second
}

func restoredProbeDue(record model.ChannelHealthState, now time.Time) time.Time {
	due := time.Unix(record.NextProbeAt, 0)
	if record.NextProbeAt <= 0 || !due.After(now) {
		return now.Add(startupProbeJitter(record.ScopeKey))
	}
	return due
}

func RestorePersistentChannelHealth() error {
	if !channelHealthPersistenceEnabled || model.DB == nil {
		return nil
	}
	records, err := model.ListChannelHealthStates()
	if err != nil {
		return err
	}
	now := channelCircuitNow()
	for _, record := range records {
		if !ChannelHealthProbeSupportsPath(record.RequestPath) {
			_ = model.DeleteChannelHealthState(record.ScopeKey)
			continue
		}
		channel, channelErr := model.CacheGetChannel(record.ChannelID)
		if channelErr != nil || channel == nil {
			continue
		}
		if channelConfigFingerprint(channel) != record.Fingerprint {
			_ = model.DeleteChannelHealthState(record.ScopeKey)
			continue
		}
		if channel.Status != common.ChannelStatusEnabled {
			continue
		}
		restorePersistentChannelHealthRecord(record, channel, now)
	}
	return nil
}

func restorePersistentChannelHealthRecord(record model.ChannelHealthState, channel *model.Channel, now time.Time) {
	if !ChannelHealthProbeSupportsPath(record.RequestPath) {
		return
	}
	identity := buildChannelHealthIdentity(channel, 0, record.ModelName, record.RequestPath, now)
	probeType := ChannelHealthProbeType(record.ProbeType)
	if probeType == "" {
		probeType = ChannelHealthProbeTypeInitial
	}
	if record.Scope == string(ChannelHealthProbeScopeChannel) {
		due := restoredProbeDue(record, now)
		shard := channelAggregateHealthShardFor(identity.ChannelKey)
		shard.Lock()
		state := getAggregateHealthStateLocked(shard, identity, now)
		state.ProbeRevision = record.Revision
		state.ProbeType = probeType
		state.ProbeScope = ChannelHealthProbeScopeChannel
		state.ProbeRouteLabel = identity.RouteLabel
		state.ProbeRouteKey = identity.RouteKey
		state.ProbeFailures = max(record.ProbeFailures, 0)
		state.LastFailureReason = record.Reason
		if record.OpenedAt > 0 {
			state.LastFailureAt = time.Unix(record.OpenedAt, 0)
		}
		if record.State == persistentChannelHealthOpen || probeType == ChannelHealthProbeTypeRecovery {
			state.OpenUntil = due
			state.ProbeType = ChannelHealthProbeTypeRecovery
		} else {
			state.Suspect = true
			state.ProbeDue = due
		}
		shard.Unlock()
		return
	}

	shard := channelHealthShardFor(identity.RouteKey)
	shard.Lock()
	state := getRouteHealthStateLocked(shard, identity, now)
	if record.State == persistentChannelHealthRecoveryPending && record.NextProbeAt <= 0 {
		target := record.RecoveryTargetCapacity
		if target < channelHealthMinCapacity {
			target = identity.InitialCapacity
		}
		if target > channelHealthMaxCapacity {
			target = channelHealthMaxCapacity
		}
		capacity := record.RecoveryCapacity
		if capacity < channelHealthMinCapacity {
			capacity = channelHealthMinCapacity
		}
		if capacity > target {
			capacity = target
		}
		recoverySuccesses := record.RecoverySuccesses
		if recoverySuccesses < 0 {
			recoverySuccesses = 0
		}
		lastRecoveryAt := now
		if record.RecoveryStartedAt > 0 {
			lastRecoveryAt = time.Unix(record.RecoveryStartedAt, 0)
		}
		state.OpenUntil = time.Time{}
		state.Suspect = false
		state.ProbeDue = time.Time{}
		state.ProbeInFlight = false
		state.ProbeID = 0
		state.ProbeType = ""
		state.ProbeLeaseUntil = time.Time{}
		state.ProbeGeneration = record.Revision
		state.CapacityBeforeOpen = target
		state.RecoveryTargetCapacity = target
		state.Capacity = capacity
		state.RecoverySuccesses = recoverySuccesses
		state.RecoveryFailures = 0
		state.ProbeFailures = 0
		state.LastRecoveryAt = lastRecoveryAt
		state.LastFailureReason = record.Reason
		if record.OpenedAt > 0 {
			state.LastFailureAt = time.Unix(record.OpenedAt, 0)
		}
		state.LastTouched = now
		shard.Unlock()
		return
	}
	due := restoredProbeDue(record, now)
	state.ProbeGeneration = record.Revision
	state.ProbeType = probeType
	state.ProbeFailures = max(record.ProbeFailures, 0)
	state.LastFailureReason = record.Reason
	if record.OpenedAt > 0 {
		state.LastFailureAt = time.Unix(record.OpenedAt, 0)
	}
	if record.State == persistentChannelHealthOpen || probeType == ChannelHealthProbeTypeRecovery {
		state.OpenUntil = due
		state.ProbeDue = due
		state.ProbeType = ChannelHealthProbeTypeRecovery
	} else {
		state.Suspect = true
		state.ProbeDue = due
	}
	shard.Unlock()
}

func RestorePersistentChannelHealthForChannel(channel *model.Channel) error {
	if !channelHealthPersistenceEnabled || model.DB == nil || channel == nil || channel.Id <= 0 {
		return nil
	}
	records, err := model.ListChannelHealthStates()
	if err != nil {
		return err
	}
	now := channelCircuitNow()
	fingerprint := channelConfigFingerprint(channel)
	for _, record := range records {
		if record.ChannelID != channel.Id {
			continue
		}
		if !ChannelHealthProbeSupportsPath(record.RequestPath) {
			_ = model.DeleteChannelHealthState(record.ScopeKey)
			continue
		}
		if record.Fingerprint != fingerprint {
			_ = model.DeleteChannelHealthState(record.ScopeKey)
			continue
		}
		restorePersistentChannelHealthRecord(record, channel, now)
	}
	return nil
}
