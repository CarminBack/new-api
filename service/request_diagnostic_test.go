package service

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newRequestDiagnosticContext(t *testing.T, contentType string, body string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", contentType)
	return c
}

func TestCaptureFailedRequestDiagnosticRedactsSensitiveFields(t *testing.T) {
	c := newRequestDiagnosticContext(t, "application/json", `{"model":"gpt-5.6-terra","messages":[{"role":"user","content":"hello"}],"api_key":"secret","headers":{"Authorization":"Bearer secret","Cookie":"session"}}`)
	diagnostic := captureFailedRequestDiagnostic(c)
	require.True(t, diagnostic["captured"].(bool))
	body := diagnostic["body"].(map[string]interface{})
	require.Equal(t, "[REDACTED]", body["api_key"])
	require.Equal(t, "[REDACTED]", body["headers"].(map[string]interface{})["Authorization"])
	require.Equal(t, "hello", body["messages"].([]interface{})[0].(map[string]interface{})["content"])
}

func TestCaptureFailedRequestDiagnosticDoesNotStoreNonJSON(t *testing.T) {
	c := newRequestDiagnosticContext(t, "multipart/form-data; boundary=test", "binary")
	diagnostic := captureFailedRequestDiagnostic(c)
	require.False(t, diagnostic["captured"].(bool))
	require.Equal(t, "non_json_request", diagnostic["reason"])
}

func TestCaptureFailedRequestDiagnosticMarksLargeBody(t *testing.T) {
	c := newRequestDiagnosticContext(t, "application/json", strings.Repeat("x", int(requestDiagnosticMaxBytes)+1))
	diagnostic := captureFailedRequestDiagnostic(c)
	require.False(t, diagnostic["captured"].(bool))
	require.True(t, diagnostic["truncated"].(bool))
	require.Equal(t, "request_exceeds_diagnostic_limit", diagnostic["reason"])
}
