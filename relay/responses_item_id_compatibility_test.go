package relay

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsResponsesItemIDPrefixError(t *testing.T) {
	tests := []struct {
		name   string
		status int
		code   any
		param  string
		want   bool
	}{
		{name: "exact", status: http.StatusBadRequest, code: "invalid_id_prefix", param: "input[84].id", want: true},
		{name: "wrong status", status: http.StatusUnprocessableEntity, code: "invalid_id_prefix", param: "input[84].id"},
		{name: "wrong code", status: http.StatusBadRequest, code: "invalid_value", param: "input[84].id"},
		{name: "nested id", status: http.StatusBadRequest, code: "invalid_id_prefix", param: "input[84].content[0].id"},
		{name: "invalid index", status: http.StatusBadRequest, code: "invalid_id_prefix", param: "input[item].id"},
		{
			name:   "tagged upstream error",
			status: http.StatusBadRequest,
			code:   nil,
			param:  "",
			want:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message := "invalid item id"
			if test.name == "tagged upstream error" {
				message = "[ApiIdParam] [input[84].id] [invalid_id_prefix] Invalid 'input[84].id': 'item_redacted'. Expected an ID that begins with 'rs'."
			}
			err := types.WithOpenAIError(types.OpenAIError{
				Message: message,
				Code:    test.code,
				Param:   test.param,
			}, test.status)
			assert.Equal(t, test.want, isResponsesItemIDPrefixError(err))
		})
	}
}

func TestIsResponsesItemIDPrefixErrorRejectsMalformedTaggedMessages(t *testing.T) {
	messages := []string{
		"[ApiIdParam] [input[item].id] [invalid_id_prefix] invalid",
		"[ApiIdParam] [input[84].content[0].id] [invalid_id_prefix] invalid",
		"[ApiIdParam] [input[84].id] [invalid_value] invalid",
		"prefix [ApiIdParam] [input[84].id] [invalid_id_prefix] invalid",
	}
	for _, message := range messages {
		err := types.WithOpenAIError(types.OpenAIError{Message: message}, http.StatusBadRequest)
		assert.False(t, isResponsesItemIDPrefixError(err), message)
	}
}

func TestNormalizeResponsesItemIDsStripsOnlyReplayableGenericIDs(t *testing.T) {
	payload := []byte(`{
		"model":"gpt-test",
		"metadata":{"number":1.25},
		"input":[
			{"type":"reasoning","id":"rs_keep","encrypted_content":"keep"},
			{"type":"reasoning","id":"item_reasoning_secret","encrypted_content":"encrypted","summary":[]},
			{"type":"message","id":"item_message_secret","role":"assistant","content":[{"type":"output_text","text":"answer"}]},
			{"type":"function_call","id":"item_function_secret","call_id":"call_keep","name":"lookup","arguments":"{}"},
			{"type":"function_call_output","id":"fco_keep","call_id":"call_keep","output":"ok"}
		]
	}`)

	result, err := normalizeResponsesItemIDs(payload)
	require.NoError(t, err)
	assert.Equal(t, 3, result.stripped)
	assert.Equal(t, map[string]int{"reasoning": 1, "message": 1, "function_call": 1}, result.types)

	var request map[string]json.RawMessage
	require.NoError(t, common.Unmarshal(result.payload, &request))
	var input []map[string]json.RawMessage
	require.NoError(t, common.Unmarshal(request["input"], &input))
	assert.JSONEq(t, `"rs_keep"`, string(input[0]["id"]))
	assert.NotContains(t, input[1], "id")
	assert.NotContains(t, input[2], "id")
	assert.NotContains(t, input[3], "id")
	assert.JSONEq(t, `"call_keep"`, string(input[3]["call_id"]))
	assert.JSONEq(t, `"fco_keep"`, string(input[4]["id"]))
	assert.JSONEq(t, `{"number":1.25}`, string(request["metadata"]))
	assert.NotContains(t, string(result.payload), "item_reasoning_secret")
	assert.NotContains(t, string(result.payload), "item_message_secret")
	assert.NotContains(t, string(result.payload), "item_function_secret")
}

func TestNormalizeResponsesItemIDsRejectsReferencesAndIncompleteItems(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{name: "item reference", payload: `{"input":[{"type":"item_reference","id":"item_reference_secret"}]}`},
		{name: "message without content", payload: `{"input":[{"type":"message","id":"item_message_secret","role":"assistant"}]}`},
		{name: "function without call id", payload: `{"input":[{"type":"function_call","id":"item_function_secret","name":"lookup","arguments":"{}"}]}`},
		{name: "unknown type", payload: `{"input":[{"type":"computer_call","id":"item_computer_secret","action":{}}]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := normalizeResponsesItemIDs([]byte(test.payload))
			require.Error(t, err)
			assert.Zero(t, result.stripped)
			assert.Equal(t, test.payload, string(result.payload))
		})
	}
}

func TestResponsesHelperRetriesExactItemIDPrefixErrorOnce(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		mu.Lock()
		bodies = append(bodies, body)
		attempt := len(bodies)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		if attempt == 1 {
			_, _ = w.Write([]byte(`{"error":{"message":"Expected an ID that begins with 'rs'.","type":"invalid_request_error","param":"input[0].id","code":"invalid_id_prefix"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"error":{"message":"second prefix validation error","type":"invalid_request_error","param":"input[1].id","code":"invalid_id_prefix"}}`))
	}))
	t.Cleanup(server.Close)

	request := &dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[
			{"type":"reasoning","id":"item_reasoning_secret","encrypted_content":"encrypted"},
			{"type":"function_call","id":"item_function_secret","call_id":"call_keep","name":"lookup","arguments":"{}"}
		]`),
	}
	c, _ := newResponsesItemIDCompatibilityContext(t, server.URL, request, true)
	info := newResponsesItemIDCompatibilityRelayInfo(request)

	apiErr := ResponsesHelper(c, info)
	require.NotNil(t, apiErr)
	assert.Equal(t, "invalid_id_prefix", string(apiErr.GetErrorCode()))

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, bodies, 2)
	assert.Contains(t, string(bodies[0]), "item_reasoning_secret")
	assert.Contains(t, string(bodies[0]), "item_function_secret")
	assert.NotContains(t, string(bodies[1]), "item_reasoning_secret")
	assert.NotContains(t, string(bodies[1]), "item_function_secret")
	assert.Contains(t, string(bodies[1]), "call_keep")

	audit, ok := common.GetContextKey(c, constant.ContextKeyResponsesItemIDCompatibility)
	require.True(t, ok)
	auditMap := audit.(map[string]interface{})
	assert.Equal(t, 2, auditMap["stripped"])
}

