package service

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

const (
	referenceMediaRetention = 24 * time.Hour
	referenceImageMaxBytes  = 30 * 1024 * 1024
	referenceVideoMaxBytes  = 30 * 1024 * 1024
	referenceAudioMaxBytes  = 20 * 1024 * 1024
)

var referenceMediaIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func referenceMediaStorageDir() string {
	if dir := strings.TrimSpace(os.Getenv("REFERENCE_MEDIA_STORAGE_DIR")); dir != "" {
		return dir
	}
	if info, err := os.Stat("/data"); err == nil && info.IsDir() {
		return "/data/reference-media"
	}
	return "data/reference-media"
}

func StoreTemporaryReferenceImage(dataURL string) (string, error) {
	return storeTemporaryReferenceMedia(dataURL, "image", referenceImageMaxBytes)
}

func StoreTemporaryReferenceVideo(dataURL string) (string, error) {
	return storeTemporaryReferenceMedia(dataURL, "video", referenceVideoMaxBytes)
}

func StoreTemporaryReferenceAudio(dataURL string) (string, error) {
	return storeTemporaryReferenceMedia(dataURL, "audio", referenceAudioMaxBytes)
}

func storeTemporaryReferenceMedia(dataURL string, kind string, maxBytes int) (string, error) {
	parts := strings.SplitN(strings.TrimSpace(dataURL), ",", 2)
	header := ""
	if len(parts) > 0 {
		header = strings.ToLower(parts[0])
	}
	if len(parts) != 2 || !strings.HasPrefix(header, "data:"+kind+"/") || !strings.Contains(header, ";base64") {
		return "", fmt.Errorf("reference %s must be a data:%s/...;base64 URL", kind, kind)
	}
	encoded := strings.TrimSpace(parts[1])
	if len(encoded) > base64.StdEncoding.EncodedLen(maxBytes)+4 {
		return "", fmt.Errorf("reference %s must be between 1 byte and %dMB", kind, maxBytes/(1024*1024))
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("invalid reference %s base64: %w", kind, err)
	}
	if len(raw) == 0 || len(raw) > maxBytes {
		return "", fmt.Errorf("reference %s must be between 1 byte and %dMB", kind, maxBytes/(1024*1024))
	}
	mimeType := http.DetectContentType(raw)
	if !isSupportedReferenceMediaContentType(kind, mimeType) {
		return "", fmt.Errorf("unsupported reference %s content type: %s", kind, mimeType)
	}

	baseURL, err := referenceMediaPublicBaseURL()
	if err != nil {
		return "", err
	}
	id := common.GetUUID()
	if err := os.MkdirAll(referenceMediaStorageDir(), 0750); err != nil {
		return "", fmt.Errorf("create reference media storage: %w", err)
	}
	if err := os.WriteFile(filepath.Join(referenceMediaStorageDir(), id), raw, 0600); err != nil {
		return "", fmt.Errorf("store reference %s: %w", kind, err)
	}

	expires := time.Now().Add(referenceMediaRetention).Unix()
	signature := generateReferenceMediaSignature(id, expires)
	return fmt.Sprintf("%s/api/reference-media/%s/content?expires=%d&signature=%s", baseURL, id, expires, signature), nil
}

func isSupportedReferenceMediaContentType(kind string, mimeType string) bool {
	switch kind {
	case "image":
		return mimeType == "image/png" || mimeType == "image/jpeg" || mimeType == "image/webp" || mimeType == "image/gif"
	case "video":
		return mimeType == "video/mp4" || mimeType == "video/webm" || mimeType == "video/quicktime" || mimeType == "video/mpeg" || mimeType == "video/x-msvideo"
	case "audio":
		return mimeType == "audio/mpeg" || mimeType == "audio/wav" || mimeType == "audio/wave" || mimeType == "audio/x-wav" ||
			mimeType == "audio/ogg" || mimeType == "application/ogg" || mimeType == "audio/webm" || mimeType == "video/webm" ||
			mimeType == "audio/mp4" || mimeType == "video/mp4" || mimeType == "audio/aac" || mimeType == "audio/flac" || mimeType == "audio/x-flac"
	default:
		return false
	}
}

func GetTemporaryReferenceMediaPath(id string) string {
	if !referenceMediaIDPattern.MatchString(id) {
		return ""
	}
	return filepath.Join(referenceMediaStorageDir(), id)
}

func ValidateTemporaryReferenceMediaSignature(id string, expires int64, signature string) bool {
	if !referenceMediaIDPattern.MatchString(id) || expires <= time.Now().Unix() || signature == "" {
		return false
	}
	return signature == generateReferenceMediaSignature(id, expires)
}

func StartTemporaryReferenceMediaCleanupTask() {
	go func() {
		CleanupExpiredTemporaryReferenceMedia()
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			CleanupExpiredTemporaryReferenceMedia()
		}
	}()
}

func CleanupExpiredTemporaryReferenceMedia() {
	entries, err := os.ReadDir(referenceMediaStorageDir())
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-referenceMediaRetention)
	for _, entry := range entries {
		if entry.IsDir() || !referenceMediaIDPattern.MatchString(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(referenceMediaStorageDir(), entry.Name()))
		}
	}
}

func referenceMediaPublicBaseURL() (string, error) {
	value := strings.TrimRight(strings.TrimSpace(system_setting.ServerAddress), "/")
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return "", fmt.Errorf("ServerAddress must be configured with a public HTTP(S) URL for reference media")
	}
	return value, nil
}

func generateReferenceMediaSignature(id string, expires int64) string {
	return common.GenerateHMAC(fmt.Sprintf("reference-media:%s:%d", id, expires))
}
