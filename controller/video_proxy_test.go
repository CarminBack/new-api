package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
)

func TestResolveOpenAIVideoURLPrefersStoredResultURL(t *testing.T) {
	task := &model.Task{
		TaskID: "task_public",
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: "task_upstream",
			ResultURL:      "https://cdn.example.com/video.mp4",
		},
	}

	videoURL, requiresAuth := resolveOpenAIVideoURL(task, "https://api.example.com/")

	assert.Equal(t, "https://cdn.example.com/video.mp4", videoURL)
	assert.False(t, requiresAuth)
}

func TestResolveOpenAIVideoURLFallsBackToAuthenticatedContentEndpoint(t *testing.T) {
	task := &model.Task{
		TaskID: "task_public",
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: "task_upstream",
			ResultURL:      "https://token.example.com/v1/videos/task_public/content",
		},
	}

	videoURL, requiresAuth := resolveOpenAIVideoURL(task, "https://api.example.com/")

	assert.Equal(t, "https://api.example.com/v1/videos/task_upstream/content", videoURL)
	assert.True(t, requiresAuth)
}
