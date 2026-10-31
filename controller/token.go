package controller

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type tokenAutoGroupsInput struct {
	Set    bool
	Groups []string
}

func (input *tokenAutoGroupsInput) UnmarshalJSON(data []byte) error {
	input.Set = true
	if strings.TrimSpace(string(data)) == "null" {
		input.Groups = nil
		return nil
	}
	return common.Unmarshal(data, &input.Groups)
}

type tokenRequest struct {
	model.Token
	AutoGroups tokenAutoGroupsInput `json:"auto_groups"`
}

type tokenResponse struct {
	*model.Token
	AutoGroups      []string `json:"auto_groups"`
	PeriodUsedQuota *int64   `json:"period_used_quota,omitempty"`
}

func maxTokenQuota() int {
	quota, err := common.WalletQuotaFromDecimalStrict(
		decimal.NewFromInt(1_000_000_000).Mul(decimal.NewFromFloat(common.QuotaPerUnit)),
	)
	if err != nil {
		return common.MaxWalletQuota
	}
	return quota
}

func buildMaskedTokenResponse(token *model.Token) *tokenResponse {
	if token == nil {
		return nil
	}
	maskedToken := *token
	maskedToken.Key = token.GetMaskedKey()
	autoGroups, err := token.GetAutoGroups()
	if err != nil {
		common.SysError(fmt.Sprintf("failed to parse auto groups for token %d: %v", token.Id, err))
		autoGroups = nil
	}
	if len(autoGroups) == 0 {
		autoGroups = nil
	}
	return &tokenResponse{Token: &maskedToken, AutoGroups: autoGroups}
}

func buildMaskedTokenResponses(tokens []*model.Token) []*tokenResponse {
	maskedTokens := make([]*tokenResponse, 0, len(tokens))
	for _, token := range tokens {
		maskedTokens = append(maskedTokens, buildMaskedTokenResponse(token))
	}
	return maskedTokens
}

func getTokenRequestUserGroup(c *gin.Context) (string, error) {
	if userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup); userGroup != "" {
		return userGroup, nil
	}
	if userGroup := c.GetString("group"); userGroup != "" {
		return userGroup, nil
	}
	return model.GetUserGroup(c.GetInt("id"), false)
}

func setTokenAutoGroups(c *gin.Context, token *model.Token, groups []string) bool {
	if len(groups) == 0 {
		if err := token.SetAutoGroups(nil); err != nil {
			common.ApiError(c, err)
			return false
		}
		return true
	}

	maxCount := setting.GetMaxTokenAutoGroups()
	if len(groups) > maxCount {
		common.ApiErrorI18n(c, i18n.MsgTokenAutoGroupsTooMany, map[string]any{"Max": maxCount})
		return false
	}

	userGroup, err := getTokenRequestUserGroup(c)
	if err != nil {
		common.ApiError(c, err)
		return false
	}
	seen := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		if _, ok := seen[group]; ok {
			common.ApiErrorI18n(c, i18n.MsgTokenAutoGroupsDuplicate, map[string]any{"Group": group})
			return false
		}
		seen[group] = struct{}{}
		if !service.IsUserSelectableGroup(userGroup, group) {
			common.ApiErrorI18n(c, i18n.MsgTokenAutoGroupsInvalid, map[string]any{"Group": group})
			return false
		}
	}

	if err := token.SetAutoGroups(groups); err != nil {
		common.ApiError(c, err)
		return false
	}
	return true
}

// parseTokenUsageRange uses a half-open interval so adjacent calendar days do not overlap.
func parseTokenUsageRange(c *gin.Context) (start, end int64, err error) {
	query := c.Request.URL.Query()
	if !query.Has("start_timestamp") && !query.Has("end_timestamp") {
		return 0, 0, nil
	}
	start, startErr := strconv.ParseInt(query.Get("start_timestamp"), 10, 64)
	end, endErr := strconv.ParseInt(query.Get("end_timestamp"), 10, 64)
	if startErr != nil || endErr != nil || start < 0 || end <= start {
		return 0, 0, fmt.Errorf("invalid usage date range: provide start_timestamp and end_timestamp with 0 <= start < end")
	}
	return start, end, nil
}

