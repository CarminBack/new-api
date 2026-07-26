package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestVideoTaskRequestPaths(t *testing.T) {
	allowed := []string{
		"/v1/video/generations",
		"/v1/video/generations/task-1",
		"/v1/videos",
		"/v1/videos/task-1",
		"/v1/videos/task-1/remix",
		"/kling/v1/videos/image2video",
		"/jimeng",
		"/jimeng/",
	}
	for _, path := range allowed {
		assert.True(t, isVideoTaskRequestPath(path), path)
	}

	rejected := []string{
		"/v1/chat/completions",
		"/v1/completions",
		"/v1/responses",
		"/v1/images/generations",
		"/v1beta/models/grok-video-1.5:generateContent",
	}
	for _, path := range rejected {
		assert.False(t, isVideoTaskRequestPath(path), path)
	}
}

func TestDistributeSkipsChannelSetupForVideoTaskFetch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Distribute())
	router.GET("/v1/video/generations/:task_id", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	router.GET("/v1/videos/:task_id", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	for _, path := range []string{
		"/v1/video/generations/task_test",
		"/v1/videos/task_test",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		assert.Equal(t, http.StatusNoContent, resp.Code, path)
	}
}
