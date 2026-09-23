package openai

import (
	"fmt"
	"io"
	"net/http"

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
	"github.com/tidwall/sjson"
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
	if oaiError := responsesResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	info.ObserveResponseModel(responsesResponse.Model)
	responseBody = rewriteSGLangResponsesCreatedAt(info, responseBody, "created_at", responsesResponse.CreatedAt)

	// 写入新的 response body
	service.IOCopyBytesGracefully(c, resp, responseBody)

	// compute usage
	usage := &dto.Usage{}
	service.ApplyResponsesUsage(usage, responsesResponse.Usage)
	// Count actual tool invocations from Output (not tool declarations).
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

	return usage, nil
}

func OaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, "invalid response or response body")
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	defer service.CloseResponseBodyGracefully(resp)

	// Opt this stream into confirmed-write tracking: a prepared HTTP header must
	// not block a safe channel retry, and Responses metadata events alone must
	// stay retryable.
	common.SetContextKey(c, constant.ContextKeyStreamResponseTracking, true)
	common.SetContextKey(c, constant.ContextKeyStreamDownstreamStarted, false)
	common.SetContextKey(c, constant.ContextKeyStreamActualOutputStarted, false)
	info.SendResponseCount = 0
	info.ReceivedResponseCount = 0
	info.StreamTerminalEvent = ""
	info.StreamUsagePresent = false
	info.StreamDownstreamStarted = false

	accumulator := service.NewResponsesUsageAccumulator(info)

	type pendingEvent struct {
		response dto.ResponsesStreamResponse
		data     string
	}
	// response.created / in_progress / queued are metadata. They are held back
	// until real content arrives so an early upstream failure can still be
	// retried against another channel without the client seeing a partial SSE.
	pending := make([]pendingEvent, 0, 3)
	var streamErr *types.NewAPIError

	flushPending := func() bool {
		for _, event := range pending {
			if err := sendResponsesStreamData(c, info, event.response, event.data); err != nil {
				streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
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

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		// 检查当前数据是否包含 completed 状态和 usage 信息
		var streamResponse dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
			logger.LogError(c, "failed to unmarshal stream response: "+err.Error())
			opts := []types.NewAPIErrorOptions{}
			if streamDownstreamStarted() {
				// Content already reached the client; a retry would duplicate output.
				opts = append(opts, types.ErrOptionWithSkipRetry())
			}
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway, opts...)
			sr.Stop(streamErr)
			return
		}
		if streamResponse.Response != nil {
			data = string(rewriteSGLangResponsesCreatedAt(info, []byte(data), "response.created_at", streamResponse.Response.CreatedAt))
		}
		// Always observe the event: model attribution and stream outcome must be
		// recorded even when the write itself is held back.
		if helper.IsResponsesTerminalEvent(streamResponse.Type) {
			info.StreamTerminalEvent = streamResponse.Type
			info.StreamUsagePresent = streamResponse.Response != nil && streamResponse.Response.Usage != nil
		}
		accumulator.Observe(&streamResponse)
		switch streamResponse.Type {
		case "response.created", "response.in_progress", "response.queued":
			pending = append(pending, pendingEvent{response: streamResponse, data: data})
			return
		}
		if !flushPending() {
			sr.Stop(streamErr)
			return
		}
		if err := sendResponsesStreamData(c, info, streamResponse, data); err != nil {
			streamErr = types.NewOpenAIError(err, types.ErrorCodeBadResponse, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
			sr.Stop(streamErr)
			return
		}
	})

	common.SetContextKey(c, constant.ContextKeyResponseStreamStatus, info.StreamStatus)
	if streamErr != nil {
		return nil, streamErr
	}
	// The upstream accepted the request but cannot report final usage once the
	// downstream disappears or a started stream is truncated. Only a stream that
	// never delivered content stays retryable.
	if info.StreamStatus != nil && info.StreamStatus.EndReason == relaycommon.StreamEndReasonClientGone {
		return accumulator.Finish(), nil
	}
	if info.StreamStatus != nil && info.StreamStatus.EndReason == relaycommon.StreamEndReasonScannerErr {
		if !streamDownstreamStarted() {
			return nil, types.NewOpenAIError(fmt.Errorf("responses stream scanner error: %w", info.StreamStatus.EndError), types.ErrorCodeBadResponseBody, http.StatusBadGateway)
		}
		return accumulator.Finish(), nil
	}
	// A stream that ends without any terminal event was cut short. If nothing
	// reached the client it is still safe to fail over to another channel.
	if info.StreamTerminalEvent == "" && (info.StreamStatus == nil || info.StreamStatus.EndReason != relaycommon.StreamEndReasonDone) {
		if !streamDownstreamStarted() {
			return nil, types.NewOpenAIError(fmt.Errorf("empty responses stream: upstream ended before terminal event"), types.ErrorCodeBadResponseBody, http.StatusBadGateway)
		}
		return accumulator.Finish(), nil
	}
	if info.StreamStatus != nil {
		info.StreamStatus.RequireTerminal()
	}
	return accumulator.Finish(), nil
}

func rewriteSGLangResponsesCreatedAt(info *relaycommon.RelayInfo, payload []byte, path string, createdAt dto.IntValue) []byte {
	if info.GetChannelType() != constant.ChannelTypeSGLang {
		return payload
	}
	if !gjson.GetBytes(payload, path).Exists() {
		return payload
	}
	patched, err := sjson.SetBytes(payload, path, int(createdAt))
	if err != nil {
		return payload
	}
	return patched
}
