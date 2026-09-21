package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
)

const (
	imageGenerationRetention         = 7 * 24 * time.Hour
	imageGenerationURLArchiveTimeout = 15 * time.Second
	pendingGeminiImagesContextKey    = "pending_gemini_image_generation"
	maxPendingGeminiImageBytes       = 64 * 1024 * 1024
)

type pendingGeminiImage struct {
	responseIndex int
	mimeType      string
	ext           string
	raw           []byte
}

type pendingGeminiImages struct {
	images         []pendingGeminiImage
	validCount     int
	size           int
	archiveLimited bool
	seenResponses  map[*dto.GeminiChatResponse]struct{}
}

type decodedImageGenerationPayload struct {
	responseIndex int
	mimeType      string
	ext           string
	raw           []byte
}

type imageGenerationArchivePayload struct {
	responseIndex int
	mimeType      string
	relativePath  string
	absolutePath  string
}

func imageGenerationStorageDir() string {
	if dir := strings.TrimSpace(os.Getenv("IMAGE_GENERATION_STORAGE_DIR")); dir != "" {
		return dir
	}
	if info, err := os.Stat("/data"); err == nil && info.IsDir() {
		return "/data/image-generations"
	}
	return "data/image-generations"
}

func imageGenerationFilePath(relativePath string) string {
	cleanPath := filepath.Clean(relativePath)
	if filepath.IsAbs(cleanPath) || cleanPath == "." || cleanPath == ".." || strings.HasPrefix(cleanPath, ".."+string(os.PathSeparator)) {
		return ""
	}
	return filepath.Join(imageGenerationStorageDir(), cleanPath)
}

func SaveImageGenerationResponse(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ImageRequest, responseBody []byte, quota int) {
	if len(responseBody) == 0 || len(responseBody) > 64<<20 || request == nil || info == nil || model.DB == nil {
		return
	}

	var envelope struct {
		Data common.RawMessage `json:"data"`
	}
	if err := common.Unmarshal(responseBody, &envelope); err != nil {
		return
	}
	var data []dto.ImageData
	switch common.GetJsonType(envelope.Data) {
	case "object":
		var item dto.ImageData
		if err := common.Unmarshal(envelope.Data, &item); err != nil {
			return
		}
		data = []dto.ImageData{item}
	case "array":
		if err := common.Unmarshal(envelope.Data, &data); err != nil {
			return
		}
	default:
		return
	}
	saveImageGenerationItems(c, info, request, selectImageGenerationPayloads(data), quota)
}

// ResetPendingGeminiImageGeneration isolates channel attempts. A failed
// attempt must never contribute images to the next retry's billing or archive.
func ResetPendingGeminiImageGeneration(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(pendingGeminiImagesContextKey, (*pendingGeminiImages)(nil))
}

// CaptureGeminiImageGeneration retains validated inline Gemini image payloads
// until the request's final quota is known. It does not alter billing or storage.
func CaptureGeminiImageGeneration(c *gin.Context, response *dto.GeminiChatResponse) {
	if c == nil || response == nil {
		return
	}
	pending, _ := c.Get(pendingGeminiImagesContextKey)
	captured, _ := pending.(*pendingGeminiImages)
	if captured != nil {
		if _, exists := captured.seenResponses[response]; exists {
			return
		}
	}

	responseSeen := false
	for _, candidate := range response.Candidates {
		for _, part := range candidate.Content.Parts {
			inline := part.InlineData
			if inline == nil {
				continue
			}
			declaredMimeType := strings.ToLower(strings.TrimSpace(inline.MimeType))
			if !strings.HasPrefix(declaredMimeType, "image/") || strings.TrimSpace(inline.Data) == "" {
				continue
			}
			mimeType, ext, raw, err := decodeImageGenerationBase64(inline.Data)
			if err != nil {
				continue
			}
			if captured == nil {
				captured = &pendingGeminiImages{seenResponses: make(map[*dto.GeminiChatResponse]struct{})}
				c.Set(pendingGeminiImagesContextKey, captured)
			}
			if !responseSeen {
				captured.seenResponses[response] = struct{}{}
				responseSeen = true
			}
			responseIndex := captured.validCount
			if captured.validCount < dto.MaxImageN {
				captured.validCount++
			}
			if captured.archiveLimited {
				continue
			}
			if len(raw) > maxPendingGeminiImageBytes-captured.size || len(captured.images) >= dto.MaxImageN {
				captured.archiveLimited = true
				continue
			}
			captured.size += len(raw)
			captured.images = append(captured.images, pendingGeminiImage{
				responseIndex: responseIndex,
				mimeType:      mimeType,
				ext:           ext,
				raw:           raw,
			})
		}
	}
}

