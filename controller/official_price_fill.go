package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

const officialPricePresetURL = officialRatioPresetBaseURL + "/llm-metadata/api/newapi/ratio_config-v1-base.json"

var (
	officialPriceFillMu       sync.Mutex
	officialSnapshotDateRegex = regexp.MustCompile(`-\d{4}-\d{2}-\d{2}$`)
	officialPriceAliases      = map[string]string{
		"gpt-5.5-none": "gpt-5.5",
		"grok-build":   "grok-build-0.1",
	}
)

type OfficialPriceFillRequest struct {
	DryRun bool `json:"dry_run"`
}

type OfficialPriceFillItem struct {
	Model       string         `json:"model"`
	SourceModel string         `json:"source_model"`
	Fields      map[string]any `json:"fields"`
}

type OfficialPriceFillSkip struct {
	Model  string `json:"model"`
	Reason string `json:"reason"`
}

type OfficialPriceFillResult struct {
	DryRun        bool                    `json:"dry_run"`
	Source        string                  `json:"source"`
	EnabledModels int                     `json:"enabled_models"`
	AlreadyPriced int                     `json:"already_priced"`
	FilledModels  []OfficialPriceFillItem `json:"filled_models"`
	SkippedModels []OfficialPriceFillSkip `json:"skipped_models"`
}

type officialPriceFillPlan struct {
	Result        OfficialPriceFillResult
	PricingData   map[string]any
	ChangedFields map[string]struct{}
}

type officialPricingField struct {
	SyncKey   string
	OptionKey string
}

var officialPricingFields = []officialPricingField{
	{SyncKey: "model_ratio", OptionKey: "ModelRatio"},
	{SyncKey: "completion_ratio", OptionKey: "CompletionRatio"},
	{SyncKey: "cache_ratio", OptionKey: "CacheRatio"},
	{SyncKey: "create_cache_ratio", OptionKey: "CreateCacheRatio"},
	{SyncKey: "image_ratio", OptionKey: "ImageRatio"},
	{SyncKey: "audio_ratio", OptionKey: "AudioRatio"},
	{SyncKey: "audio_completion_ratio", OptionKey: "AudioCompletionRatio"},
	{SyncKey: "model_price", OptionKey: "ModelPrice"},
	{SyncKey: billing_setting.BillingModeField, OptionKey: "billing_setting.billing_mode"},
	{SyncKey: billing_setting.BillingExprField, OptionKey: "billing_setting.billing_expr"},
}

func FillOfficialModelPrices(c *gin.Context) {
	var req OfficialPriceFillRequest
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求参数格式错误"})
		return
	}

	officialPriceFillMu.Lock()
	defer officialPriceFillMu.Unlock()

	officialData, err := fetchOfficialPricePreset(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "获取官方价格预设失败: " + err.Error()})
		return
	}

	abilities, err := model.GetAllEnableAbilityWithChannels()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "查询已启用模型失败: " + err.Error()})
		return
	}
	enabledModelSet := make(map[string]struct{}, len(abilities))
	for _, ability := range abilities {
		if modelName := strings.TrimSpace(ability.Model); modelName != "" {
			enabledModelSet[modelName] = struct{}{}
		}
	}
	enabledModels := make([]string, 0, len(enabledModelSet))
	for modelName := range enabledModelSet {
		enabledModels = append(enabledModels, modelName)
	}

	plan := buildOfficialPriceFillPlan(enabledModels, getLocalPricingSyncData(), officialData)
	plan.Result.DryRun = req.DryRun
	plan.Result.Source = officialPricePresetURL

	if !req.DryRun && len(plan.Result.FilledModels) > 0 {
		optionValues, err := officialPriceFillOptionValues(plan)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "生成价格配置失败: " + err.Error()})
			return
		}
		if err := model.UpdateOptionsBulk(optionValues); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "保存官方价格失败: " + err.Error()})
			return
		}
		model.RefreshPricing()
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": plan.Result})
}

