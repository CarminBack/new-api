package aistarslab

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

const (
	channelName  = "aistarslab"
	providerPath = "/v1/videos"
)

// AistarsLabMetadata is the supported subset of the provider metadata object.
// Keeping this type explicit prevents arbitrary client fields from being sent
// to a provider that rejects unknown metadata keys.
type AistarsLabMetadata struct {
	Resolution string           `json:"resolution,omitempty"`
	Size       string           `json:"size,omitempty"`
	Ratio      string           `json:"ratio,omitempty"`
	ModeType   string           `json:"mode_type,omitempty"`
	Images     []string         `json:"images,omitempty"`
	Videos     []string         `json:"videos,omitempty"`
	Audios     []string         `json:"audios,omitempty"`
	Content    []map[string]any `json:"content,omitempty"`
}

type requestPayload struct {
	Model    string             `json:"model"`
	Prompt   string             `json:"prompt"`
	Seconds  string             `json:"seconds,omitempty"`
	Duration int                `json:"duration,omitempty"`
	Size     string             `json:"size,omitempty"`
	N        int                `json:"n"`
	Metadata AistarsLabMetadata `json:"metadata,omitempty"`
}

type responsePayload struct {
	ID          string             `json:"id"`
	TaskID      string             `json:"task_id,omitempty"`
	Object      string             `json:"object,omitempty"`
	Model       string             `json:"model,omitempty"`
	Status      string             `json:"status,omitempty"`
	Progress    int                `json:"progress,omitempty"`
	CreatedAt   int64              `json:"created_at,omitempty"`
	CompletedAt int64              `json:"completed_at,omitempty"`
	Seconds     string             `json:"seconds,omitempty"`
	Size        string             `json:"size,omitempty"`
	Metadata    map[string]any     `json:"metadata,omitempty"`
	Error       *responseTaskError `json:"error,omitempty"`
}

type responseTaskError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type capability struct {
	known       bool
	channel     string
	resolution  string
	resolutions map[string]bool
	minSeconds  int
	maxSeconds  int
	rations     map[string]bool
	modes       map[string]bool
	maxImages   int
	maxVideos   int
	maxAudios   int
	perItem     bool
}

type TaskAdaptor struct {
	taskcommon.BaseBilling
	apiKey  string
	baseURL string
}

