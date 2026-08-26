package common

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitUpstreamUsageLog(t *testing.T) {
	originalDeadline := upstreamUsageLogDeadline.Load()
	t.Cleanup(func() { upstreamUsageLogDeadline.Store(originalDeadline) })

	now := time.Now()
	t.Setenv("UPSTREAM_USAGE_LOG_DURATION", "30m")
	initUpstreamUsageLog(now)

	deadline := upstreamUsageLogDeadline.Load()
	require.NotZero(t, deadline)
	assert.Equal(t, now.Add(30*time.Minute).UnixNano(), deadline)
	assert.True(t, isUpstreamUsageLogEnabledAt(now.Add(29*time.Minute)))
	assert.False(t, isUpstreamUsageLogEnabledAt(now.Add(30*time.Minute)))
}

func TestInitUpstreamUsageLogRejectsInvalidDuration(t *testing.T) {
	originalDeadline := upstreamUsageLogDeadline.Load()
	t.Cleanup(func() { upstreamUsageLogDeadline.Store(originalDeadline) })

	t.Setenv("UPSTREAM_USAGE_LOG_DURATION", "invalid")
	initUpstreamUsageLog(time.Now())

	assert.Zero(t, upstreamUsageLogDeadline.Load())
}
