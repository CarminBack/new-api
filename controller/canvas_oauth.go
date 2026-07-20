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
	defaultVideoOAuthClientID     = "video"
	defaultVideoOAuthRedirectURI  = "https://video.mewinyou.shop/auth/callback"
	defaultVideoTokenName         = "Carmin 视频自动授权"
	defaultCanvasImageGroup       = "Image"
	defaultCanvasVideoGroup       = "Video"
	defaultCanvasChatGPTGroup     = "ChatGPT"
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
	ImageGroup   string
	VideoGroup   string
	ChatGPTGroup string
	VideoOnly    bool
}

func getCanvasOAuthConfig() canvasOAuthConfig {
	return canvasOAuthConfig{
		ClientID:     common.GetEnvOrDefaultString("CANVAS_OAUTH_CLIENT_ID", defaultCanvasOAuthClientID),
		ClientSecret: strings.TrimSpace(os.Getenv("CANVAS_OAUTH_CLIENT_SECRET")),
		RedirectURI:  common.GetEnvOrDefaultString("CANVAS_OAUTH_REDIRECT_URI", defaultCanvasOAuthRedirectURI),
		TokenName:    common.GetEnvOrDefaultString("CANVAS_OAUTH_TOKEN_NAME", defaultCanvasTokenName),
		ImageGroup:   common.GetEnvOrDefaultString("CANVAS_OAUTH_TOKEN_GROUP", defaultCanvasImageGroup),
		VideoGroup:   common.GetEnvOrDefaultString("CANVAS_OAUTH_VIDEO_GROUP", defaultCanvasVideoGroup),
		ChatGPTGroup: common.GetEnvOrDefaultString("CANVAS_OAUTH_CHATGPT_GROUP", defaultCanvasChatGPTGroup),
	}
}

func getVideoOAuthConfig() canvasOAuthConfig {
	return canvasOAuthConfig{
		ClientID:     common.GetEnvOrDefaultString("VIDEO_OAUTH_CLIENT_ID", defaultVideoOAuthClientID),
		ClientSecret: strings.TrimSpace(os.Getenv("VIDEO_OAUTH_CLIENT_SECRET")),
		RedirectURI:  common.GetEnvOrDefaultString("VIDEO_OAUTH_REDIRECT_URI", defaultVideoOAuthRedirectURI),
		TokenName:    common.GetEnvOrDefaultString("VIDEO_OAUTH_TOKEN_NAME", defaultVideoTokenName),
		VideoGroup:   common.GetEnvOrDefaultString("VIDEO_OAUTH_TOKEN_GROUP", defaultCanvasVideoGroup),
		VideoOnly:    true,
	}
}

func getOAuthConfig(clientID string) (canvasOAuthConfig, bool) {
	for _, config := range []canvasOAuthConfig{getCanvasOAuthConfig(), getVideoOAuthConfig()} {
		if clientID == config.ClientID {
			return config, true
		}
	}
	return canvasOAuthConfig{}, false
}