func IsAistarsLabBaseURL(baseURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || !strings.EqualFold(parsed.Hostname(), "api.video.aistarslab.com") {
		return false
	}
	path := strings.ToLower(strings.TrimRight(parsed.EscapedPath(), "/"))
	return path == "/openai" || strings.HasPrefix(path, "/openai/")
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.apiKey = info.ApiKey
	a.baseURL = info.ChannelBaseUrl
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	if taskErr := relaycommon.ValidateBasicTaskRequest(c, info, "generate"); taskErr != nil {
		return taskErr
	}

	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if req.N != nil && *req.N != 1 {
		return invalidCapability("n", "only n=1 is supported")
	}
	if rawSeconds := strings.TrimSpace(req.Seconds); rawSeconds != "" {
		seconds, parseErr := strconv.Atoi(rawSeconds)
		if parseErr != nil || seconds <= 0 {
			return invalidCapability("seconds", "must be a positive integer")
		}
	}
	if req.N == nil {
		req.N = common.GetPointer(1)
	}
	metadata, err := parseMetadata(req.Metadata)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_metadata", http.StatusBadRequest)
	}
	metadata = mergePublicFields(req, metadata)
	images, videos, audios := collectReferences(req, metadata)

	capability := capabilityForModel(req.Model)
	if !capability.known {
		// Unknown models remain pass-through compatible. Their provider-side
		// validation is still authoritative when the capability sync is stale.
		if strings.TrimSpace(metadata.ModeType) == "" {
			if len(images) > 0 {
				metadata.ModeType = "image2video"
			} else {
				metadata.ModeType = "text2video"
			}
		}
		metadata.Images = images
		metadata.Videos = videos
		metadata.Audios = audios
		metadata.Content = nil
		req.Metadata = metadataMap(metadata)
		req.Image = ""
		req.Images = nil
		req.Videos = nil
		req.Audios = nil
		req.Resolution = ""
		req.ModeType = ""
		c.Set("task_request", req)
		return nil
	}
	seconds := requestedSeconds(req)
	if seconds == 0 {
		seconds = capability.minSeconds
	}
	if seconds < capability.minSeconds || seconds > capability.maxSeconds {
		return invalidCapability("seconds", fmt.Sprintf("must be between %d and %d for model %s", capability.minSeconds, capability.maxSeconds, req.Model))
	}
	if len(images) > capability.maxImages || len(videos) > capability.maxVideos || len(audios) > capability.maxAudios {
		return invalidCapability("inputs", fmt.Sprintf("input limits exceeded: images<=%d, videos<=%d, audios<=%d", capability.maxImages, capability.maxVideos, capability.maxAudios))
	}

	mode := strings.TrimSpace(metadata.ModeType)
	if mode == "" {
		if len(images) > 0 {
			mode = "image2video"
		} else {
			mode = "text2video"
		}
	}
	if !capability.modes[mode] {
		return invalidCapability("metadata.mode_type", "mode is not supported by the selected model")
	}
	switch mode {
	case "text2video":
		if len(images) > 0 {
			return invalidCapability("metadata.images", "text2video does not accept reference images")
		}
	case "image2video":
		if len(images) == 0 {
			return invalidCapability("metadata.images", "image2video requires at least one image")
		}
	case "frames2video":
		if len(images) != 2 {
			return invalidCapability("metadata.images", "frames2video requires exactly two images in first/last order")
		}
	}

	resolution := canonicalResolution(metadata.Resolution)
	if resolution == "" {
		resolution = capability.resolution
	}
	if !capability.resolutions[strings.ToLower(resolution)] {
		return invalidCapability("metadata.resolution", "resolution is not supported by the selected model")
	}

	size := strings.TrimSpace(req.Size)
	if size == "" {
		size = strings.TrimSpace(metadata.Size)
	}
	if size == "" {
		size = strings.TrimSpace(metadata.Ratio)
	}
	if size == "" {
		size = "16:9"
	}
	if !capability.rations[size] {
		return invalidCapability("size", "aspect ratio is not supported by the selected model")
	}

	// Store normalized values so BuildRequestBody and billing use the same
	// interpretation as preflight validation.
	metadata.ModeType = mode
	metadata.Resolution = resolution
	metadata.Images = images
	metadata.Videos = videos
	metadata.Audios = audios
	metadata.Content = nil
	req.Metadata = metadataMap(metadata)
	// Keep the normalized references in metadata only. This avoids counting
	// top-level images twice when BuildRequestBody reconstructs the provider
	// payload.
	req.Image = ""
	req.Images = nil
	req.Videos = nil
	req.Audios = nil
	req.Resolution = ""
	req.ModeType = ""
	req.Size = size
	if req.Seconds == "" {
		req.Seconds = strconv.Itoa(seconds)
	}
	c.Set("task_request", req)
	return nil
}

func invalidCapability(param, message string) *dto.TaskError {
	return service.TaskErrorWrapperLocal(fmt.Errorf("%s: %s", param, message), "invalid_request", http.StatusBadRequest)
}

func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	if req, err := relaycommon.GetTaskRequest(c); err == nil {
		capability := capabilityForModel(req.Model)
		if capability.known && !capability.perItem {
			return map[string]float64{"seconds": float64(requestedSeconds(req))}
		}
	}
	return nil
}

func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	base := strings.TrimRight(a.baseURL, "/")
	if strings.HasSuffix(strings.ToLower(base), "/v1") {
		return base + "/videos", nil
	}
	return base + providerPath, nil
}

