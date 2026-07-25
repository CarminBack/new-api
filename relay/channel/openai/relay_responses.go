package openai

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
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

	if responsesResponse.HasImageGenerationCall() {
		c.Set("image_generation_call", true)
		c.Set("image_generation_call_quality", responsesResponse.GetQuality())
		c.Set("image_generation_call_size", responsesResponse.GetSize())
	}

	// 写入新的 response body
	service.IOCopyBytesGracefully(c, resp, responseBody)

	// compute usage
	usage, _ := responsesUsage(&responsesResponse)
	if info == nil || info.ResponsesUsageInfo == nil || info.ResponsesUsageInfo.BuiltInTools == nil {
		return usage, nil
	}
	// 解析 Tools 用量
	for _, tool := range responsesResponse.Tools {
		buildToolinfo, ok := info.ResponsesUsageInfo.BuiltInTools[common.Interface2String(tool["type"])]
		if !ok || buildToolinfo == nil {
			logger.LogError(c, fmt.Sprintf("BuiltInTools not found for tool type: %v", tool["type"]))
			continue
		}
		buildToolinfo.CallCount++
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
	info.SendResponseCount = 0
	info.ReceivedResponseCount = 0
	info.StreamTerminalEvent = ""
	info.StreamUsagePresent = false
	info.StreamDownstreamStarted = false

	var usage = &dto.Usage{}
	var responseTextBuilder strings.Builder
	var finalResponse *dto.OpenAIResponsesResponse
	var streamErr *types.NewAPIError
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
	streamHasUpstreamOutput := func() bool {
		return streamDownstreamStarted() || responseTextBuilder.Len() > 0 ||
			(finalResponse != nil && len(finalResponse.Output) > 0)
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
			if finalResponse != nil {
				if finalResponse.HasImageGenerationCall() {
					c.Set("image_generation_call", true)
					c.Set("image_generation_call_quality", finalResponse.GetQuality())
					c.Set("image_generation_call_size", finalResponse.GetSize())
				}
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
		case "response.failed", "response.error":
			info.StreamTerminalEvent = streamResponse.Type
			skipRetry := streamDownstreamStarted()
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
			// 函数调用处理
			if streamResponse.Item != nil {
				switch streamResponse.Item.Type {
				case dto.BuildInCallWebSearchCall:
					if info != nil && info.ResponsesUsageInfo != nil && info.ResponsesUsageInfo.BuiltInTools != nil {
						if webSearchTool, exists := info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview]; exists && webSearchTool != nil {
							webSearchTool.CallCount++
						}
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
	if info.StreamStatus != nil && info.StreamStatus.EndReason == relaycommon.StreamEndReasonClientGone {
		return &dto.Usage{}, nil
	}
	if info.StreamStatus != nil && info.StreamStatus.EndReason == relaycommon.StreamEndReasonScannerErr {
		if !streamDownstreamStarted() {
			return nil, types.NewOpenAIError(fmt.Errorf("responses stream scanner error: %w", info.StreamStatus.EndError), types.ErrorCodeBadResponseBody, http.StatusBadGateway)
		}
		return &dto.Usage{}, nil
	}
	if info.StreamStatus != nil && info.StreamStatus.EndReason == relaycommon.StreamEndReasonEOF && info.StreamTerminalEvent == "" {
		if !streamDownstreamStarted() {
			return nil, types.NewOpenAIError(fmt.Errorf("empty responses stream: upstream ended before terminal event"), types.ErrorCodeBadResponseBody, http.StatusBadGateway)
		}
		return &dto.Usage{}, nil
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
			usage = service.ResponseText2Usage(c, responseText, info.UpstreamModelName, info.GetEstimatePromptTokens())
		}
	}

	if !info.StreamUsagePresent && usage.PromptTokens == 0 && usage.CompletionTokens != 0 {
		usage.PromptTokens = info.GetEstimatePromptTokens()
	}

	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens

	return usage, nil
}