func CanvasOAuthAuthorize(c *gin.Context) {
	clientID := c.Query("client_id")
	config, ok := getOAuthConfig(clientID)
	if !ok {
		c.String(http.StatusBadRequest, "invalid OAuth authorization request")
		return
	}
	if config.ClientSecret == "" {
		c.String(http.StatusServiceUnavailable, "OAuth client is not configured")
		return
	}

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
		c.Redirect(http.StatusFound, "/sign-in?redirect="+url.QueryEscape(returnTo))
		return
	}
	user, err := model.GetUserById(userID, false)
	if err != nil || user.Status != common.UserStatusEnabled {
		c.Redirect(http.StatusFound, "/sign-in?redirect="+url.QueryEscape(c.Request.URL.RequestURI()))
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
	if err := c.Request.ParseForm(); err != nil {
		canvasOAuthError(c, http.StatusBadRequest, "invalid_request", "invalid form body")
		return
	}
	clientID, clientSecret, ok := c.Request.BasicAuth()
	config, knownClient := getOAuthConfig(clientID)
	if knownClient && config.ClientSecret == "" {
		canvasOAuthError(c, http.StatusServiceUnavailable, "temporarily_unavailable", "OAuth client is not configured")
		return
	}
	if !ok || !knownClient || subtle.ConstantTimeCompare([]byte(clientSecret), []byte(config.ClientSecret)) != 1 {
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
	specs := canvasTokenSpecs(config)
	availableSpecs, deniedGroup := filterCanvasTokenSpecs(specs, func(group string) bool {
		return service.GroupInUserUsableGroups(user.Group, group)
	})
	if deniedGroup != "" {
		canvasOAuthError(c, http.StatusForbidden, "access_denied", fmt.Sprintf("当前账号不可使用 %s 分组", deniedGroup))
		return
	}
	tokens := make(map[string]*model.Token, len(availableSpecs))
	for _, spec := range availableSpecs {
		token, provisionErr := getOrCreateCanvasToken(user.Id, spec.Name, spec.Group)
		if provisionErr != nil {
			common.SysLog("failed to provision Canvas token: " + provisionErr.Error())
			canvasOAuthError(c, http.StatusInternalServerError, "server_error", "failed to provision Canvas access")
			return
		}
		if token.Status != common.TokenStatusEnabled {
			canvasOAuthError(c, http.StatusForbidden, "access_denied", fmt.Sprintf("Canvas %s 分组自动授权令牌已被停用，请在令牌页面重新启用", spec.Group))
			return
		}
		tokens[spec.Capability] = token
	}

	groupTokens := gin.H{}
	scopes := make([]string, 0, len(specs)+1)
	accessToken := ""
	for _, spec := range availableSpecs {
		value := "sk-" + tokens[spec.Capability].Key
		groupTokens[spec.Capability] = value
		scopes = append(scopes, spec.Capability)
		if accessToken == "" {
			accessToken = value
		}
	}
	if textToken, ok := groupTokens["text"]; ok {
		groupTokens["audio"] = textToken
		scopes = append(scopes, "audio")
	}

	c.JSON(http.StatusOK, gin.H{
		"access_token": accessToken,
		"token_type":   "Bearer",
		"scope":        strings.Join(scopes, " "),
		"group_tokens": groupTokens,
		"user": gin.H{
			"sub":      strconv.Itoa(user.Id),
			"username": user.Username,
		},
	})
}

type canvasTokenSpec struct {
	Capability string
	Name       string
	Group      string
	Required   bool
}

func canvasTokenSpecs(config canvasOAuthConfig) []canvasTokenSpec {
	if config.VideoOnly {
		return []canvasTokenSpec{{Capability: "video", Name: config.TokenName, Group: config.VideoGroup, Required: true}}
	}
	return []canvasTokenSpec{
		{Capability: "image", Name: config.TokenName, Group: config.ImageGroup, Required: true},
		{Capability: "video", Name: config.TokenName + " (Video)", Group: config.VideoGroup},
		{Capability: "text", Name: config.TokenName + " (ChatGPT)", Group: config.ChatGPTGroup},
	}
}

func filterCanvasTokenSpecs(specs []canvasTokenSpec, canUse func(string) bool) ([]canvasTokenSpec, string) {
	available := make([]canvasTokenSpec, 0, len(specs))
	for _, spec := range specs {
		if canUse(spec.Group) {
			available = append(available, spec)
		} else if spec.Required {
			return nil, spec.Group
		}
	}
	return available, ""
}

func getOrCreateCanvasToken(userID int, tokenName, tokenGroup string) (*model.Token, error) {
	var token model.Token
	existingToken, err := model.GetUserTokenByNameAndGroup(userID, tokenName, tokenGroup)
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
		Name:               tokenName,
		CreatedTime:        now,
		AccessedTime:       now,
		ExpiredTime:        -1,
		UnlimitedQuota:     true,
		ModelLimitsEnabled: false,
		ModelLimits:        "",
		Group:              tokenGroup,
	}
	if err := token.Insert(); err != nil {
		if existing, lookupErr := model.GetUserTokenByNameAndGroup(userID, tokenName, tokenGroup); lookupErr == nil {
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
