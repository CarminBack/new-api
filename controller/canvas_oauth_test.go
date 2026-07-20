package controller

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCanvasOAuthAuthorizeRedirectsAnonymousUserToDefaultSignIn(t *testing.T) {
	t.Setenv("CANVAS_OAUTH_CLIENT_ID", "canvas")
	t.Setenv("CANVAS_OAUTH_CLIENT_SECRET", "test-secret")
	t.Setenv("CANVAS_OAUTH_REDIRECT_URI", "https://canvas.example/auth/callback")

	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("test-session-secret"))))
	router.GET("/oauth/authorize", CanvasOAuthAuthorize)

	request := httptest.NewRequest(http.MethodGet, "/oauth/authorize?response_type=code&client_id=canvas&redirect_uri=https%3A%2F%2Fcanvas.example%2Fauth%2Fcallback&state=1234567890abcdef&code_challenge=abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ&code_challenge_method=S256", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Equal(t, "/sign-in?redirect="+url.QueryEscape(request.URL.RequestURI()), recorder.Header().Get("Location"))
}

func TestVideoOAuthAuthorizeRedirectsAnonymousUserToDefaultSignIn(t *testing.T) {
	t.Setenv("VIDEO_OAUTH_CLIENT_ID", "video")
	t.Setenv("VIDEO_OAUTH_CLIENT_SECRET", "test-secret")
	t.Setenv("VIDEO_OAUTH_REDIRECT_URI", "https://video.example/auth/callback")

	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("test-session-secret"))))
	router.GET("/oauth/authorize", CanvasOAuthAuthorize)

	request := httptest.NewRequest(http.MethodGet, "/oauth/authorize?response_type=code&client_id=video&redirect_uri=https%3A%2F%2Fvideo.example%2Fauth%2Fcallback&state=1234567890abcdef&code_challenge=abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ&code_challenge_method=S256", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusFound, recorder.Code)
	require.Equal(t, "/sign-in?redirect="+url.QueryEscape(request.URL.RequestURI()), recorder.Header().Get("Location"))
}

func TestGetOrCreateCanvasTokenCreatesAndReusesGroupToken(t *testing.T) {
	db := setupTokenControllerTestDB(t)

	created, err := getOrCreateCanvasToken(7, "无限画布自动授权", "Image")
	require.NoError(t, err)
	require.Equal(t, 7, created.UserId)
	require.Equal(t, "无限画布自动授权", created.Name)
	require.Equal(t, "Image", created.Group)
	require.Equal(t, common.TokenStatusEnabled, created.Status)
	require.True(t, created.UnlimitedQuota)
	require.EqualValues(t, -1, created.ExpiredTime)
	require.NotEmpty(t, created.Key)

	reused, err := getOrCreateCanvasToken(7, "无限画布自动授权", "Image")
	require.NoError(t, err)
	require.Equal(t, created.Id, reused.Id)
	require.Equal(t, created.Key, reused.Key)

	var count int64
	require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", 7).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestCanvasTokenSpecsProvisionThreeGroups(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	config := canvasOAuthConfig{
		TokenName:    "无限画布自动授权",
		ImageGroup:   "Image",
		VideoGroup:   "Video",
		ChatGPTGroup: "ChatGPT",
	}

	specs := canvasTokenSpecs(config)
	require.Equal(t, []canvasTokenSpec{
		{Capability: "image", Name: "无限画布自动授权", Group: "Image", Required: true},
		{Capability: "video", Name: "无限画布自动授权 (Video)", Group: "Video"},
		{Capability: "text", Name: "无限画布自动授权 (ChatGPT)", Group: "ChatGPT"},
	}, specs)

	created := make(map[string]*model.Token, len(specs))
	for _, spec := range specs {
		token, err := getOrCreateCanvasToken(9, spec.Name, spec.Group)
		require.NoError(t, err)
		created[spec.Capability] = token
	}
	require.NotEqual(t, created["image"].Key, created["video"].Key)
	require.NotEqual(t, created["video"].Key, created["text"].Key)

	var count int64
	require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", 9).Count(&count).Error)
	require.EqualValues(t, 3, count)

	for _, spec := range specs {
		reused, err := getOrCreateCanvasToken(9, spec.Name, spec.Group)
		require.NoError(t, err)
		require.Equal(t, created[spec.Capability].Id, reused.Id)
	}
	require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", 9).Count(&count).Error)
	require.EqualValues(t, 3, count)
}

func TestVideoTokenSpecsProvisionOnlyVideoGroup(t *testing.T) {
	config := canvasOAuthConfig{
		TokenName:  "Carmin 视频自动授权",
		VideoGroup: "Video",
		VideoOnly:  true,
	}

	require.Equal(t, []canvasTokenSpec{
		{Capability: "video", Name: "Carmin 视频自动授权", Group: "Video", Required: true},
	}, canvasTokenSpecs(config))
}

func TestFilterCanvasTokenSpecsSkipsUnavailableOptionalGroups(t *testing.T) {
	specs := []canvasTokenSpec{
		{Capability: "image", Group: "Image", Required: true},
		{Capability: "video", Group: "Video"},
		{Capability: "text", Group: "ChatGPT"},
	}

	available, deniedGroup := filterCanvasTokenSpecs(specs, func(group string) bool {
		return group == "Image" || group == "Video"
	})

	require.Empty(t, deniedGroup)
	require.Equal(t, specs[:2], available)
}

func TestFilterCanvasTokenSpecsRejectsUnavailableRequiredGroup(t *testing.T) {
	specs := []canvasTokenSpec{
		{Capability: "image", Group: "Image", Required: true},
		{Capability: "video", Group: "Video"},
	}

	available, deniedGroup := filterCanvasTokenSpecs(specs, func(string) bool { return false })

	require.Nil(t, available)
	require.Equal(t, "Image", deniedGroup)
}

func TestGetOrCreateCanvasTokenDoesNotReenableDisabledToken(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	disabled := &model.Token{
		UserId:         8,
		Key:            "disabled-key",
		Status:         common.TokenStatusDisabled,
		Name:           "无限画布自动授权 (Video)",
		CreatedTime:    1,
		AccessedTime:   1,
		ExpiredTime:    -1,
		UnlimitedQuota: true,
		Group:          "Video",
	}
	require.NoError(t, db.Create(disabled).Error)

	reused, err := getOrCreateCanvasToken(8, disabled.Name, disabled.Group)
	require.NoError(t, err)
	require.Equal(t, disabled.Id, reused.Id)
	require.Equal(t, common.TokenStatusDisabled, reused.Status)
}
