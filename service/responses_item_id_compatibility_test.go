package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponsesItemIDCompatibilityAuditIsAdminOnlyAndSanitized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	RecordResponsesItemIDCompatibility(c, 3, map[string]int{"reasoning": 1, "function_call": 2})

	other := model.NewLogOther()
	AppendResponsesItemIDCompatibilityAdminInfo(c, other)
	snapshot := other.Snapshot()
	adminInfo, ok := snapshot["admin_info"].(map[string]any)
	require.True(t, ok)
	audit, ok := adminInfo["responses_item_id_compatibility"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, true, audit["retried"])
	assert.Equal(t, 3, audit["stripped"])
	assert.Equal(t, "item_", audit["id_prefix"])
	assert.Equal(t, map[string]int{"reasoning": 1, "function_call": 2}, audit["item_types"])
}
