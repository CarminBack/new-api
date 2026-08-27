package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/gin-gonic/gin"
)

const genericResponsesItemIDPrefixForAudit = "item_"

func RecordResponsesItemIDCompatibility(c *gin.Context, stripped int, itemTypes map[string]int) {
	if c == nil || stripped <= 0 || len(itemTypes) == 0 {
		return
	}
	typesCopy := make(map[string]int, len(itemTypes))
	for itemType, count := range itemTypes {
		typesCopy[itemType] = count
	}
	common.SetContextKey(c, constant.ContextKeyResponsesItemIDCompatibility, map[string]interface{}{
		"retried":    true,
		"stripped":   stripped,
		"id_prefix":  genericResponsesItemIDPrefixForAudit,
		"item_types": typesCopy,
	})
}

func AppendResponsesItemIDCompatibilityAdminInfo(c *gin.Context, adminInfo map[string]interface{}) {
	if c == nil || adminInfo == nil {
		return
	}
	audit, ok := common.GetContextKey(c, constant.ContextKeyResponsesItemIDCompatibility)
	if !ok || audit == nil {
		return
	}
	adminInfo["responses_item_id_compatibility"] = audit
}
