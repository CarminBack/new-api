package service

import (
	"encoding/base64"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const tinyPNGDataURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL7WQAAAABJRU5ErkJggg=="

func TestStoreTemporaryReferenceImage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("REFERENCE_MEDIA_STORAGE_DIR", dir)
	originalServerAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://token.example.test"
	t.Cleanup(func() { system_setting.ServerAddress = originalServerAddress })

	storedURL, err := StoreTemporaryReferenceImage(tinyPNGDataURL)
	require.NoError(t, err)
	parsed, err := url.Parse(storedURL)
	require.NoError(t, err)
	assert.Equal(t, "https://token.example.test", parsed.Scheme+"://"+parsed.Host)
	parts := strings.Split(parsed.Path, "/")
	id := parts[len(parts)-2]
	expires, err := strconv.ParseInt(parsed.Query().Get("expires"), 10, 64)
	require.NoError(t, err)
	assert.True(t, ValidateTemporaryReferenceMediaSignature(id, expires, parsed.Query().Get("signature")))
	assert.FileExists(t, filepath.Join(dir, id))
}

func TestStoreTemporaryReferenceImageRejectsInvalidDataURL(t *testing.T) {
	_, err := StoreTemporaryReferenceImage("aGVsbG8=")
	require.Error(t, err)
	_, err = StoreTemporaryReferenceImage("data:image/png;base64,not-base64")
	require.Error(t, err)
}

func TestStoreTemporaryReferenceVideoAndAudio(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("REFERENCE_MEDIA_STORAGE_DIR", dir)
	originalServerAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://token.example.test"
	t.Cleanup(func() { system_setting.ServerAddress = originalServerAddress })

	videoBytes := []byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 0, 0, 'i', 's', 'o', 'm', 'm', 'p', '4', '2'}
	audioBytes := []byte{'I', 'D', '3', 4, 0, 0, 0, 0, 0, 0}

	videoURL, err := StoreTemporaryReferenceVideo("data:video/mp4;base64," + base64.StdEncoding.EncodeToString(videoBytes))
	require.NoError(t, err)
	audioURL, err := StoreTemporaryReferenceAudio("data:audio/mpeg;base64," + base64.StdEncoding.EncodeToString(audioBytes))
	require.NoError(t, err)
	assert.Contains(t, videoURL, "/api/reference-media/")
	assert.Contains(t, audioURL, "/api/reference-media/")

	entries, err := filepath.Glob(filepath.Join(dir, "*"))
	require.NoError(t, err)
	require.Len(t, entries, 2)
}

func TestStoreTemporaryReferenceVideoAndAudioRejectWrongContent(t *testing.T) {
	encodedImage := strings.TrimPrefix(tinyPNGDataURL, "data:image/png;base64,")
	_, err := StoreTemporaryReferenceVideo("data:video/mp4;base64," + encodedImage)
	require.Error(t, err)
	_, err = StoreTemporaryReferenceAudio("data:audio/mpeg;base64," + encodedImage)
	require.Error(t, err)
}

func TestCleanupExpiredTemporaryReferenceMedia(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("REFERENCE_MEDIA_STORAGE_DIR", dir)
	expiredID := strings.Repeat("a", 32)
	freshID := strings.Repeat("b", 32)
	require.NoError(t, os.WriteFile(filepath.Join(dir, expiredID), []byte("expired"), 0600))
	expiredAt := time.Now().Add(-referenceMediaRetention - time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(dir, expiredID), expiredAt, expiredAt))
	require.NoError(t, os.WriteFile(filepath.Join(dir, freshID), []byte("fresh"), 0600))

	CleanupExpiredTemporaryReferenceMedia()
	assert.NoFileExists(t, filepath.Join(dir, expiredID))
	assert.FileExists(t, filepath.Join(dir, freshID))
}