func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}
	metadata, err := parseMetadata(req.Metadata)
	if err != nil {
		return nil, err
	}
	metadata = mergePublicFields(req, metadata)
	images, videos, audios := collectReferences(req, metadata)
	if strings.TrimSpace(metadata.ModeType) == "" {
		if len(images) > 0 {
			metadata.ModeType = "image2video"
		} else {
			metadata.ModeType = "text2video"
		}
	}
	if strings.TrimSpace(metadata.Resolution) != "" {
		metadata.Resolution = canonicalResolution(metadata.Resolution)
	}
	modelName := info.UpstreamModelName
	if strings.TrimSpace(modelName) == "" {
		modelName = req.Model
	}
	seconds := requestedSeconds(req)
	if seconds <= 0 {
		seconds = 4
	}
	payload := requestPayload{
		Model:   modelName,
		Prompt:  req.Prompt,
		Seconds: strconv.Itoa(seconds),
		Size:    req.Size,
		N:       1,
		Metadata: AistarsLabMetadata{
			Resolution: metadata.Resolution,
			ModeType:   metadata.ModeType,
			Images:     images,
			Videos:     videos,
			Audios:     audios,
		},
	}
	if payload.Size == "" {
		payload.Size = metadata.Size
	}
	if payload.Size == "" {
		payload.Size = metadata.Ratio
	}
	if payload.Size == "" {
		payload.Size = "16:9"
	}
	data, err := common.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (string, []byte, *dto.TaskError) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}
	_ = resp.Body.Close()
	var parsed responsePayload
	if err := common.Unmarshal(body, &parsed); err != nil {
		return "", body, service.TaskErrorWrapper(err, "invalid_response", http.StatusBadGateway)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		code := "upstream_error"
		message := http.StatusText(resp.StatusCode)
		if parsed.Error != nil {
			if strings.TrimSpace(parsed.Error.Code) != "" {
				code = parsed.Error.Code
			}
			if strings.TrimSpace(parsed.Error.Message) != "" {
				message = parsed.Error.Message
			}
		}
		return "", body, service.TaskErrorWrapper(fmt.Errorf("%s", message), code, resp.StatusCode)
	}
	if strings.TrimSpace(parsed.ID) == "" {
		err := fmt.Errorf("provider response did not contain a task id")
		return "", body, service.TaskErrorWrapper(err, "invalid_response", http.StatusBadGateway)
	}
	video := dto.NewOpenAIVideo()
	video.ID = info.PublicTaskID
	video.TaskID = info.PublicTaskID
	video.Model = info.OriginModelName
	video.CreatedAt = parsed.CreatedAt
	if video.CreatedAt == 0 {
		video.CreatedAt = time.Now().Unix()
	}
	video.Seconds = parsed.Seconds
	video.Size = parsed.Size
	c.JSON(http.StatusOK, video)
	return parsed.ID, body, nil
}

func (a *TaskAdaptor) FetchTask(baseURL, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok || strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("invalid task_id")
	}
	base := strings.TrimRight(baseURL, "/")
	path := providerPath
	if strings.HasSuffix(strings.ToLower(base), "/v1") {
		path = "/videos"
	}
	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequest(http.MethodGet, base+path+"/"+taskID, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Accept", "application/json")
	return client.Do(request)
}

func (a *TaskAdaptor) GetModelList() []string {
	return []string{"seedance-2.0", "seedance-2.0-fast"}
}