func TestResponsesHelperReturnsSuccessfulCompatibilityRetry(t *testing.T) {
	originalLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() {
		common.LogConsumeEnabled = originalLogConsumeEnabled
	})

	var mu sync.Mutex
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		mu.Lock()
		bodies = append(bodies, body)
		attempt := len(bodies)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if attempt == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Expected an ID that begins with 'rs'.","type":"invalid_request_error","param":"input[0].id","code":"invalid_id_prefix"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"resp_compatibility_success","object":"response","status":"completed","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`))
	}))
	t.Cleanup(server.Close)

	request := &dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[{"type":"reasoning","id":"item_reasoning_secret","encrypted_content":"encrypted"}]`),
	}
	c, recorder := newResponsesItemIDCompatibilityContext(t, server.URL, request, true)
	apiErr := ResponsesHelper(c, newResponsesItemIDCompatibilityRelayInfo(request))

	require.Nil(t, apiErr)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{"id":"resp_compatibility_success","object":"response","status":"completed","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`, recorder.Body.String())

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, bodies, 2)
	assert.Contains(t, string(bodies[0]), "item_reasoning_secret")
	assert.NotContains(t, string(bodies[1]), "item_reasoning_secret")

	audit, ok := common.GetContextKey(c, constant.ContextKeyResponsesItemIDCompatibility)
	require.True(t, ok)
	assert.Equal(t, true, audit.(map[string]interface{})["retried"])
}

func TestResponsesHelperRetriesPassThroughBody(t *testing.T) {
	originalLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() {
		common.LogConsumeEnabled = originalLogConsumeEnabled
	})

	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "application/json")
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Expected an ID that begins with 'rs'.","type":"invalid_request_error","param":"input[0].id","code":"invalid_id_prefix"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"resp_pass_through_success","object":"response","status":"completed","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`))
	}))
	t.Cleanup(server.Close)

	payload := `{"model":"gpt-test","input":[{"type":"function_call","id":"item_function_secret","call_id":"call_keep","name":"lookup","arguments":"{}"}]}`
	request := &dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[{"type":"function_call","id":"item_function_secret","call_id":"call_keep","name":"lookup","arguments":"{}"}]`),
	}
	c, recorder := newResponsesItemIDCompatibilityContext(t, server.URL, request, true)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{
		PassThroughBodyEnabled:              true,
		ResponsesItemIDCompatibilityEnabled: true,
	})
	t.Cleanup(func() {
		common.CleanupBodyStorage(c)
	})

	apiErr := ResponsesHelper(c, newResponsesItemIDCompatibilityRelayInfo(request))
	require.Nil(t, apiErr)
	assert.Equal(t, http.StatusOK, recorder.Code)
	require.Len(t, bodies, 2)
	assert.JSONEq(t, payload, string(bodies[0]))
	assert.NotContains(t, string(bodies[1]), "item_function_secret")
	assert.Contains(t, string(bodies[1]), "call_keep")
}

func TestResponsesHelperDoesNotRetryWhenCompatibilityIsDisabled(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"Expected an ID that begins with 'rs'.","type":"invalid_request_error","param":"input[0].id","code":"invalid_id_prefix"}}`))
	}))
	t.Cleanup(server.Close)

	request := &dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: json.RawMessage(`[{"type":"reasoning","id":"item_reasoning_secret","encrypted_content":"encrypted"}]`),
	}
	c, _ := newResponsesItemIDCompatibilityContext(t, server.URL, request, false)
	apiErr := ResponsesHelper(c, newResponsesItemIDCompatibilityRelayInfo(request))

	require.NotNil(t, apiErr)
	assert.Equal(t, "invalid_id_prefix", string(apiErr.GetErrorCode()))
	assert.Equal(t, 1, requests)
}

func newResponsesItemIDCompatibilityContext(t *testing.T, baseURL string, request *dto.OpenAIResponsesRequest, enabled bool) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyChannelId, 164)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, baseURL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "test-key")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, request.Model)
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{
		ResponsesItemIDCompatibilityEnabled: enabled,
	})
	return c, recorder
}

func newResponsesItemIDCompatibilityRelayInfo(request *dto.OpenAIResponsesRequest) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeResponses,
		RelayFormat:     types.RelayFormatOpenAIResponses,
		OriginModelName: request.Model,
		RequestURLPath:  "/v1/responses",
		Request:         request,
	}
}
