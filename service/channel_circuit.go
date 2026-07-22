package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

const (
	channelCircuitNamespace = "new-api:channel_circuit:v1"
	channelCircuitWindow    = time.Minute
	channelCircuitProbeTTL  = 30 * time.Second
	channelCircuitStateTTL  = time.Hour
	channelOutageThreshold  = 5
	channelOutageOpenFor    = 5 * time.Minute

	ginKeyChannelCircuitProbe = "channel_circuit_probe"
)

var channelCircuitNow = time.Now

type channelCircuitPolicy struct {
	Threshold int
	OpenFor   time.Duration
}

type channelCircuitProbe struct {
	Keys map[string]struct{}
}

type memoryChannelCircuitState struct {
	Failures      int
	FailureExpiry time.Time
	OpenUntil     time.Time
	StateExpiry   time.Time
	ProbeUntil    time.Time
}

var memoryChannelCircuits = struct {
	sync.Mutex
	states map[string]memoryChannelCircuitState
}{states: make(map[string]memoryChannelCircuitState)}

var recordChannelCircuitFailureScript = redis.NewScript(`
local failures = redis.call('INCR', KEYS[1])
if failures == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
if failures >= tonumber(ARGV[2]) then
  redis.call('SET', KEYS[2], ARGV[3], 'PX', ARGV[4])
  redis.call('DEL', KEYS[3])
end
return failures
`)

func channelCircuitPolicyFor(class ChannelFailureClass) channelCircuitPolicy {
	switch class {
	case ChannelFailureChannelFatal:
		return channelCircuitPolicy{Threshold: 2, OpenFor: 10 * time.Minute}
	case ChannelFailureUncertain:
		return channelCircuitPolicy{Threshold: 2, OpenFor: 5 * time.Minute}
	default:
		return channelCircuitPolicy{Threshold: 5, OpenFor: 2 * time.Minute}
	}
}

func channelCircuitKey(channelID int, modelName string, requestPath string) string {
	sum := sha256.Sum256([]byte(modelName + "\x00" + requestPath))
	return fmt.Sprintf("%d:%s", channelID, hex.EncodeToString(sum[:8]))
}

func channelOutageCircuitKey(channelID int) string {
	return fmt.Sprintf("%d:all", channelID)
}

func channelCircuitRedisKeys(key string) (failureKey string, openKey string, probeKey string) {
	base := channelCircuitNamespace + ":" + key
	return base + ":fail", base + ":open", base + ":probe"
}

func channelCircuitRedisEnabled() bool {
	return common.RedisEnabled && common.RDB != nil
}

// AllowChannelCircuitAttempt returns false while the route is open. Once its
// cooldown expires, Redis SETNX (or a process-local lock) permits one probe.
func AllowChannelCircuitAttempt(c *gin.Context, channelID int, modelName string, requestPath string) bool {
	if channelID <= 0 {
		return true
	}
	acquiredProbes := make([]string, 0, 2)
	for _, key := range []string{channelOutageCircuitKey(channelID), channelCircuitKey(channelID, modelName, requestPath)} {
		allowed, probe, err := allowChannelCircuitAttemptRedis(key)
		if err != nil {
			allowed, probe = allowChannelCircuitAttemptMemory(key)
		}
		if !allowed {
			for _, acquiredKey := range acquiredProbes {
				clearCurrentChannelCircuitProbe(c, acquiredKey, true)
			}
			return false
		}
		if probe {
			markCurrentChannelCircuitProbe(c, key)
			acquiredProbes = append(acquiredProbes, key)
		}
	}
	return true
}

func allowChannelCircuitAttemptRedis(key string) (bool, bool, error) {
	if !channelCircuitRedisEnabled() {
		return false, false, errors.New("redis disabled")
	}
	_, openKey, probeKey := channelCircuitRedisKeys(key)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	raw, err := common.RDB.Get(ctx, openKey).Result()
	if errors.Is(err, redis.Nil) {
		return true, false, nil
	}
	if err != nil {
		return false, false, err
	}
	openUntilUnixMilli, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return false, false, err
	}
	if channelCircuitNow().Before(time.UnixMilli(openUntilUnixMilli)) {
		return false, false, nil
	}
	acquired, err := common.RDB.SetNX(ctx, probeKey, "1", channelCircuitProbeTTL).Result()
	if err != nil {
		return false, false, err
	}
	return acquired, acquired, nil
}

func allowChannelCircuitAttemptMemory(key string) (bool, bool) {
	now := channelCircuitNow()
	memoryChannelCircuits.Lock()
	defer memoryChannelCircuits.Unlock()

	state, ok := memoryChannelCircuits.states[key]
	if !ok || (!state.StateExpiry.IsZero() && !now.Before(state.StateExpiry)) {
		delete(memoryChannelCircuits.states, key)
		return true, false
	}
	if now.Before(state.OpenUntil) {
		return false, false
	}
	if state.OpenUntil.IsZero() {
		return true, false
	}
	if now.Before(state.ProbeUntil) {
		return false, false
	}
	state.ProbeUntil = now.Add(channelCircuitProbeTTL)
	memoryChannelCircuits.states[key] = state
	return true, true
}

