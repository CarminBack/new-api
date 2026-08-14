package sora

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTaskResultFailedWithStringError(t *testing.T) {
	adaptor := &TaskAdaptor{}

	taskInfo, err := adaptor.ParseTaskResult([]byte(`{
		"id": "task_upstream",
		"status": "failed",
		"error": "safety system rejected this request"
	}`))

	require.NoError(t, err)
	require.NotNil(t, taskInfo)
	assert.Equal(t, model.TaskStatusFailure, taskInfo.Status)
	assert.Equal(t, "safety system rejected this request", taskInfo.Reason)
}

func TestParseTaskResultFailedWithObjectError(t *testing.T) {
	adaptor := &TaskAdaptor{}

	taskInfo, err := adaptor.ParseTaskResult([]byte(`{
		"id": "task_upstream",
		"status": "failed",
		"error": {"message": "invalid prompt", "code": "invalid_request"}
	}`))

	require.NoError(t, err)
	require.NotNil(t, taskInfo)
	assert.Equal(t, model.TaskStatusFailure, taskInfo.Status)
	assert.Equal(t, "invalid prompt", taskInfo.Reason)
}

func TestParseTaskResultErrorWithoutStatus(t *testing.T) {
	adaptor := &TaskAdaptor{}

	taskInfo, err := adaptor.ParseTaskResult([]byte(`{
		"code": "Client specified an invalid argument",
		"error": "Generated video rejected by content moderation.",
		"id": "task_upstream",
		"task_id": "task_upstream",
		"model": "grok-image-video"
	}`))

	require.NoError(t, err)
	require.NotNil(t, taskInfo)
	assert.Equal(t, model.TaskStatusFailure, taskInfo.Status)
	assert.Equal(t, "Generated video rejected by content moderation.", taskInfo.Reason)
}

func TestParseTaskResultUnknownWithoutErrorIsQueued(t *testing.T) {
	adaptor := &TaskAdaptor{}

	taskInfo, err := adaptor.ParseTaskResult([]byte(`{
		"id": "task_upstream",
		"task_id": "task_upstream",
		"object": "video",
		"model": "grok-video-1.5",
		"status": "unknown",
		"progress": 0
	}`))

	require.NoError(t, err)
	require.NotNil(t, taskInfo)
	assert.Equal(t, model.TaskStatusQueued, taskInfo.Status)
	assert.Empty(t, taskInfo.Reason)
}

func TestParseTaskResultUnknownWithErrorFails(t *testing.T) {
	adaptor := &TaskAdaptor{}

	taskInfo, err := adaptor.ParseTaskResult([]byte(`{
		"id": "task_upstream",
		"status": "unknown",
		"error": {"message": "upstream rejected the task", "code": "invalid_request"}
	}`))

	require.NoError(t, err)
	require.NotNil(t, taskInfo)
	assert.Equal(t, model.TaskStatusFailure, taskInfo.Status)
	assert.Equal(t, "upstream rejected the task", taskInfo.Reason)
}

func TestSoraBuildRequestBodyReturnsReplayablePassThroughBody(t *testing.T) {
	payload := []byte("opaque-sora-request-body")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/octet-stream")
	defer common.CleanupBodyStorage(c)

	info := &relaycommon.RelayInfo{}
	body, err := (&TaskAdaptor{}).BuildRequestBody(c, info)
	require.NoError(t, err)
	replayable, ok := body.(common.ReplayableBody)
	require.True(t, ok)

	sent, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, payload, sent)
	assert.EqualValues(t, len(payload), replayable.Size())

	replayBody, err := replayable.NewReader()
	require.NoError(t, err)
	replay, err := io.ReadAll(replayBody)
	require.NoError(t, err)
	require.NoError(t, replayBody.Close())
	assert.Equal(t, payload, replay)
}
