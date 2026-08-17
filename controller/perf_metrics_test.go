package controller

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPerfMetricsTodayStartUsesServiceTimezone(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	now := time.Date(2026, time.August, 17, 23, 59, 59, 0, location)

	require.Equal(t,
		time.Date(2026, time.August, 17, 0, 0, 0, 0, location).Unix(),
		perfMetricsTodayStart(now),
	)
}
