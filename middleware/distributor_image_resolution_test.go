package middleware

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetModelFromRequestReadsImageSize(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("json generation", func(t *testing.T) {
		body := `{"model":"gpt-image-2","size":"3840x2160","prompt":"cat"}`
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewBufferString(body))
		c.Request.Header.Set("Content-Type", "application/json")

		request, err := getModelFromRequest(c)
		require.NoError(t, err)
		assert.Equal(t, "gpt-image-2", request.Model)
		assert.Equal(t, "3840x2160", request.Size)

		replayed, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		assert.Equal(t, body, string(replayed))
	})

	t.Run("multipart edit", func(t *testing.T) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		require.NoError(t, writer.WriteField("model", "gpt-image-2"))
		require.NoError(t, writer.WriteField("size", "2048x1152"))
		require.NoError(t, writer.WriteField("prompt", "edit"))
		require.NoError(t, writer.Close())

		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
		c.Request.Header.Set("Content-Type", writer.FormDataContentType())

		request, err := getModelFromRequest(c)
		require.NoError(t, err)
		assert.Equal(t, "gpt-image-2", request.Model)
		assert.Equal(t, "2048x1152", request.Size)
	})
}

func TestGetModelFromJSONBodyRejectsNonStringSize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewBufferString(`{"model":"gpt-image-2","size":2048}`))
	c.Request.Header.Set("Content-Type", "application/json")

	_, err := getModelFromJSONBody(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "field size must be a string")
}

func TestGetModelRequestPreservesImageGenerationSize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewBufferString(`{"model":"gpt-image-2","size":"2048x1152"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	request, shouldSelect, err := getModelRequest(c)
	require.NoError(t, err)
	assert.True(t, shouldSelect)
	assert.Equal(t, "gpt-image-2", request.Model)
	assert.Equal(t, "2048x1152", request.Size)
}