func (a *TaskAdaptor) GetChannelName() string { return channelName }

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	var parsed responsePayload
	if err := common.Unmarshal(respBody, &parsed); err != nil {
		return nil, err
	}
	result := &relaycommon.TaskInfo{Code: 0}
	switch parsed.Status {
	case "queued", "pending":
		result.Status = model.TaskStatusQueued
	case "in_progress", "processing", "running":
		result.Status = model.TaskStatusInProgress
	case "completed", "succeeded", "success", "done":
		result.Status = model.TaskStatusSuccess
		result.Progress = "100%"
		result.Url = resultURL(parsed.Metadata)
	case "failed", "cancelled", "canceled", "expired":
		result.Status = model.TaskStatusFailure
		if parsed.Error != nil {
			result.Reason = parsed.Error.Message
		}
	default:
		if parsed.Error != nil && parsed.Error.Message != "" {
			result.Status = model.TaskStatusFailure
			result.Reason = parsed.Error.Message
		} else {
			result.Status = model.TaskStatusInProgress
		}
	}
	if parsed.Progress > 0 && parsed.Progress < 100 {
		result.Progress = fmt.Sprintf("%d%%", parsed.Progress)
	}
	return result, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(task *model.Task) ([]byte, error) {
	video := dto.NewOpenAIVideo()
	video.ID = task.TaskID
	video.TaskID = task.TaskID
	video.Model = task.Properties.OriginModelName
	video.Status = task.Status.ToVideoStatus()
	video.SetProgressStr(task.Progress)
	video.CreatedAt = task.CreatedAt
	video.CompletedAt = task.UpdatedAt
	video.SetMetadata("result_url", task.GetResultURL())
	if task.Status == model.TaskStatusFailure {
		video.Error = &dto.OpenAIVideoError{Code: "task_failed", Message: task.FailReason}
	}
	return common.Marshal(video)
}

func parseMetadata(raw map[string]interface{}) (AistarsLabMetadata, error) {
	var metadata AistarsLabMetadata
	if raw == nil {
		return metadata, nil
	}
	if err := taskcommon.UnmarshalMetadata(raw, &metadata); err != nil {
		return metadata, err
	}
	return metadata, nil
}

func metadataMap(metadata AistarsLabMetadata) map[string]interface{} {
	data, _ := common.Marshal(metadata)
	result := map[string]interface{}{}
	_ = common.Unmarshal(data, &result)
	return result
}

func collectReferences(req relaycommon.TaskSubmitReq, metadata AistarsLabMetadata) ([]string, []string, []string) {
	images := appendUniqueReferences(nil, []string{req.Image})
	images = appendUniqueReferences(images, req.Images)
	images = appendUniqueReferences(images, metadata.Images)
	videos := appendUniqueReferences(nil, req.Videos)
	videos = appendUniqueReferences(videos, metadata.Videos)
	audios := appendUniqueReferences(nil, req.Audios)
	audios = appendUniqueReferences(audios, metadata.Audios)
	for _, item := range metadata.Content {
		kind, _ := item["type"].(string)
		var mediaKey string
		switch kind {
		case "image_url":
			mediaKey = "image_url"
		case "video_url":
			mediaKey = "video_url"
		case "audio_url":
			mediaKey = "audio_url"
		}
		media, _ := item[mediaKey].(map[string]interface{})
		urlValue, _ := media["url"].(string)
		if strings.TrimSpace(urlValue) == "" {
			continue
		}
		switch kind {
		case "image_url":
			images = appendUniqueReferences(images, []string{urlValue})
		case "video_url":
			videos = appendUniqueReferences(videos, []string{urlValue})
		case "audio_url":
			audios = appendUniqueReferences(audios, []string{urlValue})
		}
	}
	return images, videos, audios
}

func mergePublicFields(req relaycommon.TaskSubmitReq, metadata AistarsLabMetadata) AistarsLabMetadata {
	if strings.TrimSpace(req.Resolution) != "" {
		metadata.Resolution = strings.TrimSpace(req.Resolution)
	}
	if strings.TrimSpace(req.ModeType) != "" {
		metadata.ModeType = strings.TrimSpace(req.ModeType)
	}
	return metadata
}

func appendUniqueReferences(dst, values []string) []string {
	seen := make(map[string]struct{}, len(dst)+len(values))
	for _, value := range dst {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		dst = append(dst, value)
	}
	return dst
}

func requestedSeconds(req relaycommon.TaskSubmitReq) int {
	if strings.TrimSpace(req.Seconds) != "" {
		seconds, _ := strconv.Atoi(strings.TrimSpace(req.Seconds))
		return seconds
	}
	return req.Duration
}

func resultURL(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	urlValue, _ := metadata["result_url"].(string)
	return strings.TrimSpace(urlValue)
}

