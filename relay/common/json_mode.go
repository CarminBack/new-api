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
	if rawMessageContainsJSONText(request.Instructions) || rawMessageContainsJSONText(request.Input) {
		return nil
	}

	var instructions string
	if len(request.Instructions) > 0 {
		if err := common.Unmarshal(request.Instructions, &instructions); err != nil {
			return nil
		}
	}
	if strings.TrimSpace(instructions) == "" {
		instructions = responsesJSONModeInstruction
	} else {
		instructions = responsesJSONModeInstruction + "\n" + instructions
	}

	raw, err := common.Marshal(instructions)
	if err != nil {
		return err
	}
	request.Instructions = raw
	return nil
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
