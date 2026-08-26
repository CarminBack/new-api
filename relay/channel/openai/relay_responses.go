package openai

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func OaiResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	// read response body
	var responsesResponse dto.OpenAIResponsesResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	err = common.Unmarshal(responseBody, &responsesResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if common.DebugEnabled {
		rawUsage := gjson.GetBytes(responseBody, "usage")
		if rawUsage.Exists() {
			logger.LogDebug(c, "openai responses upstream usage: %s", rawUsage.Raw)
		} else {
			logger.LogDebug(c, "openai responses upstream usage: <missing>")
		}
	}
	if oaiError := responsesResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	// 写入新的 response body
	service.IOCopyBytesGracefully(c, resp, responseBody)

	// compute usage
	usage, _ := responsesUsage(&responsesResponse)
	if info != nil {
		// Count actual tool invocations from Output, not request declarations.
		for _, output := range responsesResponse.Output {
			switch output.Type {
			case dto.BuildInCallWebSearchCall:
				info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
			case dto.BuildInCallFileSearchCall:
				info.CountBillableToolCall(dto.BuildInCallFileSearchCall, "")
			case dto.BuildInCallFunctionCall:
				info.CountBillableToolCall(dto.BuildInCallFunctionCall, output.Name)
			}
		}

		imageCounter := &relaycommon.ImageGenerationCallCounter{}
		if !relaycommon.IsNonBillableResponsesStatus(responsesResponse.Status) {
			for i := range responsesResponse.Output {
				idx := i
				imageCounter.Observe(&responsesResponse.Output[i], &idx)
			}
		}
		imageCounter.Commit(info)
	}
	return usage, nil
}

func responsesUsage(response *dto.OpenAIResponsesResponse) (*dto.Usage, bool) {
	usage := &dto.Usage{}
	if response == nil || response.Usage == nil {
		return usage, false
	}

	*usage = *response.Usage
	usage.PromptTokens = response.Usage.InputTokens
	usage.CompletionTokens = response.Usage.OutputTokens
	if response.Usage.TotalTokens != 0 {
		usage.TotalTokens = response.Usage.TotalTokens
	} else {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}
	if response.Usage.InputTokensDetails != nil {
		usage.PromptTokensDetails = *response.Usage.InputTokensDetails
		inputDetails := *response.Usage.InputTokensDetails
		usage.InputTokensDetails = &inputDetails
	}
	usage.UsageSemantic = dto.BillingUsageSemanticOpenAI
	usage.UsageSource = dto.BillingUsageSourceOAIResponses
	usage.BillingUsage = dto.NewOpenAIResponsesBillingUsage(usage)
	return usage, true
}

func OaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, "invalid response or response body")
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	defer service.CloseResponseBodyGracefully(resp)
	common.SetContextKey(c, constant.ContextKeyStreamResponseTracking, true)
	common.SetContextKey(c, constant.ContextKeyStreamDownstreamStarted, false)
	common.SetContextKey(c, constant.ContextKeyStreamActualOutputStarted, false)
	info.SendResponseCount = 0
	info.ReceivedResponseCount = 0
	info.StreamTerminalEvent = ""
	info.StreamUsagePresent = false
	info.StreamDownstreamStarted = false

	var usage = &dto.Usage{}
	var responseTextBuilder strings.Builder
	var finalResponse *dto.OpenAIResponsesResponse
	var streamErr *types.NewAPIError
	imageCounter := &relaycommon.ImageGenerationCallCounter{}
	imageCommitted := false
	type pendingEvent struct {
		response dto.ResponsesStreamResponse
		data     string
	}
	pending := make([]pendingEvent, 0, 3)

	streamWriteError := func(err error) *types.NewAPIError {
		return types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
	}
	flushPending := func() bool {
		for _, event := range pending {
			if err := sendResponsesStreamData(c, info, event.response, event.data); err != nil {
				streamErr = streamWriteError(err)
				return false
			}
		}
		pending = pending[:0]
		return true
	}
	streamDownstreamStarted := func() bool {
		return info.SendResponseCount > 0 ||
			common.GetContextKeyBool(c, constant.ContextKeyStreamDownstreamStarted)
	}
	streamActualOutputStarted := func() bool {
		return common.GetContextKeyBool(c, constant.ContextKeyStreamActualOutputStarted)
	}
	streamHasUpstreamOutput := func() bool {
		return streamDownstreamStarted() || responseTextBuilder.Len() > 0 ||
			(finalResponse != nil && len(finalResponse.Output) > 0)
	}
	estimateStreamUsage := func() *dto.Usage {
		responseText := responseTextBuilder.String()
		if responseText == "" {
			responseText = service.ExtractOutputTextFromResponses(finalResponse)
		}
		return service.ResponseText2Usage(c, responseText, info.UpstreamModelName, info.GetEstimatePromptTokens())
	}

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		var streamResponse dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
			logger.LogError(c, "failed to unmarshal stream response: "+err.Error())
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway)
			if streamDownstreamStarted() {
				streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
			}
			sr.Stop(streamErr)
			return
		}

		switch streamResponse.Type {
		case "response.completed", "response.done", "response.incomplete":
			info.StreamTerminalEvent = streamResponse.Type
			finalResponse = streamResponse.Response
			// Keep the raw upstream usage shape available for diagnosing provider
			// fields that are not represented by the typed DTO. This is gated by
			// DEBUG and intentionally logs usage only, never prompt or output data.
			if common.DebugEnabled {
				rawUsage := gjson.Get(data, "response.usage")
				if rawUsage.Exists() {
					logger.LogDebug(c, "openai responses upstream usage: %s", rawUsage.Raw)
				} else {
					logger.LogDebug(c, "openai responses upstream usage: <missing>")
				}
			}
			usage, info.StreamUsagePresent = responsesUsage(finalResponse)
			if !streamHasUpstreamOutput() && !info.StreamUsagePresent {
				streamErr = types.NewOpenAIError(
					fmt.Errorf("empty responses stream: terminal event %s contained no usage or output", streamResponse.Type),
					types.ErrorCodeBadResponse,
					http.StatusBadGateway,
				)
				sr.Stop(streamErr)
				return
			}
			if !imageCommitted {
				if finalResponse == nil || relaycommon.IsNonBillableResponsesStatus(finalResponse.Status) {
					imageCounter.Reset()
				} else {
					for i := range finalResponse.Output {
						idx := i
						imageCounter.Observe(&finalResponse.Output[i], &idx)
					}
				}
				imageCounter.Commit(info)
				imageCommitted = true
			}
			if !flushPending() {
				sr.Stop(streamErr)
				return
			}
			if err := sendResponsesStreamData(c, info, streamResponse, data); err != nil {
				streamErr = streamWriteError(err)
				sr.Stop(streamErr)
				return
			}
			sr.Done()
		case "response.failed", "response.error", "response.cancelled", "response.canceled":
			if !imageCommitted {
				imageCounter.Reset()
				imageCounter.Commit(info)
				imageCommitted = true
			}
			info.StreamTerminalEvent = streamResponse.Type
			actualOutputStarted := streamActualOutputStarted()
			recordResponsesUpstreamFailure(c, info, streamResponse, actualOutputStarted)
			skipRetry := actualOutputStarted
			if streamResponse.Response != nil {
				if oaiErr := streamResponse.Response.GetOpenAIError(); oaiErr != nil && oaiErr.Type != "" {
					if skipRetry {
						streamErr = types.WithOpenAIError(*oaiErr, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
					} else {
						streamErr = types.WithOpenAIError(*oaiErr, http.StatusBadGateway)
					}
					sr.Stop(streamErr)
					return
				}
			}
			if skipRetry {
				streamErr = types.NewOpenAIError(
					fmt.Errorf("responses stream error: %s", streamResponse.Type),
					types.ErrorCodeBadResponse,
					http.StatusBadGateway,
					types.ErrOptionWithSkipRetry(),
				)
			} else {
				streamErr = types.NewOpenAIError(
					fmt.Errorf("responses stream error: %s", streamResponse.Type),
					types.ErrorCodeBadResponse,
					http.StatusBadGateway,
				)
			}
			if !skipRetry {
				sr.Stop(streamErr)
				return
			}
			if !flushPending() {
				sr.Stop(streamErr)
				return
			}
			if err := sendResponsesStreamData(c, info, streamResponse, data); err != nil {
				streamErr = streamWriteError(err)
				sr.Stop(streamErr)
				return
			}
			sr.Stop(streamErr)
		case "response.output_text.delta":
			responseTextBuilder.WriteString(streamResponse.Delta)
			if !flushPending() {
				sr.Stop(streamErr)
				return
			}
			if err := sendResponsesStreamData(c, info, streamResponse, data); err != nil {
				streamErr = streamWriteError(err)
				sr.Stop(streamErr)
			}
		case dto.ResponsesOutputTypeItemDone:
			if streamResponse.Item != nil {
				switch streamResponse.Item.Type {
				case dto.BuildInCallWebSearchCall:
					info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
				case dto.BuildInCallFileSearchCall:
					info.CountBillableToolCall(dto.BuildInCallFileSearchCall, "")
				case dto.BuildInCallFunctionCall:
					info.CountBillableToolCall(dto.BuildInCallFunctionCall, streamResponse.Item.Name)
				case dto.ResponsesOutputTypeImageGenerationCall:
					if !imageCommitted {
						imageCounter.Observe(streamResponse.Item, streamResponse.OutputIndex)
					}
				}
			}
			if !flushPending() {
				sr.Stop(streamErr)
				return
			}
			if err := sendResponsesStreamData(c, info, streamResponse, data); err != nil {
				streamErr = streamWriteError(err)
				sr.Stop(streamErr)
			}
		default:
			if streamResponse.Type == "response.created" || streamResponse.Type == "response.in_progress" || streamResponse.Type == "response.queued" {
				pending = append(pending, pendingEvent{response: streamResponse, data: data})
				return
			}
			if !flushPending() {
				sr.Stop(streamErr)
				return
			}
			if err := sendResponsesStreamData(c, info, streamResponse, data); err != nil {
				streamErr = streamWriteError(err)
				sr.Stop(streamErr)
			}
		}
	})
	if streamErr != nil {
		return nil, streamErr
	}
	// The upstream accepted these requests but cannot return final usage after
	// the downstream disappears or a started stream is truncated.
	if info.StreamStatus != nil && info.StreamStatus.EndReason == relaycommon.StreamEndReasonClientGone {
		return estimateStreamUsage(), nil
	}
	if info.StreamStatus != nil && info.StreamStatus.EndReason == relaycommon.StreamEndReasonScannerErr {
		if !streamDownstreamStarted() {
			return nil, types.NewOpenAIError(fmt.Errorf("responses stream scanner error: %w", info.StreamStatus.EndError), types.ErrorCodeBadResponseBody, http.StatusBadGateway)
		}
		return estimateStreamUsage(), nil
	}
	if info.StreamStatus != nil && info.StreamStatus.EndReason == relaycommon.StreamEndReasonEOF && info.StreamTerminalEvent == "" {
		if !streamDownstreamStarted() {
			return nil, types.NewOpenAIError(fmt.Errorf("empty responses stream: upstream ended before terminal event"), types.ErrorCodeBadResponseBody, http.StatusBadGateway)
		}
		return estimateStreamUsage(), nil
	}
	if info.StreamStatus != nil &&
		(!info.StreamStatus.IsNormalEnd() || info.StreamStatus.HasErrors()) {
		return &dto.Usage{}, nil
	}

	if !info.StreamUsagePresent && usage.CompletionTokens == 0 {
		responseText := responseTextBuilder.String()
		if responseText == "" {
			responseText = service.ExtractOutputTextFromResponses(finalResponse)
		}
		if responseText != "" {
			usage = estimateStreamUsage()
		}
	}

	if !info.StreamUsagePresent && usage.PromptTokens == 0 && usage.CompletionTokens != 0 {
		usage.PromptTokens = info.GetEstimatePromptTokens()
	}

	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens

	return usage, nil
}