func RecordChannelCircuitFailure(c *gin.Context, channelID int, modelName string, requestPath string, class ChannelFailureClass) {
	if channelID <= 0 {
		return
	}
	key := channelCircuitKey(channelID, modelName, requestPath)
	policy := channelCircuitPolicyFor(class)
	if isCurrentChannelCircuitProbe(c, key) {
		policy.Threshold = 1
	}
	_ = recordChannelCircuitFailureRedis(key, policy)
	recordChannelCircuitFailureMemory(key, policy)
	clearCurrentChannelCircuitProbe(c, key, false)

	outageKey := channelOutageCircuitKey(channelID)
	if class == ChannelFailureUncertain || isCurrentChannelCircuitProbe(c, outageKey) {
		outagePolicy := channelCircuitPolicy{Threshold: channelOutageThreshold, OpenFor: channelOutageOpenFor}
		if isCurrentChannelCircuitProbe(c, outageKey) {
			outagePolicy.Threshold = 1
		}
		_ = recordChannelCircuitFailureRedis(outageKey, outagePolicy)
		recordChannelCircuitFailureMemory(outageKey, outagePolicy)
		clearCurrentChannelCircuitProbe(c, outageKey, false)
	}
}

func isCurrentChannelCircuitProbe(c *gin.Context, key string) bool {
	if c == nil {
		return false
	}
	value, ok := c.Get(ginKeyChannelCircuitProbe)
	if !ok {
		return false
	}
	probe, ok := value.(channelCircuitProbe)
	if !ok {
		return false
	}
	_, ok = probe.Keys[key]
	return ok
}

func markCurrentChannelCircuitProbe(c *gin.Context, key string) {
	if c == nil {
		return
	}
	probe := channelCircuitProbe{Keys: make(map[string]struct{})}
	if value, ok := c.Get(ginKeyChannelCircuitProbe); ok {
		if current, ok := value.(channelCircuitProbe); ok && current.Keys != nil {
			probe = current
		}
	}
	probe.Keys[key] = struct{}{}
	c.Set(ginKeyChannelCircuitProbe, probe)
}

func recordChannelCircuitFailureRedis(key string, policy channelCircuitPolicy) error {
	if !channelCircuitRedisEnabled() {
		return errors.New("redis disabled")
	}
	failureKey, openKey, probeKey := channelCircuitRedisKeys(key)
	now := channelCircuitNow()
	openUntil := now.Add(policy.OpenFor)
	stateTTL := policy.OpenFor + channelCircuitStateTTL
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	return recordChannelCircuitFailureScript.Run(ctx, common.RDB, []string{failureKey, openKey, probeKey},
		channelCircuitWindow.Milliseconds(), policy.Threshold, openUntil.UnixMilli(), stateTTL.Milliseconds()).Err()
}

func recordChannelCircuitFailureMemory(key string, policy channelCircuitPolicy) {
	now := channelCircuitNow()
	memoryChannelCircuits.Lock()
	defer memoryChannelCircuits.Unlock()

	state := memoryChannelCircuits.states[key]
	if state.FailureExpiry.IsZero() || !now.Before(state.FailureExpiry) {
		state.Failures = 0
		state.FailureExpiry = now.Add(channelCircuitWindow)
	}
	state.Failures++
	if state.Failures >= policy.Threshold {
		state.OpenUntil = now.Add(policy.OpenFor)
		state.StateExpiry = state.OpenUntil.Add(channelCircuitStateTTL)
		state.ProbeUntil = time.Time{}
	}
	memoryChannelCircuits.states[key] = state
}

func RecordChannelCircuitSuccess(c *gin.Context, channelID int, modelName string, requestPath string) {
	if channelID <= 0 {
		return
	}
	keys := []string{channelCircuitKey(channelID, modelName, requestPath)}
	outageKey := channelOutageCircuitKey(channelID)
	if isCurrentChannelCircuitProbe(c, outageKey) {
		keys = append(keys, outageKey)
	}
	for _, key := range keys {
		_ = resetChannelCircuitRedis(key)
	}
	memoryChannelCircuits.Lock()
	for _, key := range keys {
		delete(memoryChannelCircuits.states, key)
	}
	memoryChannelCircuits.Unlock()
	for _, key := range keys {
		clearCurrentChannelCircuitProbe(c, key, true)
	}
}

func resetChannelCircuitRedis(key string) error {
	if !channelCircuitRedisEnabled() {
		return errors.New("redis disabled")
	}
	failureKey, openKey, probeKey := channelCircuitRedisKeys(key)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	return common.RDB.Del(ctx, failureKey, openKey, probeKey).Err()
}

// ReleaseChannelCircuitProbe makes a half-open route available for another
// probe when an attempt ended before reaching the upstream.
func ReleaseChannelCircuitProbe(c *gin.Context, channelID int, modelName string, requestPath string) {
	if channelID <= 0 {
		return
	}
	clearCurrentChannelCircuitProbe(c, channelCircuitKey(channelID, modelName, requestPath), true)
	clearCurrentChannelCircuitProbe(c, channelOutageCircuitKey(channelID), true)
}

func clearCurrentChannelCircuitProbe(c *gin.Context, key string, releaseProbe bool) {
	if c == nil {
		return
	}
	if !isCurrentChannelCircuitProbe(c, key) {
		return
	}
	value, _ := c.Get(ginKeyChannelCircuitProbe)
	probe, _ := value.(channelCircuitProbe)
	delete(probe.Keys, key)
	if len(probe.Keys) == 0 {
		c.Set(ginKeyChannelCircuitProbe, nil)
	} else {
		c.Set(ginKeyChannelCircuitProbe, probe)
	}
	if releaseProbe && channelCircuitRedisEnabled() {
		_, _, probeKey := channelCircuitRedisKeys(key)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_ = common.RDB.Del(ctx, probeKey).Err()
	}
	if releaseProbe {
		memoryChannelCircuits.Lock()
		if state, ok := memoryChannelCircuits.states[key]; ok {
			state.ProbeUntil = time.Time{}
			memoryChannelCircuits.states[key] = state
		}
		memoryChannelCircuits.Unlock()
	}
}
