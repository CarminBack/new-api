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
		name    string
		status  int
		code    any
		param   string
		message string
		want    bool
	}{
		{name: "exact", status: http.StatusBadRequest, code: "invalid_id_prefix", param: "input[84].id", want: true},
		{name: "invalid value reasoning", status: http.StatusBadRequest, code: "invalid_value", param: "input[2].id", message: "Invalid 'input[2].id': 'item_redacted'. Expected an ID that begins with 'rs'.", want: true},
		{name: "invalid value function", status: http.StatusBadRequest, code: "invalid_value", param: "input[2].id", message: "Invalid 'input[2].id': 'item_redacted'. Expected an ID that begins with 'fc'.", want: true},
		{name: "wrong prefix", status: http.StatusBadRequest, code: "invalid_value", param: "input[2].id", message: "Invalid 'input[2].id': 'item_redacted'. Expected an ID that begins with 'msg'."},
		{name: "nested param", status: http.StatusBadRequest, code: "invalid_id_prefix", param: "input[2].content[0].id"},
		{name: "bad index", status: http.StatusBadRequest, code: "invalid_id_prefix", param: "input[item].id"},
		{name: "wrong status", status: http.StatusUnprocessableEntity, code: "invalid_id_prefix", param: "input[2].id"},
		{name: "tagged", status: http.StatusBadRequest, message: "[ApiIdParam] [input[84].id] [invalid_id_prefix] invalid", want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := types.WithOpenAIError(types.OpenAIError{Message: test.message, Code: test.code, Param: test.param}, test.status)
			assert.Equal(t, test.want, isResponsesItemIDPrefixError(err))
		})
	}
}

func TestNormalizeResponsesItemIDsIsFailClosed(t *testing.T) {
	payload := []byte(`{"model":"gpt-test","metadata":{"trace":"keep"},"input":[{"type":"reasoning","id":"item_reasoning_secret","encrypted_content":"encrypted"},{"type":"message","id":"item_message_secret","role":"assistant","content":[{"type":"output_text","text":"answer"}]},{"type":"function_call","id":"item_function_secret","call_id":"call_keep","name":"lookup","arguments":"{}"},{"type":"function_call_output","id":"fco_keep","call_id":"call_keep","output":"ok"}]}`)
	result, err := normalizeResponsesItemIDs(payload)
	require.NoError(t, err)
	assert.Equal(t, 3, result.stripped)
	assert.Equal(t, map[string]int{"reasoning": 1, "message": 1, "function_call": 1}, result.types)
	assert.NotContains(t, string(result.payload), "item_reasoning_secret")
	assert.NotContains(t, string(result.payload), "item_message_secret")
	assert.NotContains(t, string(result.payload), "item_function_secret")
	assert.Contains(t, string(result.payload), "call_keep")
	assert.Contains(t, string(result.payload), "fco_keep")

	unsafePayloads := []string{
		`{"input":[{"type":"item_reference","id":"item_reference_secret"}]}`,
		`{"input":[{"type":"message","id":"item_message_secret","role":"assistant"}]}`,
		`{"input":[{"type":"function_call","id":"item_function_secret","name":"lookup","arguments":"{}"}]}`,
		`{"input":[{"type":"computer_call","id":"item_computer_secret","action":{}}]}`,
	}
	for _, payload := range unsafePayloads {
		result, err := normalizeResponsesItemIDs([]byte(payload))
		require.Error(t, err)
		assert.Zero(t, result.stripped)
		assert.Equal(t, payload, string(result.payload))
	}
}

func TestResponsesHelperRetriesItemIDCompatibilityOnce(t *testing.T) {
	originalLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = false
	t.Cleanup(func() { common.LogConsumeEnabled = originalLogConsumeEnabled })

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

	request := &dto.OpenAIResponsesRequest{Model: "gpt-test", Input: json.RawMessage(`[{"type":"reasoning","id":"item_reasoning_secret","encrypted_content":"encrypted"},{"type":"function_call","id":"item_function_secret","call_id":"call_keep","name":"lookup","arguments":"{}"}]`)}
	c, recorder := newResponsesItemIDCompatibilityContext(t, server.URL, request, true)
	apiErr := ResponsesHelper(c, newResponsesItemIDCompatibilityRelayInfo(request))
	require.Nil(t, apiErr)
	assert.Equal(t, http.StatusOK, recorder.Code)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, bodies, 2)
	assert.Contains(t, string(bodies[0]), "item_reasoning_secret")
	assert.NotContains(t, string(bodies[1]), "item_reasoning_secret")
	assert.NotContains(t, string(bodies[1]), "item_function_secret")
	assert.Contains(t, string(bodies[1]), "call_keep")

	audit, ok := common.GetContextKey(c, constant.ContextKeyResponsesItemIDCompatibility)
	require.True(t, ok)
	assert.Equal(t, 2, audit.(map[string]interface{})["stripped"])
}

func TestResponsesHelperDoesNotRetryItemIDCompatibilityWhenDisabled(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"Expected an ID that begins with 'rs'.","type":"invalid_request_error","param":"input[0].id","code":"invalid_id_prefix"}}`))
	}))
	t.Cleanup(server.Close)

	request := &dto.OpenAIResponsesRequest{Model: "gpt-test", Input: json.RawMessage(`[{"type":"reasoning","id":"item_reasoning_secret","encrypted_content":"encrypted"}]`)}
	c, _ := newResponsesItemIDCompatibilityContext(t, server.URL, request, false)
	apiErr := ResponsesHelper(c, newResponsesItemIDCompatibilityRelayInfo(request))
	require.NotNil(t, apiErr)
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
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{ResponsesItemIDCompatibilityEnabled: enabled})
	return c, recorder
}

func newResponsesItemIDCompatibilityRelayInfo(request *dto.OpenAIResponsesRequest) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeResponses, RelayFormat: types.RelayFormatOpenAIResponses, OriginModelName: request.Model, RequestURLPath: "/v1/responses", Request: request}
}
