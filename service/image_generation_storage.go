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
)

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
	images := base64Items
	if len(urlItems) > len(images) {
		images = urlItems
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
	perImageQuota := quota / len(images)

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
			logger.LogWarn(c, fmt.Sprintf(
				"image generation archive skipped: request_id=%s user_id=%d channel_id=%d model=%s image_index=%d source=%s reason=%s",
				requestID,
				info.UserId,
				info.ChannelId,
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

		recordQuota := perImageQuota
		if index == len(images)-1 {
			recordQuota = quota - perImageQuota*(len(images)-1)
		}
		quality := request.Quality
		if quality == "" {
			quality = "standard"
		}
		record := &model.ImageGeneration{
			UserId:     info.UserId,
			TokenId:    info.TokenId,
			ChannelId:  info.ChannelId,
			RequestId:  requestID,
			ImageIndex: index,
			ModelName:  info.OriginModelName,
			Prompt:     request.Prompt,
			Size:       request.Size,
			Quality:    quality,
			Quota:      recordQuota,
			FilePath:   relativePath,
			MimeType:   mimeType,
			Status:     model.ImageGenerationStatusSuccess,
			Group:      info.UsingGroup,
			CreatedAt:  now.Unix(),
			UseTime:    useTimeSeconds,
			ExpireAt:   now.Add(imageGenerationRetention).Unix(),
		}
		if err := model.InsertImageGeneration(record); err != nil {
			logger.LogError(c, "failed to insert image generation record: "+err.Error())
			_ = os.Remove(absolutePath)
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
		for _, record := range records {
			if record.FilePath != "" {
				_ = os.Remove(imageGenerationFilePath(record.FilePath))
			}
			if err := model.MarkImageGenerationExpired(record.Id); err != nil {
				logger.LogError(context.Background(), "failed to mark image generation expired: "+err.Error())
			}
		}
	}
}

func GetImageGenerationAbsolutePath(record *model.ImageGeneration) string {
	if record == nil || record.FilePath == "" {
		return ""
	}
	return imageGenerationFilePath(record.FilePath)
}
