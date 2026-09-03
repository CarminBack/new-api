package relay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
)

const genericResponsesItemIDPrefix = "item_"

type responsesItemIDCompatibilityResult struct {
	payload  []byte
	stripped int
	types    map[string]int
}

func isResponsesItemIDPrefixError(err *types.NewAPIError) bool {
	if err == nil || err.StatusCode != http.StatusBadRequest {
		return false
	}
	openAIError := err.ToOpenAIError()
	code := strings.TrimSpace(fmt.Sprint(openAIError.Code))
	param := strings.TrimSpace(openAIError.Param)
	if strings.EqualFold(code, "invalid_id_prefix") {
		return isResponsesInputIDParam(param)
	}
	if strings.EqualFold(code, "invalid_value") && isResponsesInputIDParam(param) {
		message := strings.TrimSpace(openAIError.Message)
		return hasExpectedResponsesItemIDPrefix(message, param)
	}
	if param != "" || (code != "" && code != "<nil>" && !strings.EqualFold(code, "null")) {
		return false
	}
	return isTaggedResponsesItemIDPrefixError(openAIError.Message)
}

func hasExpectedResponsesItemIDPrefix(message, param string) bool {
	if !strings.Contains(message, fmt.Sprintf("Invalid '%s':", param)) {
		return false
	}
	const marker = "Expected an ID that begins with '"
	start := strings.Index(message, marker)
	if start < 0 {
		return false
	}
	valueStart := start + len(marker)
	valueEnd := strings.Index(message[valueStart:], "'.")
	if valueEnd <= 0 {
		return false
	}
	switch message[valueStart : valueStart+valueEnd] {
	case "rs", "fc":
		return true
	default:
		return false
	}
}

func isResponsesInputIDParam(param string) bool {
	if !strings.HasPrefix(param, "input[") || !strings.HasSuffix(param, "].id") {
		return false
	}
	index := strings.TrimSuffix(strings.TrimPrefix(param, "input["), "].id")
	if index == "" {
		return false
	}
	for _, char := range index {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func isTaggedResponsesItemIDPrefixError(message string) bool {
	const prefix = "[ApiIdParam] ["
	const suffix = "] [invalid_id_prefix] "
	message = strings.TrimSpace(message)
	if !strings.HasPrefix(message, prefix) {
		return false
	}
	remainder := strings.TrimPrefix(message, prefix)
	separator := strings.Index(remainder, suffix)
	if separator <= 0 {
		return false
	}
	return isResponsesInputIDParam(remainder[:separator])
}

func normalizeResponsesItemIDs(payload []byte) (responsesItemIDCompatibilityResult, error) {
	result := responsesItemIDCompatibilityResult{payload: payload}
	if len(payload) == 0 {
		return result, nil
	}

	var request map[string]json.RawMessage
	if err := common.Unmarshal(payload, &request); err != nil {
		return result, fmt.Errorf("parse responses compatibility request: %w", err)
	}
	inputJSON, ok := request["input"]
	if !ok || common.GetJsonType(inputJSON) != "array" {
		return result, nil
	}

	var input []map[string]json.RawMessage
	if err := common.Unmarshal(inputJSON, &input); err != nil {
		return result, fmt.Errorf("parse responses compatibility input: %w", err)
	}

	typeCounts := make(map[string]int)
	for index, item := range input {
		var id string
		if err := common.Unmarshal(item["id"], &id); err != nil || !strings.HasPrefix(id, genericResponsesItemIDPrefix) {
			continue
		}

		var itemType string
		if err := common.Unmarshal(item["type"], &itemType); err != nil || !responsesItemHasReplayableContent(itemType, item) {
			return responsesItemIDCompatibilityResult{payload: payload}, fmt.Errorf("input[%d] type %q has a non-replayable %s id", index, itemType, genericResponsesItemIDPrefix)
		}
		delete(item, "id")
		typeCounts[itemType]++
	}
	if len(typeCounts) == 0 {
		return result, nil
	}

	normalizedInput, err := common.Marshal(input)
	if err != nil {
		return result, fmt.Errorf("marshal responses compatibility input: %w", err)
	}
	request["input"] = normalizedInput
	normalizedPayload, err := common.Marshal(request)
	if err != nil {
		return result, fmt.Errorf("marshal responses compatibility request: %w", err)
	}

	result.payload = normalizedPayload
	result.types = typeCounts
	for _, count := range typeCounts {
		result.stripped += count
	}
	return result, nil
}

func responsesItemHasReplayableContent(itemType string, item map[string]json.RawMessage) bool {
	switch itemType {
	case "reasoning":
		return hasNonNullResponsesItemField(item, "content") ||
			hasNonNullResponsesItemField(item, "encrypted_content") ||
			hasNonNullResponsesItemField(item, "summary")
	case "message":
		return hasNonNullResponsesItemField(item, "content")
	case "function_call":
		return hasNonNullResponsesItemField(item, "call_id") &&
			hasNonNullResponsesItemField(item, "name") &&
			hasNonNullResponsesItemField(item, "arguments")
	case "custom_tool_call":
		return hasNonNullResponsesItemField(item, "call_id") &&
			hasNonNullResponsesItemField(item, "name") &&
			hasNonNullResponsesItemField(item, "input")
	case "function_call_output", "custom_tool_call_output":
		return hasNonNullResponsesItemField(item, "call_id") &&
			hasNonNullResponsesItemField(item, "output")
	default:
		return false
	}
}

func hasNonNullResponsesItemField(item map[string]json.RawMessage, key string) bool {
	raw, ok := item[key]
	if !ok {
		return false
	}
	value := strings.TrimSpace(string(raw))
	return value != "" && value != "null"
}

func formatResponsesItemIDCompatibilityTypes(typeCounts map[string]int) string {
	if len(typeCounts) == 0 {
		return ""
	}
	types := make([]string, 0, len(typeCounts))
	for itemType := range typeCounts {
		types = append(types, itemType)
	}
	sort.Strings(types)
	parts := make([]string, 0, len(types))
	for _, itemType := range types {
		parts = append(parts, fmt.Sprintf("%s:%d", itemType, typeCounts[itemType]))
	}
	return strings.Join(parts, ",")
}
