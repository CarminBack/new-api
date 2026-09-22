package cachex

import (
	"context"
	"github.com/go-redis/redis/v8"
	"reflect"
	"strconv"
	"time"
)

var deleteIfUnchangedScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
return redis.call('DEL', KEYS[1])
`)

// DeleteIfUnchanged prevents a late failure from deleting another request's binding.
func (c *HybridCache[V]) DeleteIfUnchanged(key string, expected V) (bool, error) {
	full := c.ns.FullKey(key)
	if full == "" {
		return false, nil
	}
	if c.redisOn() {
		raw, err := c.redisCodec.Encode(expected)
		if err != nil {
			return false, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), defaultRedisOpTimeout)
		defer cancel()
		result, err := deleteIfUnchangedScript.Run(ctx, c.redis, []string{full}, raw).Int64()
		return result == 1, err
	}
	c.compareMu.Lock()
	defer c.compareMu.Unlock()
	current, found, err := c.memCache().Get(full)
	if err != nil || !found || !reflect.DeepEqual(current, expected) {
		return false, err
	}
	return c.memCache().DeleteMany([]string{full})[full], nil
}

var setIfUnchangedScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if ARGV[1] == '1' then
  if current == false or current ~= ARGV[2] then return 0 end
else
  if current ~= false then return 0 end
end
redis.call('SET', KEYS[1], ARGV[3], 'PX', ARGV[4])
return 1
`)

// SetIfUnchangedWithTTL updates a value only when the cache still contains the
// value observed by the caller. Redis performs the check and write atomically;
// the in-memory fallback serializes conditional updates locally.
func (c *HybridCache[V]) SetIfUnchangedWithTTL(key string, expected V, expectedFound bool, value V, ttl time.Duration) (bool, error) {
	full := c.ns.FullKey(key)
	if full == "" {
		return false, nil
	}
	if c.redisOn() {
		expectedRaw, err := c.redisCodec.Encode(expected)
		if err != nil {
			return false, err
		}
		valueRaw, err := c.redisCodec.Encode(value)
		if err != nil {
			return false, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), defaultRedisOpTimeout)
		defer cancel()
		expectedFoundArg := "0"
		if expectedFound {
			expectedFoundArg = "1"
		}
		ttlMillis := ttl.Milliseconds()
		if ttlMillis < 1 {
			ttlMillis = 1
		}
		result, err := setIfUnchangedScript.Run(ctx, c.redis, []string{full}, expectedFoundArg, expectedRaw, valueRaw, strconv.FormatInt(ttlMillis, 10)).Result()
		if err != nil {
			return false, err
		}
		matched, ok := result.(int64)
		return ok && matched == 1, nil
	}

	c.compareMu.Lock()
	defer c.compareMu.Unlock()
	current, found, err := c.memCache().Get(full)
	if err != nil {
		return false, err
	}
	if found != expectedFound || (found && !reflect.DeepEqual(current, expected)) {
		return false, nil
	}
	c.memCache().SetWithTTL(full, value, ttl)
	return true, nil
}