func fetchOfficialPricePreset(ctx context.Context) (map[string]any, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, officialPricePresetURL, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: time.Duration(defaultTimeoutSeconds) * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s", response.Status)
	}

	limited := io.LimitReader(response.Body, maxRatioConfigBytes+1)
	var payload struct {
		Success bool           `json:"success"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	}
	if err := common.DecodeJson(limited, &payload); err != nil {
		return nil, err
	}
	if !payload.Success {
		return nil, fmt.Errorf("preset rejected request: %s", payload.Message)
	}
	if len(payload.Data) == 0 {
		return nil, fmt.Errorf("empty official price preset")
	}
	return addOfficialLongContextPricing(payload.Data), nil
}

func addOfficialLongContextPricing(data map[string]any) map[string]any {
	result := cloneOfficialPricingData(data)
	modelRatios := valueMap(result["model_ratio"])
	completionRatios := valueMap(result["completion_ratio"])
	cacheRatios := valueMap(result["cache_ratio"])
	billingModes := ensureOfficialFieldMap(result, billing_setting.BillingModeField)
	billingExprs := ensureOfficialFieldMap(result, billing_setting.BillingExprField)

	for _, modelName := range []string{"gpt-5.4", "gpt-5.4-pro", "gpt-5.5"} {
		modelRatio, ratioOK := asFloat64(modelRatios[modelName])
		completionRatio, completionOK := asFloat64(completionRatios[modelName])
		if !ratioOK || !completionOK || modelRatio <= 0 || completionRatio <= 0 {
			continue
		}
		inputPrice := modelRatio * 2
		outputPrice := inputPrice * completionRatio
		standardTerms := []string{"p * " + formatOfficialPrice(inputPrice), "c * " + formatOfficialPrice(outputPrice)}
		longTerms := []string{"p * " + formatOfficialPrice(inputPrice*2), "c * " + formatOfficialPrice(outputPrice*1.5)}
		if cacheRatio, ok := asFloat64(cacheRatios[modelName]); ok && cacheRatio >= 0 {
			cachePrice := inputPrice * cacheRatio
			standardTerms = append(standardTerms, "cr * "+formatOfficialPrice(cachePrice))
			longTerms = append(longTerms, "cr * "+formatOfficialPrice(cachePrice*2))
		}
		expr := fmt.Sprintf(`len <= 272000 ? tier("standard", %s) : tier("long_context", %s)`, strings.Join(standardTerms, " + "), strings.Join(longTerms, " + "))
		if err := billing_setting.SmokeTestExpr(expr); err != nil {
			continue
		}
		billingModes[modelName] = billing_setting.BillingModeTieredExpr
		billingExprs[modelName] = expr
	}
	return result
}

func formatOfficialPrice(value float64) string {
	return strconv.FormatFloat(roundRatioValue(value), 'f', -1, 64)
}

func cloneOfficialPricingData(data map[string]any) map[string]any {
	result := make(map[string]any, len(officialPricingFields))
	for _, field := range officialPricingFields {
		values := valueMap(data[field.SyncKey])
		copyValues := make(map[string]any, len(values))
		for key, value := range values {
			copyValues[key] = value
		}
		result[field.SyncKey] = copyValues
	}
	return result
}

func ensureOfficialFieldMap(data map[string]any, field string) map[string]any {
	values := valueMap(data[field])
	if values == nil {
		values = make(map[string]any)
		data[field] = values
	}
	return values
}

func buildOfficialPriceFillPlan(enabledModels []string, localData map[string]any, officialData map[string]any) officialPriceFillPlan {
	sort.Strings(enabledModels)
	local := cloneOfficialPricingData(localData)
	official := cloneOfficialPricingData(officialData)
	plan := officialPriceFillPlan{
		Result: OfficialPriceFillResult{
			EnabledModels: len(enabledModels),
			FilledModels:  make([]OfficialPriceFillItem, 0),
			SkippedModels: make([]OfficialPriceFillSkip, 0),
		},
		PricingData:   local,
		ChangedFields: make(map[string]struct{}),
	}

	for _, modelName := range enabledModels {
		if hasOfficialBasePricing(local, modelName) {
			plan.Result.AlreadyPriced++
			continue
		}
		sourceModel, ok := resolveOfficialPriceModel(modelName, official)
		if !ok {
			plan.Result.SkippedModels = append(plan.Result.SkippedModels, OfficialPriceFillSkip{
				Model:  modelName,
				Reason: "official price not found",
			})
			continue
		}

		changes := make(map[string]any)
		for _, field := range officialPricingFields {
			sourceValues := valueMap(official[field.SyncKey])
			value, exists := sourceValues[sourceModel]
			if !exists {
				continue
			}
			targetValues := ensureOfficialFieldMap(local, field.SyncKey)
			if _, exists := targetValues[modelName]; exists {
				continue
			}
			targetValues[modelName] = normalizeSyncValue(field.SyncKey, value)
			changes[field.SyncKey] = normalizeSyncValue(field.SyncKey, value)
			plan.ChangedFields[field.SyncKey] = struct{}{}
		}
		if !hasOfficialBasePricing(local, modelName) {
			plan.Result.SkippedModels = append(plan.Result.SkippedModels, OfficialPriceFillSkip{
				Model:  modelName,
				Reason: "official base price not found",
			})
			continue
		}
		plan.Result.FilledModels = append(plan.Result.FilledModels, OfficialPriceFillItem{
			Model:       modelName,
			SourceModel: sourceModel,
			Fields:      changes,
		})
	}
	return plan
}

func hasOfficialBasePricing(data map[string]any, modelName string) bool {
	matchingName := ratio_setting.FormatMatchingModelName(modelName)
	for _, field := range []string{"model_price", "model_ratio"} {
		values := valueMap(data[field])
		if _, ok := values[matchingName]; ok {
			return true
		}
		if strings.HasSuffix(matchingName, ratio_setting.CompactModelSuffix) {
			if _, ok := values[ratio_setting.CompactWildcardModelKey]; ok {
				return true
			}
		}
	}
	modes := valueMap(data[billing_setting.BillingModeField])
	exprs := valueMap(data[billing_setting.BillingExprField])
	mode, _ := modes[modelName].(string)
	expr, _ := exprs[modelName].(string)
	return mode == billing_setting.BillingModeTieredExpr && strings.TrimSpace(expr) != ""
}

func resolveOfficialPriceModel(modelName string, officialData map[string]any) (string, bool) {
	candidates := make([]string, 0, 8)
	seen := make(map[string]struct{})
	addCandidate := func(candidate string) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			return
		}
		if _, exists := seen[candidate]; exists {
			return
		}
		seen[candidate] = struct{}{}
		candidates = append(candidates, candidate)
	}

	addCandidate(modelName)
	if strings.HasSuffix(modelName, ratio_setting.CompactModelSuffix) {
		addCandidate(strings.TrimSuffix(modelName, ratio_setting.CompactModelSuffix))
	}
	for index := 0; index < len(candidates); index++ {
		candidate := candidates[index]
		if alias, ok := officialPriceAliases[candidate]; ok {
			addCandidate(alias)
		}
		addCandidate(officialSnapshotDateRegex.ReplaceAllString(candidate, ""))
	}
	for _, candidate := range candidates {
		if hasOfficialBasePricing(officialData, candidate) {
			return candidate, true
		}
	}
	return "", false
}

func officialPriceFillOptionValues(plan officialPriceFillPlan) (map[string]string, error) {
	values := make(map[string]string, len(plan.ChangedFields))
	for _, field := range officialPricingFields {
		if _, changed := plan.ChangedFields[field.SyncKey]; !changed {
			continue
		}
		encoded, err := common.Marshal(valueMap(plan.PricingData[field.SyncKey]))
		if err != nil {
			return nil, err
		}
		values[field.OptionKey] = string(encoded)
	}
	return values, nil
}
