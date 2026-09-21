package controller

import (
	"errors"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func SyncAistarsLabConfig(c *gin.Context) {
	var request service.AistarsLabSyncRequest
	if c.Request.Body != nil {
		if err := common.DecodeJson(c.Request.Body, &request); err != nil && !errors.Is(err, io.EOF) {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid request"})
			return
		}
	}
	result, err := service.SyncAistarsLabConfig(c.Request.Context(), request)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": err.Error()})
		return
	}
	recordManageAudit(c, "aistarslab.config.sync", map[string]any{
		"dry_run": request.DryRun, "channel_id": result.ChannelID, "models": result.TotalModels,
	})
	common.ApiSuccess(c, result)
}
