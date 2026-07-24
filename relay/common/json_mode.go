package common

import (
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

const responsesJSONModeInstruction = "When producing the final textual output, return a valid json object."

// EnsureChatJSONModeInstruction satisfies upstream JSON mode validation for
// OpenAI-compatible chat requests whose messages do not mention JSON.
func EnsureChatJSONModeInstruction(request *dto.GeneralOpenAIRequest) {
	if request == nil || request.ResponseFormat == nil ||
		strings.TrimSpace(request.ResponseFormat.Type) != "json_object" {
		return
	}

	for _, message := range request.Messages {
		if valueContainsJSONText(message.Content) || rawMessageContainsJSONText(message.ToolCalls) {
			return
		}
	}

	request.Messages = append([]dto.Message{{
		Role:    request.GetSystemRoleName(),
		Content: responsesJSONModeInstruction,
	}}, request.Messages...)
}

// EnsureResponsesJSONModeInstruction satisfies upstream JSON mode validation
// without changing requests that already mention JSON or use another format.
func EnsureResponsesJSONModeInstruction(request *dto.OpenAIResponsesRequest) error {
	if request == nil || len(request.Text) == 0 {
		return nil
	}

	var textConfig struct {
		Format struct {
			Type string `json:"type"`
		} `json:"format"`
	}
	if err := common.Unmarshal(request.Text, &textConfig); err != nil ||
		strings.TrimSpace(textConfig.Format.Type) != "json_object" {
		return nil
	}
	if rawMessageContainsJSONText(request.Input) {
		return nil
	}

	var input any
	if len(request.Input) > 0 {
		if err := common.Unmarshal(request.Input, &input); err != nil {
			return nil
		}
	}
	switch typed := input.(type) {
	case nil:
		input = responsesJSONModeInstruction
	case string:
		input = responsesJSONModeInstruction + "\n" + typed
	case []any:
		input = prependJSONInstructionToResponsesItems(typed)
	default:
		return nil
	}

	raw, err := common.Marshal(input)
	if err != nil {
		return err
	}
	request.Input = raw
	return nil
}

func prependJSONInstructionToResponsesItems(items []any) []any {
	for i := len(items) - 1; i >= 0; i-- {
		message, ok := items[i].(map[string]any)
		if !ok || !strings.EqualFold(strings.TrimSpace(common.Interface2String(message["role"])), "user") {
			continue
		}
		switch content := message["content"].(type) {
		case string:
			message["content"] = responsesJSONModeInstruction + "\n" + content
		case []any:
			message["content"] = append([]any{map[string]any{
				"type": "input_text",
				"text": responsesJSONModeInstruction,
			}}, content...)
		default:
			message["content"] = responsesJSONModeInstruction
		}
		return items
	}
	return append([]any{map[string]any{
		"role":    "user",
		"content": responsesJSONModeInstruction,
	}}, items...)
}

func rawMessageContainsJSONText(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}

	var value any
	if err := common.Unmarshal(raw, &value); err != nil {
		return false
	}
	return valueContainsJSONText(value)
}

func valueContainsJSONText(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.Contains(strings.ToLower(typed), "json")
	case []any:
		for _, item := range typed {
			if valueContainsJSONText(item) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if valueContainsJSONText(item) {
				return true
			}
		}
	}
	return false
}
