package openai

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
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

	var usage = &dto.Usage{}
	var responseTextBuilder strings.Builder
	var finalResponse *dto.OpenAIResponsesResponse
	var streamErr *types.NewAPIError

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {
		var streamResponse dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
			logger.LogError(c, "failed to unmarshal stream response: "+err.Error())
			sr.Error(err)
			return
		}
		sendResponsesStreamData(c, streamResponse, data)
		switch streamResponse.Type {
		case "response.completed", "response.done", "response.incomplete":
			info.StreamTerminalEvent = streamResponse.Type
			finalResponse = streamResponse.Response
			usage, info.StreamUsagePresent = responsesUsage(finalResponse)
			if finalResponse != nil {
				if finalResponse.HasImageGenerationCall() {
					c.Set("image_generation_call", true)
					c.Set("image_generation_call_quality", finalResponse.GetQuality())
					c.Set("image_generation_call_size", finalResponse.GetSize())
				}
			}
			sr.Done()
		case "response.failed", "response.error":
			info.StreamTerminalEvent = streamResponse.Type
			if streamResponse.Response != nil {
				if oaiErr := streamResponse.Response.GetOpenAIError(); oaiErr != nil && oaiErr.Type != "" {
					streamErr = types.WithOpenAIError(*oaiErr, http.StatusInternalServerError, types.ErrOptionWithSkipRetry())
					sr.Stop(streamErr)
					return
				}
			}
			streamErr = types.NewOpenAIError(
				fmt.Errorf("responses stream error: %s", streamResponse.Type),
				types.ErrorCodeBadResponse,
				http.StatusInternalServerError,
				types.ErrOptionWithSkipRetry(),
			)
			sr.Stop(streamErr)
		case "response.output_text.delta":
			responseTextBuilder.WriteString(streamResponse.Delta)
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
		}
	})
	if streamErr != nil {
		return nil, streamErr
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