// CapturedGeminiImageGenerationCount returns the unique valid image payload
// count observed in the current upstream response without decoding the files.
func CapturedGeminiImageGenerationCount(c *gin.Context) int {
	if c == nil {
		return 0
	}
	pending, exists := c.Get(pendingGeminiImagesContextKey)
	if !exists {
		return 0
	}
	captured, _ := pending.(*pendingGeminiImages)
	if captured == nil {
		return 0
	}
	return captured.validCount
}

// SavePendingGeminiImageGeneration archives captured Gemini images using the
// already-settled request quota, so drawing history cannot trigger a second charge.
func SavePendingGeminiImageGeneration(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ImageRequest, quota int) {
	if c == nil || request == nil || info == nil || model.DB == nil {
		return
	}
	pending, exists := c.Get(pendingGeminiImagesContextKey)
	if !exists {
		return
	}
	captured, _ := pending.(*pendingGeminiImages)
	if captured == nil || len(captured.images) == 0 {
		return
	}
	c.Set(pendingGeminiImagesContextKey, &pendingGeminiImages{archiveLimited: true})
	images := make([]decodedImageGenerationPayload, 0, len(captured.images))
	for _, image := range captured.images {
		images = append(images, decodedImageGenerationPayload{
			responseIndex: image.responseIndex,
			mimeType:      image.mimeType,
			ext:           image.ext,
			raw:           image.raw,
		})
	}
	saveDecodedImageGenerationItems(c, info, request, images, quota)
}

func selectImageGenerationPayloads(data []dto.ImageData) []dto.ImageData {
	// Match the response payload semantics without recalculating the charge:
	// ignore metadata-only entries and do not archive split URL/base64 twice.
	var base64Items, urlItems []dto.ImageData
	for _, item := range data {
		if strings.TrimSpace(item.B64Json) != "" {
			base64Items = append(base64Items, item)
		}
		if strings.TrimSpace(item.Url) != "" {
			urlItems = append(urlItems, item)
		}
	}
	if len(urlItems) > len(base64Items) {
		return urlItems
	}
	return base64Items
}

func saveImageGenerationItems(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ImageRequest, images []dto.ImageData, quota int) {
	if request == nil || info == nil || model.DB == nil {
		return
	}
	if len(images) == 0 || len(images) > dto.MaxImageN {
		return
	}

	now := time.Now()
	archiveContext, cancelArchive := context.WithTimeout(context.Background(), imageGenerationURLArchiveTimeout)
	defer cancelArchive()

	useTimeSeconds := int64(0)
	if !info.StartTime.IsZero() {
		useTimeSeconds = int64(now.Sub(info.StartTime).Seconds())
		if useTimeSeconds < 0 {
			useTimeSeconds = 0
		}
	}
	requestID := c.GetString(common.RequestIdKey)
	if requestID == "" {
		requestID = common.GetUUID()
	}

	payloads := make([]imageGenerationArchivePayload, 0, len(images))
	for index, item := range images {
		var mimeType string
		var ext string
		var raw []byte
		var err error
		source := "b64_json"
		if strings.TrimSpace(item.B64Json) != "" {
			mimeType, ext, raw, err = decodeImageGenerationBase64(item.B64Json)
		} else if strings.TrimSpace(item.Url) != "" {
			source = "url:" + imageGenerationURLHost(item.Url)
			mimeType, ext, raw, err = downloadImageGenerationURL(archiveContext, item.Url)
		} else {
			err = fmt.Errorf("response item has neither b64_json nor url")
			source = "unsupported"
		}
		if err != nil {
			channelID := 0
			if info.ChannelMeta != nil {
				channelID = info.ChannelId
			}
			logger.LogWarn(c, fmt.Sprintf(
				"image generation archive skipped: request_id=%s user_id=%d channel_id=%d model=%s image_index=%d source=%s reason=%s",
				requestID,
				info.UserId,
				channelID,
				info.OriginModelName,
				index,
				common.MaskSensitiveInfo(source),
				common.MaskSensitiveInfo(err.Error()),
			))
			continue
		}

		relativeDir := filepath.Join(now.Format("20060102"), fmt.Sprintf("user-%d", info.UserId))
		filename := fmt.Sprintf("%s-%d.%s", requestID, index, ext)
		relativePath := filepath.Join(relativeDir, filename)
		absolutePath := imageGenerationFilePath(relativePath)
		if err := os.MkdirAll(filepath.Dir(absolutePath), 0750); err != nil {
			logger.LogError(c, "failed to create image generation storage dir: "+err.Error())
			continue
		}
		if err := os.WriteFile(absolutePath, raw, 0600); err != nil {
			logger.LogError(c, "failed to write image generation file: "+err.Error())
			continue
		}
		payloads = append(payloads, imageGenerationArchivePayload{
			responseIndex: index,
			mimeType:      mimeType,
			relativePath:  relativePath,
			absolutePath:  absolutePath,
		})
	}
	persistImageGenerationPayloads(c, info, request, payloads, quota, now, useTimeSeconds, requestID)
}

