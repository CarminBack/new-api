package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCanvasOAuthAuthorizeRedirectsAnonymousUserToSignIn(t *testing.T) {
	t.Setenv("CANVAS_OAUTH_CLIENT_ID", "canvas")
	t.Setenv("CANVAS_OAUTH_CLIENT_SECRET", "test-secret")
	t.Setenv("CANVAS_OAUTH_REDIRECT_URI", "https://canvas.example/auth/callback")

	router := gin.New()
	router.GET("/oauth/authorize", CanvasOAuthAuthorize)
	request := httptest.NewRequest(http.MethodGet, "/oauth/authorize?response_type=code&client_id=canvas&redirect_uri=https%3A%2F%2Fcanvas.example%2Fauth%2Fcallback&state=1234567890abcdef&code_challenge=abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ&code_challenge_method=S256", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Equal(t, "/sign-in?redirect="+url.QueryEscape(request.URL.RequestURI()), recorder.Header().Get("Location"))
}

func TestCanvasOAuthAuthorizeRejectsUnregisteredRedirect(t *testing.T) {
	t.Setenv("CANVAS_OAUTH_CLIENT_SECRET", "test-secret")
	router := gin.New()
	router.GET("/oauth/authorize", CanvasOAuthAuthorize)
	request := httptest.NewRequest(http.MethodGet, "/oauth/authorize?response_type=code&client_id=canvas&redirect_uri=https%3A%2F%2Fevil.example%2Fcallback&state=1234567890abcdef&code_challenge=abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ&code_challenge_method=S256", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func setupCanvasTokenTestDB(t *testing.T, userID int) *gorm.DB {
	t.Helper()
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}))
	require.NoError(t, db.Create(&model.User{Id: userID, Username: fmt.Sprintf("canvas-user-%d", userID), Status: common.UserStatusEnabled, Group: "default"}).Error)
	return db
}

func TestGetOrCreateCanvasTokenCreatesReusesAndPreservesDisabledToken(t *testing.T) {
	db := setupCanvasTokenTestDB(t, 7)
	created, err := getOrCreateCanvasToken(7, "Canvas automatic authorization", "Image")
	require.NoError(t, err)
	require.Equal(t, common.TokenStatusEnabled, created.Status)
	require.True(t, created.UnlimitedQuota)
	require.EqualValues(t, -1, created.ExpiredTime)

	reused, err := getOrCreateCanvasToken(7, created.Name, created.Group)
	require.NoError(t, err)
	require.Equal(t, created.Id, reused.Id)
	var count int64
	require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", 7).Count(&count).Error)
	require.EqualValues(t, 1, count)

	require.NoError(t, db.Model(created).Update("status", common.TokenStatusDisabled).Error)
	_, err = getOrCreateCanvasToken(7, created.Name, created.Group)
	require.ErrorIs(t, err, errCanvasTokenDisabled)
}

func TestProvisionCanvasTokensRollsBackWhenCapabilityTokenIsDisabled(t *testing.T) {
	db := setupCanvasTokenTestDB(t, 8)
	disabled := &model.Token{UserId: 8, Key: "disabled-key", Status: common.TokenStatusDisabled, Name: "Canvas (Video)", CreatedTime: 1, AccessedTime: 1, ExpiredTime: -1, UnlimitedQuota: true, Group: "Video"}
	require.NoError(t, db.Create(disabled).Error)

	specs := []canvasTokenSpec{
		{Capability: "image", Name: "Canvas", Group: "Image"},
		{Capability: "video", Name: "Canvas (Video)", Group: "Video"},
	}
	_, err := provisionCanvasTokens(8, specs)
	require.ErrorIs(t, err, errCanvasTokenDisabled)

	var count int64
	require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", 8).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestCanvasTokenSpecsProvisionCapabilities(t *testing.T) {
	config := canvasOAuthConfig{TokenName: "Canvas", ImageGroup: "Image", VideoGroup: "Video", ChatGPTGroup: "ChatGPT"}
	require.Equal(t, []canvasTokenSpec{
		{Capability: "image", Name: "Canvas", Group: "Image"},
		{Capability: "video", Name: "Canvas (Video)", Group: "Video"},
		{Capability: "text", Name: "Canvas (ChatGPT)", Group: "ChatGPT"},
	}, canvasTokenSpecs(config))

	video := canvasOAuthConfig{TokenName: "Video", VideoGroup: "Video", VideoOnly: true}
	require.Equal(t, []canvasTokenSpec{{Capability: "video", Name: "Video", Group: "Video"}}, canvasTokenSpecs(video))
}
