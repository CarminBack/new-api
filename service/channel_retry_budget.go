package service

import (
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	channelRetryBudgetWindow       = 2 * time.Minute
	channelRetryBudgetBucket       = 5 * time.Second
	channelRetryBudgetBucketCount  = int(channelRetryBudgetWindow / channelRetryBudgetBucket)
	channelRetryBudgetRatio        = 0.10
	channelRetryBudgetMinimumBurst = 10
	ginKeyChannelPrimaryRecorded   = "channel_retry_primary_recorded"
)

type channelRetryBudgetBucketState struct {
	Epoch   int64
	Primary int
	Retries int
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
		*bucket = channelRetryBudgetBucketState{Epoch: epoch}
	}
	return bucket
}

func summarizeRetryBudgetLocked(now time.Time) (primary int, retries int) {
	currentEpoch := retryBudgetEpoch(now)
	for _, bucket := range channelRetryBudgetState.Buckets {
		if bucket.Epoch <= 0 || currentEpoch-bucket.Epoch < 0 || currentEpoch-bucket.Epoch >= int64(channelRetryBudgetBucketCount) {
			continue
		}
		primary += bucket.Primary
		retries += bucket.Retries
	}
	return primary, retries
}

func RecordChannelPrimaryRequest(c *gin.Context) {
	if c != nil {
		if recorded, ok := c.Get(ginKeyChannelPrimaryRecorded); ok && recorded == true {
			return
		}
		c.Set(ginKeyChannelPrimaryRecorded, true)
	}
	now := channelCircuitNow()
	channelRetryBudgetState.Lock()
	currentRetryBudgetBucketLocked(now).Primary++
	channelRetryBudgetState.Unlock()
}

func AllowChannelRetry() bool {
	now := channelCircuitNow()
	channelRetryBudgetState.Lock()
	defer channelRetryBudgetState.Unlock()
	primary, retries := summarizeRetryBudgetLocked(now)
	allowance := int(float64(primary) * channelRetryBudgetRatio)
	if allowance < channelRetryBudgetMinimumBurst {
		allowance = channelRetryBudgetMinimumBurst
	}
	if retries >= allowance {
		return false
	}
	currentRetryBudgetBucketLocked(now).Retries++
	return true
}

func resetChannelRetryBudgetForTest() {
	channelRetryBudgetState.Lock()
	channelRetryBudgetState.Buckets = [channelRetryBudgetBucketCount]channelRetryBudgetBucketState{}
	channelRetryBudgetState.Unlock()
}