func saveDecodedImageGenerationItems(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ImageRequest, images []decodedImageGenerationPayload, quota int) {
	if request == nil || info == nil || model.DB == nil || len(images) == 0 || len(images) > dto.MaxImageN {
		return
	}

	now := time.Now()
	useTimeSeconds := int64(0)
	if !info.StartTime.IsZero() {
		useTimeSeconds = int64(now.Sub(info.StartTime).Seconds())
		if useTimeSeconds < 0 {
			useTimeSeconds = 0
		}
	}
	requestID := c.GetString(common.RequestIdKey)
	if requestID == "" {
		requestID = common.GetUUID()
	}

	payloads := make([]imageGenerationArchivePayload, 0, len(images))
	for _, image := range images {
		relativeDir := filepath.Join(now.Format("20060102"), fmt.Sprintf("user-%d", info.UserId))
		filename := fmt.Sprintf("%s-%d.%s", requestID, image.responseIndex, image.ext)
		relativePath := filepath.Join(relativeDir, filename)
		absolutePath := imageGenerationFilePath(relativePath)
		if err := os.MkdirAll(filepath.Dir(absolutePath), 0750); err != nil {
			logger.LogError(c, "failed to create image generation storage dir: "+err.Error())
			continue
		}
		if err := os.WriteFile(absolutePath, image.raw, 0600); err != nil {
			logger.LogError(c, "failed to write image generation file: "+err.Error())
			continue
		}
		payloads = append(payloads, imageGenerationArchivePayload{
			responseIndex: image.responseIndex,
			mimeType:      image.mimeType,
			relativePath:  relativePath,
			absolutePath:  absolutePath,
		})
	}
	persistImageGenerationPayloads(c, info, request, payloads, quota, now, useTimeSeconds, requestID)
}

func persistImageGenerationPayloads(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ImageRequest, payloads []imageGenerationArchivePayload, quota int, now time.Time, useTimeSeconds int64, requestID string) {
	if len(payloads) == 0 {
		return
	}
	perImageQuota := quota / len(payloads)
	channelID := 0
	if info.ChannelMeta != nil {
		channelID = info.ChannelId
	}
	records := make([]*model.ImageGeneration, 0, len(payloads))
	for archiveIndex, payload := range payloads {
		recordQuota := perImageQuota
		if archiveIndex == len(payloads)-1 {
			recordQuota = quota - perImageQuota*(len(payloads)-1)
		}
		quality := request.Quality
		if quality == "" {
			quality = "standard"
		}
		records = append(records, &model.ImageGeneration{
			UserId: info.UserId, TokenId: info.TokenId, ChannelId: channelID,
			RequestId: requestID, ImageIndex: payload.responseIndex,
			ModelName: info.OriginModelName, Prompt: request.Prompt, Size: request.Size,
			Quality: quality, Quota: recordQuota, FilePath: payload.relativePath,
			MimeType: payload.mimeType, Status: model.ImageGenerationStatusSuccess,
			Group: info.UsingGroup, CreatedAt: now.Unix(), UseTime: useTimeSeconds,
			ExpireAt: now.Add(imageGenerationRetention).Unix(),
		})
	}
	if err := model.InsertImageGenerations(records); err != nil {
		logger.LogError(c, "failed to insert image generation records: "+err.Error())
		for _, payload := range payloads {
			_ = os.Remove(payload.absolutePath)
		}
	}
}

func downloadImageGenerationURL(ctx context.Context, rawURL string) (mimeType string, ext string, raw []byte, err error) {
	fetchSetting := system_setting.GetFetchSetting()
	if fetchSetting == nil || !fetchSetting.EnableSSRFProtection {
		return "", "", nil, fmt.Errorf("SSRF protection must be enabled for URL image archiving")
	}
	if err := ValidateSSRFProtectedFetchURL(rawURL); err != nil {
		return "", "", nil, fmt.Errorf("URL rejected by SSRF protection: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to create image download request: %w", err)
	}
	req.Header.Set("Accept", "image/png,image/jpeg,image/webp,image/gif")

	client := GetSSRFProtectedHTTPClient()
	if client == nil {
		return "", "", nil, fmt.Errorf("SSRF-protected HTTP client is not initialized")
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", nil, fmt.Errorf("image download failed: %w", err)
	}
	defer resp.Body.Close()

	return decodeImageGenerationURLResponse(resp, imageGenerationMaxBytes())
}

