package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func MigrateTaskPricing(c *gin.Context) {
	var request service.TaskPricingMigrationRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	result, err := service.MigrateTaskPricing(request)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	recordManageAudit(c, "task.pricing.migrate", map[string]any{
		"dry_run": request.DryRun, "models": result.Total, "changed": result.Changed,
	})
	common.ApiSuccess(c, result)
}
