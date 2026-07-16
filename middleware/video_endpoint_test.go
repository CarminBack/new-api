package middleware

import (
	"testing"

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