func canonicalResolution(value string) string {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "4k") {
		return "4K"
	}
	return value
}

func capabilityForModel(modelName string) capability {
	fallback := staticCapabilityForModel(modelName)
	synced, ok := service.GetAistarsLabSeedanceCapability(modelName)
	if !ok {
		return fallback
	}
	capability := fallback
	capability.known = true
	if channel := strings.TrimSpace(synced.Channel); channel != "" {
		capability.channel = channel
	}
	if resolution := canonicalResolution(synced.Quality); resolution != "" {
		capability.resolution = resolution
		capability.resolutions = map[string]bool{}
		capability.resolutions[strings.ToLower(capability.resolution)] = true
	}
	if len(synced.AspectRatios) > 0 {
		capability.rations = map[string]bool{}
		for _, ratio := range synced.AspectRatios {
			capability.rations[strings.TrimSpace(ratio)] = true
		}
	}
	if len(synced.Modes) > 0 {
		capability.modes = map[string]bool{}
		for _, mode := range synced.Modes {
			capability.modes[strings.ToLower(strings.TrimSpace(mode))] = true
		}
	}
	if synced.DurationMin != nil {
		capability.minSeconds = *synced.DurationMin
	}
	if synced.DurationMax != nil {
		capability.maxSeconds = *synced.DurationMax
	}
	if synced.InputImagesMax > 0 || !fallback.known {
		capability.maxImages = synced.InputImagesMax
	}
	if synced.InputVideosMax > 0 || !fallback.known {
		capability.maxVideos = synced.InputVideosMax
	}
	if synced.InputAudiosMax > 0 || !fallback.known {
		capability.maxAudios = synced.InputAudiosMax
	}
	if synced.BillingUnit != "" {
		capability.perItem = synced.BillingUnit == ratio_setting.TaskBillingUnitPerItem
	}
	return capability
}

func staticCapabilityForModel(modelName string) capability {
	name := strings.ToLower(strings.TrimSpace(modelName))
	if name == "" || !strings.Contains(name, "seedance") {
		return capability{}
	}
	channel := ""
	for _, code := range []string{"47", "48", "49", "50"} {
		if strings.Contains(name, "c"+code) || strings.HasPrefix(name, code+":") {
			channel = code
			break
		}
	}
	if channel == "" {
		return capability{}
	}
	capability := capability{
		known:       true,
		channel:     channel,
		minSeconds:  4,
		maxSeconds:  15,
		maxImages:   9,
		maxVideos:   3,
		maxAudios:   3,
		resolutions: map[string]bool{},
		rations:     map[string]bool{},
		modes:       map[string]bool{"text2video": true, "image2video": true},
	}
	for _, ratio := range []string{"16:9", "9:16", "1:1"} {
		capability.rations[ratio] = true
	}
	if channel == "50" {
		capability.minSeconds = 5
		capability.maxImages = 4
		capability.maxAudios = 1
		capability.perItem = true
		capability.resolution = "720p"
		capability.resolutions["720p"] = true
		return capability
	}
	if channel == "49" {
		capability.perItem = true
		capability.modes["frames2video"] = true
		capability.resolution = "720p"
		capability.resolutions["720p"] = true
		return capability
	}
	for _, ratio := range []string{"4:3", "3:4", "21:9", "2:3", "3:2"} {
		capability.rations[ratio] = true
	}
	capability.modes["frames2video"] = true
	if strings.Contains(name, "480p") {
		capability.resolution = "480p"
	} else if strings.Contains(name, "720p") {
		capability.resolution = "720p"
	} else if strings.Contains(name, "1080p") {
		capability.resolution = "1080p"
	} else if strings.Contains(name, "4k") {
		capability.resolution = "4K"
	}
	if capability.resolution != "" {
		capability.resolutions[strings.ToLower(capability.resolution)] = true
	} else {
		for _, resolution := range []string{"480p", "720p", "1080p", "4k"} {
			capability.resolutions[resolution] = true
		}
	}
	return capability
}
