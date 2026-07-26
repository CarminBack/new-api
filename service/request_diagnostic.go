package service

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
)

const (
	requestDiagnosticMaxBytes  = int64(8 << 20)
	requestDiagnosticRetention = 72 * time.Hour
)

var sensitiveRequestFields = []string{
	"authorization",
	"apikey",
	"api_key",
	"access_token",
	"refresh_token",
	"cookie",
	"client_secret",
	"private_key",
	"password",
	"secret",
	"token",
}

// AppendFailedRequestDiagnostic captures the original JSON request only for
// a final error log. It is nested under admin_info so normal user log APIs
// continue to remove it before returning data.
func AppendFailedRequestDiagnostic(c *gin.Context, adminInfo map[string]interface{}) {
	if c == nil || adminInfo == nil {
		return
	}
	diagnostic := captureFailedRequestDiagnostic(c)
	if diagnostic != nil {
		adminInfo["request_diagnostic"] = diagnostic
	}
}

func captureFailedRequestDiagnostic(c *gin.Context) map[string]interface{} {
	now := time.Now().UTC()
	diagnostic := map[string]interface{}{
		"captured_at": now.Format(time.RFC3339),
		"expires_at":  now.Add(requestDiagnosticRetention).Format(time.RFC3339),
	}
	if c.Request == nil {
		diagnostic["captured"] = false
		diagnostic["reason"] = "missing_request"
		return diagnostic
	}
	contentType := c.Request.Header.Get("Content-Type")
	diagnostic["content_type"] = contentType
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(contentType)), "application/json") {
		diagnostic["captured"] = false
		diagnostic["reason"] = "non_json_request"
		return diagnostic
	}

	storage, err := common.GetBodyStorage(c)
	if err != nil {
		diagnostic["captured"] = false
		diagnostic["reason"] = "request_body_unavailable"
		return diagnostic
	}
	diagnostic["body_size"] = storage.Size()
	if storage.Size() > requestDiagnosticMaxBytes {
		diagnostic["captured"] = false
		diagnostic["truncated"] = true
		diagnostic["reason"] = "request_exceeds_diagnostic_limit"
		return diagnostic
	}
	body, err := storage.Bytes()
	if err != nil {
		diagnostic["captured"] = false
		diagnostic["reason"] = "request_body_unavailable"
		return diagnostic
	}
	var value interface{}
	if err := json.Unmarshal(body, &value); err != nil {
		diagnostic["captured"] = false
		diagnostic["reason"] = "invalid_json_request"
		return diagnostic
	}
	diagnostic["captured"] = true
	diagnostic["truncated"] = false
	diagnostic["body"] = redactRequestValue(value)
	return diagnostic
}

func redactRequestValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		redacted := make(map[string]interface{}, len(typed))
		for key, child := range typed {
			if isSensitiveRequestField(key) {
				redacted[key] = "[REDACTED]"
				continue
			}
			redacted[key] = redactRequestValue(child)
		}
		return redacted
	case []interface{}:
		redacted := make([]interface{}, len(typed))
		for i, child := range typed {
			redacted[i] = redactRequestValue(child)
		}
		return redacted
	default:
		return value
	}
}

func isSensitiveRequestField(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"), " ", "_"))
	for _, field := range sensitiveRequestFields {
		if normalized == field || strings.Contains(normalized, field) {
			return true
		}
	}
	return false
}

// RequestDiagnosticRetention returns the retention period used by the log
// cleanup task and is kept in one place for tests and operational tooling.
func RequestDiagnosticRetention() time.Duration {
	return requestDiagnosticRetention
}