func decodeImageGenerationURLResponse(resp *http.Response, maxBytes int64) (mimeType string, ext string, raw []byte, err error) {
	if resp == nil || resp.Body == nil {
		return "", "", nil, fmt.Errorf("image download returned an empty response")
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", nil, fmt.Errorf("image download returned HTTP %d", resp.StatusCode)
	}
	if maxBytes <= 0 {
		return "", "", nil, fmt.Errorf("invalid image archive size limit")
	}
	if resp.ContentLength > maxBytes {
		return "", "", nil, fmt.Errorf("image download size %d exceeds limit %d", resp.ContentLength, maxBytes)
	}

	raw, err = io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to read image download: %w", err)
	}
	if int64(len(raw)) > maxBytes {
		return "", "", nil, fmt.Errorf("image download exceeds limit %d", maxBytes)
	}

	mimeType = http.DetectContentType(raw)
	switch mimeType {
	case "image/png":
		ext = "png"
	case "image/jpeg":
		ext = "jpg"
	case "image/webp":
		ext = "webp"
	case "image/gif":
		ext = "gif"
	default:
		return "", "", nil, fmt.Errorf("unsupported downloaded image content type: %s", mimeType)
	}
	return mimeType, ext, raw, nil
}

func imageGenerationURLHost(rawURL string) string {
	parsedURL, err := url.Parse(rawURL)
	if err != nil || parsedURL.Hostname() == "" {
		return "invalid"
	}
	return parsedURL.Hostname()
}

func imageGenerationMaxBytes() int64 {
	if constant.MaxFileDownloadMB > 0 && constant.MaxFileDownloadMB <= 64 {
		return int64(constant.MaxFileDownloadMB) * 1024 * 1024
	}
	return 64 * 1024 * 1024
}

func decodeImageGenerationBase64(data string) (mimeType string, ext string, raw []byte, err error) {
	if _, payload, ok := strings.Cut(data, ","); ok {
		data = payload
	}
	data = strings.TrimSpace(data)
	if len(data) > base64.StdEncoding.EncodedLen(int(imageGenerationMaxBytes())) {
		return "", "", nil, fmt.Errorf("image exceeds archive size limit")
	}
	raw, err = base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", "", nil, err
	}
	if int64(len(raw)) > imageGenerationMaxBytes() {
		return "", "", nil, fmt.Errorf("image exceeds archive size limit")
	}
	mimeType = http.DetectContentType(raw)
	switch mimeType {
	case "image/png":
		ext = "png"
	case "image/jpeg":
		ext = "jpg"
	case "image/webp":
		ext = "webp"
	case "image/gif":
		ext = "gif"
	default:
		return "", "", nil, fmt.Errorf("unsupported image content type: %s", mimeType)
	}
	return mimeType, ext, raw, nil
}

func StartImageGenerationCleanupTask() {
	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			CleanupExpiredImageGenerations()
			<-ticker.C
		}
	}()
}

func CleanupExpiredImageGenerations() {
	for {
		records, err := model.GetExpiredImageGenerations(time.Now().Unix(), 100)
		if err != nil {
			logger.LogError(context.Background(), "failed to query expired image generations: "+err.Error())
			return
		}
		if len(records) == 0 {
			return
		}
		progressed := false
		for _, record := range records {
			if record.FilePath != "" {
				path := imageGenerationFilePath(record.FilePath)
				if path == "" {
					logger.LogError(context.Background(), fmt.Sprintf("refusing to clean unsafe image generation path for record %d", record.Id))
					continue
				}
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					logger.LogError(context.Background(), fmt.Sprintf("failed to remove expired image generation file for record %d: %s", record.Id, err.Error()))
					continue
				}
			}
			if err := model.MarkImageGenerationExpired(record.Id); err != nil {
				logger.LogError(context.Background(), "failed to mark image generation expired: "+err.Error())
				continue
			}
			progressed = true
		}
		if !progressed {
			return
		}
	}
}

func GetImageGenerationAbsolutePath(record *model.ImageGeneration) string {
	if record == nil || record.FilePath == "" {
		return ""
	}
	return imageGenerationFilePath(record.FilePath)
}
