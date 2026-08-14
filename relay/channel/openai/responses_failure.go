package openai

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/gin-gonic/gin"
)

const upstreamFailureMessageLimit = 512

// recordResponsesUpstreamFailure keeps only bounded, sanitized failure fields
// for admin diagnostics. The request body and credentials are intentionally not
// included.
func recordResponsesUpstreamFailure(c *gin.Context, info *relaycommon.RelayInfo, event dto.ResponsesStreamResponse, downstreamStarted bool) {
	if c == nil {
		return
	}
	diagnostic := map[string]interface{}{
		"event":                 event.Type,
		"downstream_started":    downstreamStarted,
		"actual_output_started": downstreamStarted,
	}
	if info != nil {
		diagnostic["received_event_count"] = info.ReceivedResponseCount
	}
	if event.Response != nil {
		if event.Response.ID != "" {
			diagnostic["response_id"] = event.Response.ID
		}
		if oaiErr := event.Response.GetOpenAIError(); oaiErr != nil && oaiErr.Type != "" {
			diagnostic["error_present"] = true
			diagnostic["type"] = boundedFailureValue(oaiErr.Type)
			if code := strings.TrimSpace(fmt.Sprint(oaiErr.Code)); code != "" && code != "<nil>" {
				diagnostic["code"] = boundedFailureValue(code)
			}
			if message := boundedFailureMessage(oaiErr.Message); message != "" {
				diagnostic["message"] = message
			}
			if param := boundedFailureValue(oaiErr.Param); param != "" {
				diagnostic["param"] = param
			}
		} else {
			diagnostic["error_present"] = false
		}
	} else {
		diagnostic["error_present"] = false
	}
	if upstreamRequestID := strings.TrimSpace(c.GetString(common.UpstreamRequestIdKey)); upstreamRequestID != "" {
		diagnostic["upstream_request_id"] = boundedFailureValue(upstreamRequestID)
	}
	common.SetContextKey(c, constant.ContextKeyUpstreamFailure, diagnostic)
}

func boundedFailureValue(value string) string {
	return truncateFailureValue(common.MaskSensitiveInfo(strings.TrimSpace(value)))
}

func boundedFailureMessage(message string) string {
	return boundedFailureValue(message)
}

func truncateFailureValue(value string) string {
	if len(value) <= upstreamFailureMessageLimit {
		return value
	}
	return value[:upstreamFailureMessageLimit] + "..."
}