func buildTokenResponsesWithUsage(c *gin.Context, tokens []*model.Token, start, end int64) ([]*tokenResponse, error) {
	items := buildMaskedTokenResponses(tokens)
	if end == 0 {
		return items, nil
	}
	ids := make([]int, 0, len(tokens))
	for _, token := range tokens {
		ids = append(ids, token.Id)
	}
	usage, err := model.SumTokenQuotaInRange(c.Request.Context(), c.GetInt("id"), ids, start, end)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		quota := usage[item.Id]
		item.PeriodUsedQuota = &quota
	}
	return items, nil
}

func GetAllTokens(c *gin.Context) {
	start, end, err := parseTokenUsageRange(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	userId := c.GetInt("id")
	pageInfo := common.GetPageQuery(c)
	tokens, err := model.GetAllUserTokens(userId, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	items, err := buildTokenResponsesWithUsage(c, tokens, start, end)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	total, _ := model.CountUserTokens(userId)
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

func SearchTokens(c *gin.Context) {
	start, end, err := parseTokenUsageRange(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	userId := c.GetInt("id")
	keyword := c.Query("keyword")
	token := c.Query("token")

	pageInfo := common.GetPageQuery(c)

	tokens, total, err := model.SearchUserTokens(userId, keyword, token, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	items, err := buildTokenResponsesWithUsage(c, tokens, start, end)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

func GetToken(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	userId := c.GetInt("id")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token, err := model.GetTokenByIds(id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, buildMaskedTokenResponse(token))
}

func GetTokenAutoGroups(c *gin.Context) {
	userGroup, err := getTokenRequestUserGroup(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"groups":    service.GetUserAutoGroup(userGroup),
		"max_count": setting.GetMaxTokenAutoGroups(),
	})
}

func GetTokenKey(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	userId := c.GetInt("id")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token, err := model.GetTokenByIds(id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params := tokenAuditParams(c)
	params["id"], params["name"] = token.Id, token.Name
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	common.ApiSuccess(c, gin.H{
		"key": token.GetFullKey(),
	})
}

func GetTokenStatus(c *gin.Context) {
	tokenId := c.GetInt("token_id")
	userId := c.GetInt("id")
	token, err := model.GetTokenByIds(tokenId, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	expiredAt := token.ExpiredTime
	if expiredAt == -1 {
		expiredAt = 0
	}
	c.JSON(http.StatusOK, gin.H{
		"object":          "credit_summary",
		"total_granted":   token.RemainQuota,
		"total_used":      0, // not supported currently
		"total_available": token.RemainQuota,
		"expires_at":      expiredAt * 1000,
	})
}

func GetTokenUserBalance(c *gin.Context) {
	user, err := model.GetUserCache(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}

	generalSetting := operation_setting.GetGeneralSetting()
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, gin.H{
		"quota":                         user.Quota,
		"quota_per_unit":                common.QuotaPerUnit,
		"quota_display_type":            operation_setting.GetQuotaDisplayType(),
		"usd_exchange_rate":             operation_setting.USDExchangeRate,
		"custom_currency_symbol":        generalSetting.CustomCurrencySymbol,
		"custom_currency_exchange_rate": generalSetting.CustomCurrencyExchangeRate,
	})
}

type tokenVideoModelInfo struct {
	ID           string                      `json:"id"`
	Description  string                      `json:"description,omitempty"`
	PriceLabel   string                      `json:"price_label,omitempty"`
	Limitations  []string                    `json:"limitations,omitempty"`
	Capabilities *jsplugin.VideoCapabilities `json:"capabilities,omitempty"`
}

func GetTokenVideoModels(c *gin.Context) {
	token, err := model.GetTokenById(c.GetInt("token_id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	groupRatio := ratio_setting.GetGroupRatio(token.Group)
	if user, err := model.GetUserCache(token.UserId); err == nil {
		if specialRatio, ok := ratio_setting.GetGroupGroupRatio(user.Group, token.Group); ok {
			groupRatio = specialRatio
		}
	}
	c.Header("Cache-Control", "no-store")
	common.ApiSuccess(c, buildTokenVideoModels(model.GetPricing(), token.Group, groupRatio))
}

func buildTokenVideoModels(pricing []model.Pricing, group string, groupRatio float64) []tokenVideoModelInfo {
	result := make([]tokenVideoModelInfo, 0)
	generation := jsplugin.DefaultRegistry.Generation()
	for _, item := range pricing {
		if !common.StringsContains(item.EnableGroup, "all") && !common.StringsContains(item.EnableGroup, group) {
			continue
		}
		declared, registered := generation.CanonicalModel(item.ModelName)
		pluginKey := ""
		if !registered && model.DB != nil {
			if target, resolved := model.ResolveTaskModelAlias(generation, item.ModelName); resolved && target.Declared != "" {
				declared, pluginKey = target.Declared, target.PluginKey
			}
		}
		candidates := generation.LookupEndpointCandidates("POST", "/v1/videos", declared)
		if len(candidates) == 0 && !common.StringsContains(item.EnableGroup, "Video") && !strings.Contains(strings.ToLower(item.ModelName), "video") && !strings.Contains(strings.ToLower(item.ModelName), "seedance") {
			continue
		}
		caps := generation.VideoCapabilitiesForModel(item.ModelName)
		if pluginKey != "" {
			// A resolved alias is pinned to its provider, not every provider sharing the target name.
			for _, candidate := range candidates {
				if candidate.Plugin.Meta.Key != pluginKey {
					continue
				}
				if declaredCaps, exists := candidate.Plugin.Meta.VideoCapabilities[declared]; exists {
					cloned := declaredCaps.Clone()
					caps = &cloned
				}
				break
			}
		}
		limits := videoModelLimitations(item.ModelName)
		if caps != nil {
			limits = videoCapabilityLimitations(caps)
		}
		result = append(result, tokenVideoModelInfo{
			ID: item.ModelName, Description: strings.TrimSpace(item.Description),
			PriceLabel: tokenVideoPriceLabel(item, groupRatio), Limitations: limits,
			Capabilities: caps,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func tokenVideoPriceLabel(item model.Pricing, groupRatio float64) string {
	unitPrice := item.ModelPrice
	usageKeys := billingexpr.UsedUsageKeys(item.BillingExpr)
	// Task billing evaluates the usage expression, so show the cost of one unit from the
	// same expression. Only the unit fact is set: optional facts such as video_input stay
	// absent and select the plain-request branch, as on a request without references.
	// An expression needing any other numeric fact fails and falls back to ModelPrice.
	unitKey := ""
	if usageKeys["seconds"] {
		unitKey = "seconds"
	} else if usageKeys["videos"] {
		unitKey = "videos"
	} else if len(usageKeys) == 1 {
		for key := range usageKeys {
			unitKey = key
		}
	}
	evaluate := func(usage map[string]any) (decimal.Decimal, bool) {
		cost, _, err := billingexpr.RunExprWithRequest(item.BillingExpr, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: usage})
		if err != nil || cost <= 0 {
			return decimal.Zero, false
		}
		return decimal.NewFromFloat(cost), true
	}
	format := func(price decimal.Decimal) string {
		price = price.Mul(decimal.NewFromFloat(groupRatio))
		return strings.TrimRight(strings.TrimRight(price.StringFixed(4), "0"), ".")
	}
	if unitKey != "" {
		if cost, ok := evaluate(map[string]any{unitKey: 1}); ok {
			unitPrice = cost.InexactFloat64()
		}
	}
	price := decimal.NewFromFloat(unitPrice).Mul(decimal.NewFromFloat(groupRatio))
	if price.IsNegative() || price.IsZero() {
		return "按量计费"
	}
	value := strings.TrimRight(strings.TrimRight(price.StringFixed(4), "0"), ".")
	suffix := ""
	switch {
	case unitKey == "seconds":
		suffix = "/秒"
	case unitKey == "videos":
		suffix = "/条"
	}
	if suffix != "" {
		label := "$" + value + suffix
		// Reference-video requests may bill at a different rate; show it when the
		// expression branches on video_input, plus any per-reference-second charge.
		if usageKeys["video_input"] {
			usage := map[string]any{unitKey: 1, "video_input": "video"}
			if usageKeys["reference_seconds"] {
				usage["reference_seconds"] = 0
			}
			if cost, ok := evaluate(usage); ok {
				referenceLabel := ""
				if !cost.Equal(decimal.NewFromFloat(unitPrice)) {
					referenceLabel = "$" + format(cost) + suffix
				}
				if usageKeys["reference_seconds"] {
					usage["reference_seconds"] = 1
					if withReference, ok := evaluate(usage); ok && withReference.GreaterThan(cost) {
						extra := "$" + format(withReference.Sub(cost)) + "/参考秒"
						if referenceLabel == "" {
							referenceLabel = "另加 " + extra
						} else {
							referenceLabel += " + " + extra
						}
					}
				}
				if referenceLabel != "" {
					label += "，参考视频 " + referenceLabel
				}
			}
		}
		return label
	}
	switch item.QuotaType {
	case 2:
		return "$" + value + "/秒"
	case 1:
		return "$" + value + "/条"
	default:
		return "$" + value
	}
}

func videoCapabilityLimitations(caps *jsplugin.VideoCapabilities) []string {
	limits := []string{"单次生成 1 条"}
	if caps.MaxPromptChars != nil {
		limits = append(limits, fmt.Sprintf("提示词最多 %d 字", *caps.MaxPromptChars))
	}
	if len(caps.Resolutions) > 0 {
		limits = append(limits, "分辨率 "+strings.Join(caps.Resolutions, "/"))
	}
	if len(caps.Ratios) > 0 {
		limits = append(limits, "比例 "+strings.Join(caps.Ratios, "/"))
	}
	if len(caps.SecondsSteps) > 0 {
		steps := make([]string, len(caps.SecondsSteps))
		for i, seconds := range caps.SecondsSteps {
			steps[i] = strconv.Itoa(seconds)
		}
		limits = append(limits, "时长 "+strings.Join(steps, "/")+" 秒")
	} else if caps.MinSeconds != nil && caps.MaxSeconds != nil {
		limits = append(limits, fmt.Sprintf("时长 %d–%d 秒", *caps.MinSeconds, *caps.MaxSeconds))
	}
	if caps.Frames != nil {
		if *caps.Frames {
			limits = append(limits, "支持首尾帧")
		} else {
			limits = append(limits, "不支持首尾帧")
		}
	}
	for _, field := range []struct {
		label string
		value *int
	}{{"参考图", caps.MaxImages}, {"参考视频", caps.MaxVideos}, {"参考音频", caps.MaxAudios}} {
		if field.value != nil {
			limits = append(limits, fmt.Sprintf("%s最多 %d", field.label, *field.value))
		}
	}
	if caps.ReferenceDurationRequired != nil && *caps.ReferenceDurationRequired {
		limits = append(limits, "参考视频须提供源片时长")
	}
	return append(limits, "其他限制以模型接口校验为准")
}

func videoModelLimitations(modelName string) []string {
	name := strings.ToLower(strings.TrimSpace(modelName))
	if !strings.HasPrefix(name, "seedance-") {
		return []string{"功能限制以模型接口校验为准"}
	}
	limits := []string{"单次生成 1 条"}
	for _, resolution := range []string{"480p", "720p", "1080p", "4k"} {
		if strings.Contains(name, "-"+resolution+"-") {
			label := resolution
			if resolution == "4k" {
				label = "4K"
			}
			limits = append(limits, "分辨率 "+label)
			break
		}
	}
	channel := ""
	if index := strings.LastIndex(name, "-c"); index >= 0 {
		channel = name[index+2:]
	}
	switch channel {
	case "47", "48", "50":
		limits = append(limits, "支持文生视频、图生视频", "时长 4–15 秒")
	case "49":
		limits = append(limits, "支持文生视频、图生视频、首尾帧", "时长 4–15 秒")
	default:
		limits = append(limits, "支持文生视频、图生视频、首尾帧")
	}
	limits = append(limits, "提示词最多 2000 字")
	return limits
}

func GetTokenUsage(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "No Authorization header",
		})
		return
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success": false,
			"message": "Invalid Bearer token",
		})
		return
	}
	tokenKey := parts[1]

	token, err := model.GetTokenByKey(strings.TrimPrefix(tokenKey, "sk-"), false)
	if err != nil {
		common.SysError("failed to get token by key: " + err.Error())
		common.ApiErrorI18n(c, i18n.MsgTokenGetInfoFailed)
		return
	}

	expiredAt := token.ExpiredTime
	if expiredAt == -1 {
		expiredAt = 0
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    true,
		"message": "ok",
		"data": gin.H{
			"object":               "token_usage",
			"name":                 token.Name,
			"total_granted":        token.RemainQuota + token.UsedQuota,
			"total_used":           token.UsedQuota,
			"total_available":      token.RemainQuota,
			"unlimited_quota":      token.UnlimitedQuota,
			"model_limits":         token.GetModelLimitsMap(),
			"model_limits_enabled": token.ModelLimitsEnabled,
			"expires_at":           expiredAt,
		},
	})
}

func AddToken(c *gin.Context) {
	request := tokenRequest{}
	err := c.ShouldBindJSON(&request)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token := request.Token
	if len(token.Name) > 50 {
		common.ApiErrorI18n(c, i18n.MsgTokenNameTooLong)
		return
	}
	params := tokenAuditParams(c)
	params["name"] = token.Name
	// 非无限额度时，检查额度值是否超出有效范围
	if !token.UnlimitedQuota {
		if token.RemainQuota < 0 {
			common.ApiErrorI18n(c, i18n.MsgTokenQuotaNegative)
			return
		}
		maxQuotaValue := maxTokenQuota()
		if token.RemainQuota > maxQuotaValue {
			common.ApiErrorI18n(c, i18n.MsgTokenQuotaExceedMax, map[string]any{"Max": maxQuotaValue})
			return
		}
	}
	// 检查用户令牌数量是否已达上限
	maxTokens := operation_setting.GetMaxUserTokens()
	count, err := model.CountUserTokens(c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if int(count) >= maxTokens {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": fmt.Sprintf("已达到最大令牌数量限制 (%d)", maxTokens),
		})
		return
	}
	if token.Group == "auto" {
		if !setTokenAutoGroups(c, &token, request.AutoGroups.Groups) {
			return
		}
	} else {
		token.CrossGroupRetry = false
		_ = token.SetAutoGroups(nil)
	}
	key, err := common.GenerateKey()
	if err != nil {
		common.ApiErrorI18n(c, i18n.MsgTokenGenerateFailed)
		common.SysLog("failed to generate token key: " + err.Error())
		return
	}
	cleanToken := model.Token{
		UserId:             c.GetInt("id"),
		Name:               token.Name,
		Key:                key,
		CreatedTime:        common.GetTimestamp(),
		AccessedTime:       common.GetTimestamp(),
		ExpiredTime:        token.ExpiredTime,
		RemainQuota:        token.RemainQuota,
		UnlimitedQuota:     token.UnlimitedQuota,
		ModelLimitsEnabled: token.ModelLimitsEnabled,
		ModelLimits:        token.ModelLimits,
		AllowIps:           token.AllowIps,
		Group:              token.Group,
		CrossGroupRetry:    token.CrossGroupRetry,
		AutoGroups:         token.AutoGroups,
	}
	err = cleanToken.Insert()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params["id"] = cleanToken.Id
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

func DeleteToken(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	userId := c.GetInt("id")
	token, err := model.GetTokenByIds(id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params := tokenAuditParams(c)
	params["id"], params["name"] = token.Id, token.Name
	err = token.Delete()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
}

func UpdateToken(c *gin.Context) {
	userId := c.GetInt("id")
	statusOnly := c.Query("status_only")
	request := tokenRequest{}
	err := c.ShouldBindJSON(&request)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	token := request.Token
	params := tokenAuditParams(c)
	if token.Id > 0 {
		params["id"] = token.Id
	}
	if len(token.Name) > 50 {
		common.ApiErrorI18n(c, i18n.MsgTokenNameTooLong)
		return
	}
	if !token.UnlimitedQuota {
		if token.RemainQuota < 0 {
			common.ApiErrorI18n(c, i18n.MsgTokenQuotaNegative)
			return
		}
		maxQuotaValue := maxTokenQuota()
		if token.RemainQuota > maxQuotaValue {
			common.ApiErrorI18n(c, i18n.MsgTokenQuotaExceedMax, map[string]any{"Max": maxQuotaValue})
			return
		}
	}
	cleanToken, err := model.GetTokenByIds(token.Id, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params["name"] = cleanToken.Name
	previous := *cleanToken
	if token.Status == common.TokenStatusEnabled {
		if cleanToken.Status == common.TokenStatusExpired && cleanToken.ExpiredTime <= common.GetTimestamp() && cleanToken.ExpiredTime != -1 {
			common.ApiErrorI18n(c, i18n.MsgTokenExpiredCannotEnable)
			return
		}
		if cleanToken.Status == common.TokenStatusExhausted && cleanToken.RemainQuota <= 0 && !cleanToken.UnlimitedQuota {
			common.ApiErrorI18n(c, i18n.MsgTokenExhaustedCannotEable)
			return
		}
	}
	if statusOnly != "" {
		cleanToken.Status = token.Status
	} else {
		// If you add more fields, please also update token.Update()
		cleanToken.Name = token.Name
		cleanToken.ExpiredTime = token.ExpiredTime
		cleanToken.RemainQuota = token.RemainQuota
		cleanToken.UnlimitedQuota = token.UnlimitedQuota
		cleanToken.ModelLimitsEnabled = token.ModelLimitsEnabled
		cleanToken.ModelLimits = token.ModelLimits
		cleanToken.AllowIps = token.AllowIps
		cleanToken.Group = token.Group
		cleanToken.CrossGroupRetry = token.CrossGroupRetry
		if token.Group != "auto" {
			cleanToken.CrossGroupRetry = false
			_ = cleanToken.SetAutoGroups(nil)
		} else if request.AutoGroups.Set {
			if !setTokenAutoGroups(c, cleanToken, request.AutoGroups.Groups) {
				return
			}
		}
	}
	err = cleanToken.Update()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params["name"] = cleanToken.Name
	if statusOnly != "" {
		params["from"], params["to"] = previous.Status, cleanToken.Status
	} else {
		changedFields := []string{}
		for _, field := range []struct {
			name    string
			changed bool
		}{
			{"name", previous.Name != cleanToken.Name},
			{"expired_time", previous.ExpiredTime != cleanToken.ExpiredTime},
			{"remain_quota", previous.RemainQuota != cleanToken.RemainQuota},
			{"unlimited_quota", previous.UnlimitedQuota != cleanToken.UnlimitedQuota},
			{"model_limits_enabled", previous.ModelLimitsEnabled != cleanToken.ModelLimitsEnabled},
			{"model_limits", previous.ModelLimits != cleanToken.ModelLimits},
			{"allow_ips", (previous.AllowIps == nil) != (cleanToken.AllowIps == nil) ||
				(previous.AllowIps != nil && cleanToken.AllowIps != nil && *previous.AllowIps != *cleanToken.AllowIps)},
			{"group", previous.Group != cleanToken.Group},
			{"cross_group_retry", previous.CrossGroupRetry != cleanToken.CrossGroupRetry},
			{"auto_groups", previous.AutoGroups != cleanToken.AutoGroups},
		} {
			if field.changed {
				changedFields = append(changedFields, field.name)
			}
		}
		params["changed_fields"] = changedFields
	}
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    buildMaskedTokenResponse(cleanToken),
	})
}

type TokenBatch struct {
	Ids []int `json:"ids"`
}

func DeleteTokenBatch(c *gin.Context) {
	tokenBatch := TokenBatch{}
	if err := c.ShouldBindJSON(&tokenBatch); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	params := tokenBatchAuditParams(c, tokenBatch.Ids)
	if len(tokenBatch.Ids) == 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	userId := c.GetInt("id")
	count, err := model.BatchDeleteTokens(tokenBatch.Ids, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	params["count"] = count
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    count,
	})
}

func GetTokenKeysBatch(c *gin.Context) {
	tokenBatch := TokenBatch{}
	if err := c.ShouldBindJSON(&tokenBatch); err != nil {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	params := tokenBatchAuditParams(c, tokenBatch.Ids)
	if len(tokenBatch.Ids) == 0 {
		common.ApiErrorI18n(c, i18n.MsgInvalidParams)
		return
	}
	if len(tokenBatch.Ids) > 100 {
		common.ApiErrorI18n(c, i18n.MsgBatchTooMany, map[string]any{"Max": 100})
		return
	}
	userId := c.GetInt("id")
	tokens, err := model.GetTokenKeysByIds(tokenBatch.Ids, userId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	keysMap := make(map[int]string)
	returnedIDs := make([]int, 0, len(tokens))
	for _, t := range tokens {
		keysMap[t.Id] = t.GetFullKey()
		returnedIDs = append(returnedIDs, t.Id)
	}
	params["count"] = len(tokens)
	params["returned_ids"] = returnedIDs
	common.SetContextKey(c, constant.ContextKeyTokenAuditSucceeded, true)
	common.ApiSuccess(c, gin.H{"keys": keysMap})
}
