package service

import (
	"time"

	"github.com/gin-gonic/gin"
)

const (
	ginKeyChannelAttemptCurrent = "channel_route_attempt_current"
	ginKeyChannelAttempts       = "channel_route_attempts"
	maxLoggedChannelAttempts    = 8
)

type ChannelRouteAttempt struct {
	ChannelID       int                 `json:"channel_id"`
	KeyIndex        int                 `json:"key_index"`
	StatusCode      int                 `json:"status_code"`
	DurationMS      int64               `json:"duration_ms"`
	ResponseStarted bool                `json:"response_started"`
	Class           ChannelFailureClass `json:"class"`
	Retry           bool                `json:"retry"`
	Reason          string              `json:"reason"`
	startedAt       time.Time
}

func BeginChannelRouteAttempt(c *gin.Context, channelID int, keyIndex int) {
	if c == nil || channelID <= 0 {
		return
	}
	c.Set(ginKeyChannelAttemptCurrent, ChannelRouteAttempt{
		ChannelID: channelID,
		KeyIndex:  keyIndex,
		startedAt: channelCircuitNow(),
	})
}

func FinishChannelRouteAttempt(c *gin.Context, statusCode int, decision ChannelFailureDecision) {
	if c == nil {
		return
	}
	value, ok := c.Get(ginKeyChannelAttemptCurrent)
	if !ok {
		return
	}
	attempt, ok := value.(ChannelRouteAttempt)
	if !ok {
		return
	}
	attempt.StatusCode = statusCode
	attempt.DurationMS = channelCircuitNow().Sub(attempt.startedAt).Milliseconds()
	if attempt.DurationMS < 0 {
		attempt.DurationMS = 0
	}
	attempt.Class = decision.Class
	attempt.Retry = decision.Retry
	attempt.Reason = decision.Reason
	if c.Writer != nil {
		attempt.ResponseStarted = c.Writer.Written()
	}
	appendChannelRouteAttempt(c, attempt)
	c.Set(ginKeyChannelAttemptCurrent, nil)
}

func FinishSuccessfulChannelRouteAttempt(c *gin.Context) {
	statusCode := 0
	if c != nil && c.Writer != nil {
		statusCode = c.Writer.Status()
	}
	FinishChannelRouteAttempt(c, statusCode, ChannelFailureDecision{
		Class:  "success",
		Reason: "success",
	})
}

func appendChannelRouteAttempt(c *gin.Context, attempt ChannelRouteAttempt) {
	attempt.startedAt = time.Time{}
	attempts := GetChannelRouteAttempts(c, false)
	if len(attempts) >= maxLoggedChannelAttempts {
		return
	}
	attempts = append(attempts, attempt)
	c.Set(ginKeyChannelAttempts, attempts)
}

// GetChannelRouteAttempts returns a copy suitable for admin-only log metadata.
// includeCurrent snapshots an in-flight attempt as successful for consume logs,
// which can be written before the controller regains control from the relay.
func GetChannelRouteAttempts(c *gin.Context, includeCurrent bool) []ChannelRouteAttempt {
	if c == nil {
		return nil
	}
	var attempts []ChannelRouteAttempt
	if value, ok := c.Get(ginKeyChannelAttempts); ok {
		if stored, ok := value.([]ChannelRouteAttempt); ok {
			attempts = append(attempts, stored...)
		}
	}
	if !includeCurrent || len(attempts) >= maxLoggedChannelAttempts {
		return attempts
	}
	value, ok := c.Get(ginKeyChannelAttemptCurrent)
	if !ok {
		return attempts
	}
	current, ok := value.(ChannelRouteAttempt)
	if !ok {
		return attempts
	}
	if c.Writer != nil {
		current.StatusCode = c.Writer.Status()
	}
	current.DurationMS = channelCircuitNow().Sub(current.startedAt).Milliseconds()
	if current.DurationMS < 0 {
		current.DurationMS = 0
	}
	current.Class = "success"
	current.Reason = "success"
	if c.Writer != nil {
		current.ResponseStarted = c.Writer.Written()
	}
	current.startedAt = time.Time{}
	return append(attempts, current)
}

func AppendChannelRouteAttemptsAdminInfo(c *gin.Context, adminInfo map[string]interface{}, includeCurrent bool) {
	if adminInfo == nil {
		return
	}
	if attempts := GetChannelRouteAttempts(c, includeCurrent); len(attempts) > 0 {
		adminInfo["route_attempts"] = attempts
	}
}
