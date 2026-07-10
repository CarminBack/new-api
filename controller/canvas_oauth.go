package controller

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	defaultCanvasOAuthClientID    = "canvas"
	defaultCanvasOAuthRedirectURI = "https://canvas.mewinyou.shop/auth/callback"
	defaultCanvasTokenName        = "无限画布自动授权"
	defaultCanvasTokenGroup       = "Image"
	canvasOAuthCodeTTL            = 60 * time.Second
)

var (
	pkceChallengePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43,128}$`)
	pkceVerifierPattern  = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
)

type canvasOAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	TokenName    string
	TokenGroup   string
}

func getCanvasOAuthConfig() canvasOAuthConfig {
	return canvasOAuthConfig{
		ClientID:     common.GetEnvOrDefaultString("CANVAS_OAUTH_CLIENT_ID", defaultCanvasOAuthClientID),
		ClientSecret: strings.TrimSpace(os.Getenv("CANVAS_OAUTH_CLIENT_SECRET")),
		RedirectURI:  common.GetEnvOrDefaultString("CANVAS_OAUTH_REDIRECT_URI", defaultCanvasOAuthRedirectURI),
		TokenName:    common.GetEnvOrDefaultString("CANVAS_OAUTH_TOKEN_NAME", defaultCanvasTokenName),
		TokenGroup:   common.GetEnvOrDefaultString("CANVAS_OAUTH_TOKEN_GROUP", defaultCanvasTokenGroup),
	}
}

func CanvasOAuthAuthorize(c *gin.Context) {
	config := getCanvasOAuthConfig()
	if config.ClientSecret == "" {
		c.String(http.StatusServiceUnavailable, "Canvas OAuth is not configured")
		return
	}

	clientID := c.Query("client_id")
	redirectURI := c.Query("redirect_uri")
	state := c.Query("state")
	codeChallenge := c.Query("code_challenge")
	if c.Query("response_type") != "code" || clientID != config.ClientID || redirectURI != config.RedirectURI || c.Query("code_challenge_method") != "S256" || len(state) < 16 || len(state) > 256 || !pkceChallengePattern.MatchString(codeChallenge) {
		c.String(http.StatusBadRequest, "invalid OAuth authorization request")
		return
	}

	session := sessions.Default(c)
	userID, ok := sessionInt(session.Get("id"))
	if !ok || userID <= 0 || session.Get("username") == nil {
		returnTo := c.Request.URL.RequestURI()
		c.Redirect(http.StatusFound, "/login?return_to="+url.QueryEscape(returnTo))
		return
	}
	user, err := model.GetUserById(userID, false)
	if err != nil || user.Status != common.UserStatusEnabled {
		c.Redirect(http.StatusFound, "/login?return_to="+url.QueryEscape(c.Request.URL.RequestURI()))
		return
	}

	rawCode, err := common.GenerateRandomCharsKey(64)
	if err != nil {
		common.SysLog("failed to generate Canvas OAuth code: " + err.Error())
		c.String(http.StatusInternalServerError, "failed to create authorization code")
		return
	}
	codeHash := sha256.Sum256([]byte(rawCode))
	now := time.Now()
	err = model.CreateCanvasOAuthCode(&model.CanvasOAuthCode{
		CodeHash:      hex.EncodeToString(codeHash[:]),
		UserId:        userID,
		ClientId:      clientID,
		RedirectUri:   redirectURI,
		CodeChallenge: codeChallenge,
		CreatedAt:     now.Unix(),
		ExpiresAt:     now.Add(canvasOAuthCodeTTL).Unix(),
	})
	if err != nil {
		common.SysLog("failed to create Canvas OAuth code: " + err.Error())
		c.String(http.StatusInternalServerError, "failed to create authorization code")
		return
	}

	callback, _ := url.Parse(redirectURI)
	query := callback.Query()
	query.Set("code", rawCode)
	query.Set("state", state)
	callback.RawQuery = query.Encode()
	c.Header("Cache-Control", "no-store")
	c.Redirect(http.StatusFound, callback.String())
}

func CanvasOAuthToken(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	config := getCanvasOAuthConfig()
	if config.ClientSecret == "" {
		canvasOAuthError(c, http.StatusServiceUnavailable, "temporarily_unavailable", "Canvas OAuth is not configured")
		return
	}
	if err := c.Request.ParseForm(); err != nil {
		canvasOAuthError(c, http.StatusBadRequest, "invalid_request", "invalid form body")
		return
	}
	clientID, clientSecret, ok := c.Request.BasicAuth()
	if !ok || clientID != config.ClientID || subtle.ConstantTimeCompare([]byte(clientSecret), []byte(config.ClientSecret)) != 1 {
		c.Header("WWW-Authenticate", `Basic realm="canvas-oauth"`)
		canvasOAuthError(c, http.StatusUnauthorized, "invalid_client", "client authentication failed")
		return
	}
	if c.PostForm("grant_type") != "authorization_code" || c.PostForm("redirect_uri") != config.RedirectURI {
		canvasOAuthError(c, http.StatusBadRequest, "invalid_grant", "invalid authorization grant")
		return
	}
	rawCode := c.PostForm("code")
	verifier := c.PostForm("code_verifier")
	if rawCode == "" || !pkceVerifierPattern.MatchString(verifier) {
		canvasOAuthError(c, http.StatusBadRequest, "invalid_grant", "invalid authorization grant")
		return
	}
	codeHash := sha256.Sum256([]byte(rawCode))
	expectedChallenge := sha256.Sum256([]byte(verifier))
	actualChallenge := base64.RawURLEncoding.EncodeToString(expectedChallenge[:])
	code, err := model.ConsumeCanvasOAuthCode(hex.EncodeToString(codeHash[:]), clientID, config.RedirectURI, actualChallenge, time.Now().Unix())
	if err != nil {
		canvasOAuthError(c, http.StatusBadRequest, "invalid_grant", "authorization code is invalid, expired, or failed PKCE verification")
		return
	}

	user, err := model.GetUserById(code.UserId, false)
	if err != nil || user.Status != common.UserStatusEnabled {
		canvasOAuthError(c, http.StatusBadRequest, "invalid_grant", "user is unavailable")
		return
	}
	if !service.GroupInUserUsableGroups(user.Group, config.TokenGroup) {
		canvasOAuthError(c, http.StatusForbidden, "access_denied", fmt.Sprintf("当前账号不可使用 %s 分组", config.TokenGroup))
		return
	}
	token, err := getOrCreateCanvasToken(user.Id, config)
	if err != nil {
		common.SysLog("failed to provision Canvas token: " + err.Error())
		canvasOAuthError(c, http.StatusInternalServerError, "server_error", "failed to provision Canvas access")
		return
	}
	if token.Status != common.TokenStatusEnabled {
		canvasOAuthError(c, http.StatusForbidden, "access_denied", "Canvas 自动授权令牌已被停用，请在令牌页面重新启用")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token": "sk-" + token.Key,
		"token_type":   "Bearer",
		"scope":        "image",
		"user": gin.H{
			"sub":      strconv.Itoa(user.Id),
			"username": user.Username,
		},
	})
}

func getOrCreateCanvasToken(userID int, config canvasOAuthConfig) (*model.Token, error) {
	var token model.Token
	existingToken, err := model.GetUserTokenByNameAndGroup(userID, config.TokenName, config.TokenGroup)
	if err == nil {
		return existingToken, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	count, err := model.CountUserTokens(userID)
	if err != nil {
		return nil, err
	}
	if int(count) >= operation_setting.GetMaxUserTokens() {
		return nil, errors.New("maximum token count reached")
	}
	key, err := common.GenerateKey()
	if err != nil {
		return nil, err
	}
	now := common.GetTimestamp()
	token = model.Token{
		UserId:             userID,
		Key:                key,
		Status:             common.TokenStatusEnabled,
		Name:               config.TokenName,
		CreatedTime:        now,
		AccessedTime:       now,
		ExpiredTime:        -1,
		UnlimitedQuota:     true,
		ModelLimitsEnabled: false,
		ModelLimits:        "",
		Group:              config.TokenGroup,
	}
	if err := token.Insert(); err != nil {
		if existing, lookupErr := model.GetUserTokenByNameAndGroup(userID, config.TokenName, config.TokenGroup); lookupErr == nil {
			return existing, nil
		}
		return nil, err
	}
	return &token, nil
}

func canvasOAuthError(c *gin.Context, status int, code, description string) {
	c.JSON(status, gin.H{"error": code, "error_description": description})
}

func sessionInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}
