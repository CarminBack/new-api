package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

// GetSunoTimestampedLyrics reads an existing song without entering task creation,
// quota reservation, settlement or consume logging.
func GetSunoTimestampedLyrics(c *gin.Context) {
	var input struct {
		Model   string `json:"model"`
		TaskID  string `json:"taskId"`
		AudioID string `json:"audioId"`
	}
	fail := func(status int, message string) {
		c.JSON(status, gin.H{"code": status, "msg": message, "data": nil})
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var fields map[string]json.RawMessage
	if err := common.DecodeJson(c.Request.Body, &fields); err != nil || len(fields) != 3 {
		fail(http.StatusBadRequest, "model, taskId and audioId are required")
		return
	}
	for name := range fields {
		if name != "model" && name != "taskId" && name != "audioId" {
			fail(http.StatusBadRequest, "unsupported parameter")
			return
		}
	}
	encoded, err := common.Marshal(fields)
	if err != nil || common.Unmarshal(encoded, &input) != nil || input.Model != "sunoapi_music" ||
		!strings.HasPrefix(input.TaskID, "task_") || len(input.TaskID) > 191 || strings.TrimSpace(input.AudioID) == "" || len(input.AudioID) > 256 {
		fail(http.StatusBadRequest, "invalid model, taskId or audioId")
		return
	}
	task, exists, err := model.GetByTaskId(c.GetInt("id"), input.TaskID)
	if err != nil {
		fail(http.StatusInternalServerError, "failed to query music task")
		return
	}
	if !exists || task == nil || string(task.Platform) != "sunoapi-org" || task.Action != "MUSIC" {
		fail(http.StatusNotFound, "music task not found")
		return
	}
	if task.Status != model.TaskStatusSuccess {
		fail(http.StatusConflict, "music task must be successful before requesting lyrics")
		return
	}
	var snapshot struct {
		Songs []struct {
			ID string `json:"id"`
		} `json:"songs"`
		Data struct {
			Response struct {
				Songs []struct {
					ID string `json:"id"`
				} `json:"sunoData"`
			} `json:"response"`
		} `json:"data"`
	}
	if err := common.Unmarshal(task.Data, &snapshot); err != nil {
		fail(http.StatusInternalServerError, "invalid stored music result")
		return
	}
	songs := snapshot.Songs
	if songs == nil {
		songs = snapshot.Data.Response.Songs
	}
	found := false
	for _, song := range songs {
		found = found || song.ID == input.AudioID
	}
	if !found {
		fail(http.StatusBadRequest, "audioId does not belong to this music task")
		return
	}
	channel, err := model.GetChannelById(task.ChannelId, true)
	if err != nil || channel.Status != common.ChannelStatusEnabled || channel.Type != constant.ChannelTypeTaskPlugin {
		fail(http.StatusServiceUnavailable, "original music channel is unavailable")
		return
	}
	if channel.GetSetting().TaskPluginKey != "sunoapi-org" {
		fail(http.StatusServiceUnavailable, "original channel no longer serves this music provider")
		return
	}
	key := task.PrivateData.Key
	if key == "" {
		nextKey, _, apiErr := channel.GetNextEnabledKey()
		if apiErr != nil || nextKey == "" {
			fail(http.StatusServiceUnavailable, "original music channel has no available key")
			return
		}
		key = nextKey
	}
	payload, err := common.Marshal(gin.H{"taskId": task.GetUpstreamTaskID(), "audioId": input.AudioID})
	if err != nil {
		fail(http.StatusInternalServerError, "failed to encode lyrics request")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(channel.GetBaseURL(), "/")+"/api/v1/generate/get-timestamped-lyrics", bytes.NewReader(payload))
	if err != nil {
		fail(http.StatusServiceUnavailable, "invalid original music channel URL")
		return
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	client, err := service.GetHttpClientWithProxy(channel.GetSetting().Proxy)
	if err != nil {
		fail(http.StatusServiceUnavailable, "invalid original music channel proxy")
		return
	}
	// Never forward provider credentials to a redirect target.
	outbound := *client
	outbound.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := outbound.Do(req)
	if err != nil {
		fail(http.StatusBadGateway, "lyrics provider request failed")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fail(http.StatusBadGateway, "lyrics provider returned an HTTP error")
		return
	}
	const maxLyricsResponse = 1 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLyricsResponse+1))
	if err != nil || len(body) > maxLyricsResponse {
		fail(http.StatusBadGateway, "invalid lyrics provider response")
		return
	}
	var result struct {
		Code int `json:"code"`
		Data struct {
			AlignedWords []json.RawMessage `json:"alignedWords"`
			WaveformData []float64         `json:"waveformData"`
		} `json:"data"`
	}
	if common.Unmarshal(body, &result) != nil || result.Code != 200 || result.Data.AlignedWords == nil || result.Data.WaveformData == nil {
		fail(http.StatusBadGateway, "lyrics provider returned invalid data or a business error")
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.Data(http.StatusOK, "application/json", body)
}
