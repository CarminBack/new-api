package controller

import (
	"net/http"
	"os"
	"strconv"

	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func GetTemporaryReferenceMediaContent(c *gin.Context) {
	id := c.Param("id")
	expires, err := strconv.ParseInt(c.Query("expires"), 10, 64)
	if err != nil || !service.ValidateTemporaryReferenceMediaSignature(id, expires, c.Query("signature")) {
		c.Status(http.StatusUnauthorized)
		return
	}
	path := service.GetTemporaryReferenceMediaPath(id)
	if path == "" {
		c.Status(http.StatusNotFound)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		c.Status(http.StatusGone)
		return
	}
	defer file.Close()
	buffer := make([]byte, 512)
	read, _ := file.Read(buffer)
	if read > 0 {
		c.Header("Content-Type", http.DetectContentType(buffer[:read]))
	}
	c.Header("Cache-Control", "private, max-age=3600")
	c.File(path)
}
