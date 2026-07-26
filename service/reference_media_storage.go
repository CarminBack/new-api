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
	parts := strings.SplitN(strings.TrimSpace(dataURL), ",", 2)
	if len(parts) != 2 || !strings.HasPrefix(strings.ToLower(parts[0]), "data:image/") || !strings.Contains(strings.ToLower(parts[0]), ";base64") {
		return "", fmt.Errorf("reference image must be a data:image/...;base64 URL")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[1]))
	if err != nil {
		return "", fmt.Errorf("invalid reference image base64: %w", err)
	}
	if len(raw) == 0 || len(raw) > referenceImageMaxBytes {
		return "", fmt.Errorf("reference image must be between 1 byte and %dMB", referenceImageMaxBytes/(1024*1024))
	}
	mimeType := http.DetectContentType(raw)
	if mimeType != "image/png" && mimeType != "image/jpeg" && mimeType != "image/webp" && mimeType != "image/gif" {
		return "", fmt.Errorf("unsupported reference image content type: %s", mimeType)
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
		return "", fmt.Errorf("store reference image: %w", err)
	}

	expires := time.Now().Add(referenceMediaRetention).Unix()
	signature := generateReferenceMediaSignature(id, expires)
	return fmt.Sprintf("%s/api/reference-media/%s/content?expires=%d&signature=%s", baseURL, id, expires, signature), nil
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
