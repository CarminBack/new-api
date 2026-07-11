package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func GetImageGroupPricing(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    ratio_setting.GetImageGroupPriceCopy(),
	})
}
