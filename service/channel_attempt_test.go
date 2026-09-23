package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newAttemptContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return c
}

// A failed attempt is recorded with its outcome and retry decision so the shop
// owner can read the routing flow from the log details.
func TestChannelRouteAttemptRecordsFailureDecision(t *testing.T) {
	c := newAttemptContext(t)
	BeginChannelRouteAttempt(c, 42, 1)
	_, err := c.Writer.Write([]byte("boom"))
	require.NoError(t, err)
	FinishChannelRouteAttempt(c, http.StatusBadGateway, ChannelFailureDecision{
		Class:  ChannelFailureTransient,
		Reason: "retryable_status",
		Retry:  true,
	})

	attempts := GetChannelRouteAttempts(c, false)
	require.Len(t, attempts, 1)
	assert.Equal(t, 42, attempts[0].ChannelID)
	assert.Equal(t, 1, attempts[0].KeyIndex)
	assert.Equal(t, http.StatusBadGateway, attempts[0].StatusCode)
	assert.Equal(t, ChannelFailureTransient, attempts[0].Class)
	assert.True(t, attempts[0].Retry)
	assert.Equal(t, "retryable_status", attempts[0].Reason)
	assert.True(t, attempts[0].ResponseStarted)
}

// A successful attempt is closed via the success helper and carries no retry.
func TestChannelRouteAttemptSuccess(t *testing.T) {
	c := newAttemptContext(t)
	BeginChannelRouteAttempt(c, 7, 0)
	FinishSuccessfulChannelRouteAttempt(c)

	attempts := GetChannelRouteAttempts(c, false)
	require.Len(t, attempts, 1)
	assert.Equal(t, 7, attempts[0].ChannelID)
	assert.False(t, attempts[0].Retry)
	assert.Equal(t, "success", attempts[0].Reason)
}

// The in-flight attempt is never left behind: finishing clears it.
func TestChannelRouteAttemptClearsCurrentAfterFinish(t *testing.T) {
	c := newAttemptContext(t)
	BeginChannelRouteAttempt(c, 1, 0)
	FinishChannelRouteAttempt(c, http.StatusInternalServerError, ChannelFailureDecision{Class: ChannelFailureTransient})

	// No double counting if finish runs again.
	FinishChannelRouteAttempt(c, http.StatusInternalServerError, ChannelFailureDecision{Class: ChannelFailureTransient})
	assert.Len(t, GetChannelRouteAttempts(c, false), 1)
}

// Admin metadata exposure is bounded and never contains keys or bodies.
func TestChannelRouteAttemptsAdminInfoIsBounded(t *testing.T) {
	c := newAttemptContext(t)
	for i := 0; i < maxLoggedChannelAttempts+5; i++ {
		BeginChannelRouteAttempt(c, i+1, 0)
		FinishChannelRouteAttempt(c, http.StatusInternalServerError, ChannelFailureDecision{Class: ChannelFailureTransient})
	}
	adminInfo := map[string]interface{}{}
	AppendChannelRouteAttemptsAdminInfo(c, adminInfo, false)
	attempts, ok := adminInfo["route_attempts"].([]ChannelRouteAttempt)
	require.True(t, ok)
	assert.Len(t, attempts, maxLoggedChannelAttempts, "attempt metadata must be bounded")
}

// includeCurrent snapshots an in-flight attempt as a success for consume logs.
func TestChannelRouteAttemptsIncludeCurrent(t *testing.T) {
	c := newAttemptContext(t)
	BeginChannelRouteAttempt(c, 9, 0)

	withoutCurrent := GetChannelRouteAttempts(c, false)
	assert.Empty(t, withoutCurrent)

	withCurrent := GetChannelRouteAttempts(c, true)
	require.Len(t, withCurrent, 1)
	assert.Equal(t, 9, withCurrent[0].ChannelID)
	assert.Equal(t, "success", withCurrent[0].Reason)
}

// Nil-context guards keep the helpers safe on early-return paths.
func TestChannelRouteAttemptNilContext(t *testing.T) {
	require.NotPanics(t, func() {
		BeginChannelRouteAttempt(nil, 1, 0)
		FinishChannelRouteAttempt(nil, 0, ChannelFailureDecision{})
		FinishSuccessfulChannelRouteAttempt(nil)
		AppendChannelRouteAttemptsAdminInfo(nil, nil, false)
		assert.Nil(t, GetChannelRouteAttempts(nil, false))
	})
	// A zero channel id is not tracked.
	c := newAttemptContext(t)
	BeginChannelRouteAttempt(c, 0, 0)
	assert.Empty(t, GetChannelRouteAttempts(c, false))
}
