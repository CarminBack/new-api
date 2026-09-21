package service

import (
	"context"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const textRelayEligibilityKey = "text_relay_eligible"

// IsTextRelayRequest deliberately excludes image groups, media modalities and
// hosted tools. They keep their existing submission, timeout and retry rules.
func IsTextRelayRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	// Group/channel selection can change during auto-group retries, so these
	// exclusions must be checked before the request-body classification cache.
	for _, key := range []string{"group", "using_group", "token_group"} {
		if strings.EqualFold(c.GetString(key), "Image") {
			return false
		}
	}
	if id := c.GetInt("channel_id"); id > 0 && (common.MemoryCacheEnabled || model.DB != nil) {
		if channel, err := model.CacheGetChannel(id); err == nil && channel != nil && channelInImageGroup(channel) {
			return false
		}
	}
	if value, exists := c.Get(textRelayEligibilityKey); exists {
		return value == true
	}
	eligible := false
	defer func() { c.Set(textRelayEligibilityKey, eligible) }()
	switch strings.TrimSuffix(c.Request.URL.Path, "/") {
	case "/v1/chat/completions", "/v1/completions", "/v1/messages", "/v1/responses":
	default:
		return false
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return false
	}
	body, err := storage.Bytes()
	if err != nil || !gjson.ValidBytes(body) {
		return false
	}
	root := gjson.ParseBytes(body)
	if !root.IsObject() {
		return false
	}
	modalities := root.Get("modalities")
	if modalities.Exists() {
		if !modalities.IsArray() {
			return false
		}
		for _, modality := range modalities.Array() {
			if modality.String() != "text" {
				return false
			}
		}
	}
	tools := root.Get("tools")
	if tools.Exists() && tools.Type != gjson.Null {
		if !tools.IsArray() {
			return false
		}
		for _, tool := range tools.Array() {
			kind := tool.Get("type").String()
			if kind != "function" && !(c.Request.URL.Path == "/v1/messages" && kind == "" && tool.Get("name").Exists()) {
				return false
			}
		}
	}
	if root.Get("background").Bool() {
		return false
	}
	eligible = true
	return true
}

// PrepareTextRelayContext is called once, outside the retry loop. A zero
// budget preserves the existing unlimited duration while propagating cancels.
func PrepareTextRelayContext(c *gin.Context, budget time.Duration) context.CancelFunc {
	if !IsTextRelayRequest(c) || budget <= 0 {
		return func() {}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), budget)
	c.Request = c.Request.WithContext(ctx)
	return cancel
}
