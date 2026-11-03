package service

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/bytedance/gopkg/util/gopool"
)

const (
	aistarsLabDefaultConfigURL       = "https://api.video.aistarslab.com/openapi/generation/config"
	aistarsLabDefaultChannelID       = 17
	aistarsLabDefaultCreditRate      = 100
	aistarsLabDefaultMarkupRate      = 1.3
	aistarsLabDefaultIntervalMinutes = 30
	aistarsLabRequestTimeout         = 30 * time.Second
)

var (
	aistarsLabAliasPattern = regexp.MustCompile(`^seedance-[a-z0-9.-]+-c[0-9]+$`)
	aistarsLabRawPattern   = regexp.MustCompile(`^([0-9]+:)?seedance-2\.[0-9]+`)
	aistarsLabSyncOnce     sync.Once
	aistarsLabSyncRunning  atomic.Bool
)

type AistarsLabSyncRequest struct {
	PreviewToken string  `json:"preview_token,omitempty"`
	DryRun       bool    `json:"dry_run"`
	ChannelID    int     `json:"channel_id"`
	ConfigURL    string  `json:"config_url"`
	CreditRate   float64 `json:"credit_rate"`
	MarkupRate   float64 `json:"markup_rate"`
}

type AistarsLabSyncResult struct {
	Warnings              []string                     `json:"warnings"`
	PluginVersion         string                       `json:"plugin_version"`
	PreviousPluginVersion string                       `json:"previous_plugin_version"`
	PreviewToken          string                       `json:"preview_token"`
	CapabilityChanges     []AistarsLabExpressionChange `json:"capability_changes"`
	DryRun                bool                         `json:"dry_run"`
	ChannelID             int                          `json:"channel_id"`
	ConfigURL             string                       `json:"config_url"`
	CreditRate            float64                      `json:"credit_rate"`
	MarkupRate            float64                      `json:"markup_rate"`
	TotalModels           int                          `json:"total_models"`
	AddedModels           []string                     `json:"added_models"`
	RemovedModels         []string                     `json:"removed_models"`
	ExpressionChanges     []AistarsLabExpressionChange `json:"expression_changes"`
	ModeChanges           []AistarsLabExpressionChange `json:"mode_changes"`
	PriceChanges          []AistarsLabPriceChange      `json:"price_changes"`
	MappingChanges        []AistarsLabMappingChange    `json:"mapping_changes"`
	Models                []AistarsLabSeedanceModel    `json:"models"`
}

type AistarsLabExpressionChange struct {
	Model string `json:"model"`
	Old   string `json:"old,omitempty"`
	New   string `json:"new,omitempty"`
}

type AistarsLabPriceChange struct {
	Model string   `json:"model"`
	Old   *float64 `json:"old,omitempty"`
	New   *float64 `json:"new,omitempty"`
}

type AistarsLabMappingChange struct {
	Model string `json:"model"`
	Old   string `json:"old,omitempty"`
	New   string `json:"new,omitempty"`
}

type AistarsLabSeedanceModel struct {
	PublicModel       string   `json:"public_model"`
	UpstreamModel     string   `json:"upstream_model"`
	Channel           string   `json:"channel"`
	Quality           string   `json:"quality"`
	BillingUnit       string   `json:"billing_unit"`
	Price             float64  `json:"price"`
	BillingExpression string   `json:"billing_expression"`
	RawCredits        float64  `json:"raw_credits"`
	Modes             []string `json:"modes,omitempty"`
	AspectRatios      []string `json:"aspect_ratios,omitempty"`
	DurationMin       *int     `json:"duration_min,omitempty"`
	DurationMax       *int     `json:"duration_max,omitempty"`
	DurationOptions   []int    `json:"duration_options,omitempty"`
	InputImagesMax    int      `json:"input_images_max"`
	InputVideosMax    int      `json:"input_videos_max"`
	InputAudiosMax    int      `json:"input_audios_max"`
	DefaultOption     bool     `json:"default_option"`
	SourceTitle       string   `json:"source_title,omitempty"`
	SourceDescription string   `json:"source_description,omitempty"`
}

type aistarsLabConfigResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		VideoConfig []aistarsLabVideoConfig `json:"videoConfig"`
	} `json:"data"`
}

type aistarsLabVideoConfig struct {
	Channel       string                  `json:"channel"`
	Title         string                  `json:"title"`
	Description   string                  `json:"description"`
	DefaultOption bool                    `json:"defaultOption"`
	Models        []aistarsLabConfigModel `json:"models"`
}

type aistarsLabConfigModel struct {
	Model          string              `json:"model"`
	Qualities      []aistarsLabQuality `json:"qualities"`
	Modes          []string            `json:"modes"`
	AspectRatios   []string            `json:"aspectRatios"`
	Duration       aistarsLabDuration  `json:"duration"`
	InputImagesMax *int                `json:"inputImagesMax"`
	InputVideosMax *int                `json:"inputVideosMax"`
	InputAudiosMax *int                `json:"inputAudiosMax"`
}

type aistarsLabQuality struct {
	Quality string `json:"quality"`
	Pricing struct {
		Type                 string   `json:"type"`
		Credits              *float64 `json:"credits"`
		InputVideoMultiplier *float64 `json:"inputVideoMultiplier"`
	} `json:"pricing"`
}

type aistarsLabDuration struct {
	Min     *int  `json:"min"`
	Max     *int  `json:"max"`
	Options []int `json:"options"`
}

func StartAistarsLabConfigSyncTask() {
	aistarsLabSyncOnce.Do(func() {
		if !common.IsMasterNode || !common.GetEnvOrDefaultBool("AISTARSLAB_CONFIG_SYNC_ENABLED", false) {
			return
		}
		minutes := common.GetEnvOrDefault("AISTARSLAB_CONFIG_SYNC_INTERVAL_MINUTES", aistarsLabDefaultIntervalMinutes)
		if minutes < 1 {
			minutes = aistarsLabDefaultIntervalMinutes
		}
		interval := time.Duration(minutes) * time.Minute
		gopool.Go(func() {
			runAistarsLabConfigSyncOnce()
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for range ticker.C {
				runAistarsLabConfigSyncOnce()
			}
		})
	})
}

func runAistarsLabConfigSyncOnce() {
	if !aistarsLabSyncRunning.CompareAndSwap(false, true) {
		return
	}
	defer aistarsLabSyncRunning.Store(false)
	request := AistarsLabSyncRequest{DryRun: true}
	preview, err := SyncAistarsLabConfig(context.Background(), request)
	if err != nil {
		logger.LogError(context.Background(), "AistarsLab config preview failed: "+err.Error())
		return
	}
	request.DryRun = false
	request.PreviewToken = preview.PreviewToken
	result, err := SyncAistarsLabConfig(context.Background(), request)
	if err != nil {
		logger.LogError(context.Background(), "AistarsLab config sync failed: "+err.Error())
		return
	}
	logger.LogInfo(context.Background(), fmt.Sprintf("AistarsLab config sync finished: models=%d added=%d removed=%d", result.TotalModels, len(result.AddedModels), len(result.RemovedModels)))
}

func SyncAistarsLabConfig(ctx context.Context, request AistarsLabSyncRequest) (*AistarsLabSyncResult, error) {
	request = normalizeAistarsLabSyncRequest(request)
	apiKey, err := getAistarsLabConfigAPIKey(request.ChannelID)
	if err != nil {
		return nil, err
	}
	config, err := fetchAistarsLabConfig(ctx, request.ConfigURL, apiKey)
	if err != nil {
		return nil, err
	}
	if math.IsNaN(request.CreditRate) || math.IsInf(request.CreditRate, 0) || math.IsNaN(request.MarkupRate) || math.IsInf(request.MarkupRate, 0) {
		return nil, fmt.Errorf("pricing rates must be finite")
	}
	for _, line := range config.Data.VideoConfig {
		if !regexp.MustCompile(`^[0-9]+$`).MatchString(line.Channel) {
			return nil, fmt.Errorf("invalid upstream route ID")
		}
		for _, entry := range line.Models {
			if strings.TrimSpace(entry.Model) == "" || len(entry.Qualities) == 0 {
				return nil, fmt.Errorf("upstream omitted a video model or its qualities")
			}
			for _, quality := range entry.Qualities {
				if entry.InputVideosMax != nil && *entry.InputVideosMax > 0 && quality.Pricing.InputVideoMultiplier == nil {
					return nil, fmt.Errorf("upstream omitted reference-video pricing for %s", entry.Model)
				}
				if strings.TrimSpace(quality.Quality) == "" || normalizeAistarsLabBillingUnit(quality.Pricing.Type) == "" || quality.Pricing.Credits == nil || *quality.Pricing.Credits < 0 || math.IsNaN(*quality.Pricing.Credits) || math.IsInf(*quality.Pricing.Credits, 0) || (quality.Pricing.InputVideoMultiplier != nil && (*quality.Pricing.InputVideoMultiplier <= 0 || math.IsNaN(*quality.Pricing.InputVideoMultiplier) || math.IsInf(*quality.Pricing.InputVideoMultiplier, 0))) {
					return nil, fmt.Errorf("unsupported or invalid upstream pricing for %s", entry.Model)
				}
			}
		}
	}
	models := flattenAistarsLabSeedanceModels(config.Data.VideoConfig, request.CreditRate, request.MarkupRate)
	if len(models) == 0 {
		return nil, fmt.Errorf("no supported video models found in AistarsLab config")
	}
	plugin, loaded, err := buildAistarsLabSyncedPlugin(models)
	if err != nil {
		return nil, err
	}
	channel, err := model.GetChannelById(request.ChannelID, true)
	if err != nil {
		return nil, err
	}
	if channel.Type != constant.ChannelTypeTaskPlugin || channel.GetSetting().TaskPluginKey != "aistarslab" {
		return nil, fmt.Errorf("select an AistarsLab task-plugin channel")
	}
	names := channel.GetModels()
	for _, item := range models {
		names = append(names, item.PublicModel)
	}
	snapshot, err := model.GetModelPricingSnapshot(names)
	if err != nil {
		return nil, err
	}
	versions := make(map[string]string, len(snapshot.Entries))
	for _, item := range snapshot.Entries {
		versions[item.ModelName] = item.Version
	}
	result := buildAistarsLabSyncResult(request, models, snapshot, channel)
	var current model.TaskPlugin
	lookupErr := model.DB.Where(&model.TaskPlugin{Key: "aistarslab", Active: true}).First(&current).Error
	if lookupErr != nil && lookupErr != gorm.ErrRecordNotFound {
		return nil, lookupErr
	}
	result.PluginVersion = plugin.Version
	result.PreviousPluginVersion = current.Version
	result.Warnings = []string{}
	if current.Source != "" && current.SourceHash != plugin.SourceHash {
		result.Warnings = append(result.Warnings, "This sync replaces the current AistarsLab plugin source and validation. Review any manual plugin changes before applying.")
	}
	if len(result.RemovedModels) > 0 {
		result.Warnings = append(result.Warnings, "Removed models leave this channel, but their historical pricing is preserved.")
	}
	oldPlugin, _ := jsplugin.DefaultRegistry.Get("aistarslab")
	if current.Source != "" {
		oldPlugin, err = jsplugin.NewRegistry().Register(string(current.Source), jsplugin.Options{})
		if err != nil {
			return nil, err
		}
	}
	result.CapabilityChanges = []AistarsLabExpressionChange{}
	for _, item := range models {
		old := ""
		if oldPlugin != nil {
			if caps, ok := oldPlugin.Meta.VideoCapabilities[item.PublicModel]; ok {
				old = aistarsLabCapabilitiesText(&caps)
			}
		}
		caps := loaded.Meta.VideoCapabilities[item.PublicModel]
		value := aistarsLabCapabilitiesText(&caps)
		if old != value {
			result.CapabilityChanges = append(result.CapabilityChanges, AistarsLabExpressionChange{Model: item.PublicModel, Old: old, New: value})
		}
	}
	for _, name := range result.RemovedModels {
		if oldPlugin != nil {
			if caps, ok := oldPlugin.Meta.VideoCapabilities[name]; ok {
				result.CapabilityChanges = append(result.CapabilityChanges, AistarsLabExpressionChange{Model: name, Old: aistarsLabCapabilitiesText(&caps)})
			}
		}
	}
	result.DryRun = true
	result.PreviewToken = aistarsLabPreviewToken(result, current.SourceHash, channel.Models, channel.GetModelMapping(), versions)
	if request.DryRun {
		return result, nil
	}
	if request.PreviewToken == "" || request.PreviewToken != result.PreviewToken {
		return nil, fmt.Errorf("upstream or local configuration changed; preview again before applying")
	}
	if err := applyAistarsLabCapabilitySync(request, result, channel, plugin, current.SourceHash, versions); err != nil {
		return nil, err
	}
	result.DryRun = false
	model.RefreshPricing()
	return result, nil
}

func normalizeAistarsLabSyncRequest(request AistarsLabSyncRequest) AistarsLabSyncRequest {
	if request.ChannelID <= 0 {
		request.ChannelID = common.GetEnvOrDefault("AISTARSLAB_CONFIG_SYNC_CHANNEL_ID", aistarsLabDefaultChannelID)
	}
	if strings.TrimSpace(request.ConfigURL) == "" {
		request.ConfigURL = strings.TrimSpace(common.GetEnvOrDefaultString("AISTARSLAB_CONFIG_URL", aistarsLabDefaultConfigURL))
	}
	if request.CreditRate <= 0 {
		request.CreditRate = aistarsLabEnvFloat("AISTARSLAB_CREDIT_RATE", aistarsLabDefaultCreditRate)
	}
	if request.MarkupRate <= 0 {
		request.MarkupRate = aistarsLabEnvFloat("AISTARSLAB_MARKUP_RATE", aistarsLabDefaultMarkupRate)
	}
	return request
}

func aistarsLabEnvFloat(name string, fallback float64) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(common.GetEnvOrDefaultString(name, "")), 64)
	if err != nil || value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return fallback
	}
	return value
}

func getAistarsLabConfigAPIKey(channelID int) (string, error) {
	if key := strings.TrimSpace(common.GetEnvOrDefaultString("AISTARSLAB_API_KEY", "")); key != "" {
		return strings.TrimSpace(strings.TrimPrefix(key, "Bearer ")), nil
	}
	channel, err := model.GetChannelById(channelID, true)
	if err != nil {
		return "", fmt.Errorf("get sync channel %d failed: %w", channelID, err)
	}
	key, _, apiErr := channel.GetNextEnabledKey()
	if apiErr != nil {
		return "", fmt.Errorf("get sync channel key failed: %s", apiErr.Error())
	}
	key = strings.TrimSpace(strings.TrimPrefix(key, "Bearer "))
	if key == "" {
		return "", fmt.Errorf("AistarsLab API key is empty")
	}
	return key, nil
}

func fetchAistarsLabConfig(ctx context.Context, configURL, apiKey string) (*aistarsLabConfigResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, aistarsLabRequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, configURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("AistarsLab config returned %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 10<<20))
	if err != nil {
		return nil, err
	}
	var parsed aistarsLabConfigResponse
	if err := common.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if parsed.Code != 0 {
		return nil, fmt.Errorf("AistarsLab config error: code=%d msg=%s", parsed.Code, parsed.Msg)
	}
	return &parsed, nil
}

func flattenAistarsLabSeedanceModels(configs []aistarsLabVideoConfig, creditRate, markupRate float64) []AistarsLabSeedanceModel {
	byAlias := make(map[string]AistarsLabSeedanceModel)
	for _, videoConfig := range configs {
		channel := strings.TrimSpace(videoConfig.Channel)
		if channel == "" {
			continue
		}
		for _, configModel := range videoConfig.Models {
			if strings.TrimSpace(configModel.Model) == "" {
				continue
			}
			for _, quality := range configModel.Qualities {
				unit := normalizeAistarsLabBillingUnit(quality.Pricing.Type)
				alias := buildAistarsLabSeedanceAlias(configModel.Model, quality.Quality, channel)
				if !strings.HasPrefix(strings.ToLower(configModel.Model), "seedance-") {
					alias = "aistarslab-" + strings.ToLower(configModel.Model) + "-" + strings.ToLower(quality.Quality) + "-c" + channel
				}
				if unit == "" || alias == "" || quality.Pricing.Credits == nil {
					continue
				}
				price := math.Round((*quality.Pricing.Credits/creditRate*markupRate)*100) / 100
				fact := "seconds"
				if unit == "per_item" {
					fact = "videos"
				}
				multiplier := 1.0
				if quality.Pricing.InputVideoMultiplier != nil {
					multiplier = *quality.Pricing.InputVideoMultiplier
				}
				item := AistarsLabSeedanceModel{
					PublicModel: alias, UpstreamModel: channel + ":" + configModel.Model, Channel: channel,
					Quality: strings.ToLower(strings.TrimSpace(quality.Quality)), BillingUnit: unit, Price: price,
					BillingExpression: fmt.Sprintf(`tier("base", u("%s") * %s * (u("video_input") == "video" ? %s : 1))`, fact, strconv.FormatFloat(price, 'f', -1, 64), strconv.FormatFloat(multiplier, 'f', -1, 64)),
					RawCredits:        *quality.Pricing.Credits, Modes: append([]string(nil), configModel.Modes...),
					AspectRatios: append([]string(nil), configModel.AspectRatios...), DurationMin: configModel.Duration.Min,
					DurationMax: configModel.Duration.Max, DurationOptions: append([]int(nil), configModel.Duration.Options...), InputImagesMax: aistarsLabInputLimit(configModel.InputImagesMax),
					InputVideosMax: aistarsLabInputLimit(configModel.InputVideosMax), InputAudiosMax: aistarsLabInputLimit(configModel.InputAudiosMax),
					DefaultOption: videoConfig.DefaultOption, SourceTitle: videoConfig.Title, SourceDescription: videoConfig.Description,
				}
				if existing, ok := byAlias[alias]; ok && existing.DefaultOption && !item.DefaultOption {
					continue
				}
				byAlias[alias] = item
			}
		}
	}
	result := make([]AistarsLabSeedanceModel, 0, len(byAlias))
	for _, item := range byAlias {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].PublicModel < result[j].PublicModel })
	return result
}

func buildAistarsLabSeedanceAlias(upstreamModel, quality, channel string) string {
	quality = strings.ToLower(strings.TrimSpace(quality))
	upstreamModel = strings.ToLower(strings.TrimSpace(upstreamModel))
	channel = strings.TrimSpace(channel)
	if quality == "" || upstreamModel == "" || channel == "" {
		return ""
	}
	version := "2.0"
	if match := regexp.MustCompile(`^seedance-([0-9]+\.[0-9]+)`).FindStringSubmatch(upstreamModel); len(match) == 2 {
		version = match[1]
	}
	suffix := strings.TrimPrefix(upstreamModel, "seedance-"+version)
	suffix = strings.Trim(suffix, "-")
	parts := make([]string, 0)
	for _, part := range strings.Split(suffix, "-") {
		part = strings.TrimSpace(part)
		if part != "" && part != quality {
			parts = append(parts, part)
		}
	}
	alias := []string{"seedance", quality}
	if version != "2.0" {
		alias = append(alias, "seedance-"+version)
	}
	alias = append(alias, parts...)
	alias = append(alias, "c"+channel)
	return strings.Join(alias, "-")
}

func normalizeAistarsLabBillingUnit(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "fixed_total":
		return "per_item"
	case "per_second":
		return "per_second"
	default:
		return ""
	}
}

func buildAistarsLabSyncResult(request AistarsLabSyncRequest, models []AistarsLabSeedanceModel, snapshot *model.ModelPricingSnapshot, channel *model.Channel) *AistarsLabSyncResult {
	result := &AistarsLabSyncResult{DryRun: request.DryRun, ChannelID: request.ChannelID, ConfigURL: request.ConfigURL,
		CreditRate: request.CreditRate, MarkupRate: request.MarkupRate, TotalModels: len(models),
		AddedModels: []string{}, RemovedModels: []string{}, ExpressionChanges: []AistarsLabExpressionChange{},
		PriceChanges: []AistarsLabPriceChange{}, MappingChanges: []AistarsLabMappingChange{}, Models: models}
	oldModes := make(map[string]string)
	oldExpressions := make(map[string]string)
	oldPrices := make(map[string]float64)
	for _, entry := range snapshot.Entries {
		if mode, ok := entry.Configured["billing_setting.billing_mode"].(string); ok {
			oldModes[entry.ModelName] = mode
		}
		if expression, ok := entry.Configured["billing_setting.billing_expr"].(string); ok {
			oldExpressions[entry.ModelName] = expression
		}
		if price, ok := entry.Configured["ModelPrice"].(float64); ok {
			oldPrices[entry.ModelName] = price
		}
		if variants, ok := entry.Configured[billing_setting.PluginBillingExprOption].(map[string]any); ok {
			if expression, ok := variants["aistarslab"].(string); ok {
				oldExpressions[entry.ModelName] = expression
			}
		}
	}
	oldMappings := parseAistarsLabStringMap(channel.GetModelMapping())
	oldModels := make(map[string]bool)
	for _, name := range channel.GetModels() {
		oldModels[name] = true
	}
	current := make(map[string]bool, len(models))
	for _, item := range models {
		current[item.PublicModel] = true
		oldExpression := oldExpressions[item.PublicModel]
		if !oldModels[item.PublicModel] {
			result.AddedModels = append(result.AddedModels, item.PublicModel)
		}
		if oldModes[item.PublicModel] != billing_setting.BillingModeTieredExpr {
			result.ModeChanges = append(result.ModeChanges, AistarsLabExpressionChange{Model: item.PublicModel, Old: oldModes[item.PublicModel], New: billing_setting.BillingModeTieredExpr})
		}
		if oldExpression != item.BillingExpression {
			result.ExpressionChanges = append(result.ExpressionChanges, AistarsLabExpressionChange{Model: item.PublicModel, Old: oldExpression, New: item.BillingExpression})
		}
		if oldPrice, exists := oldPrices[item.PublicModel]; !exists || math.Abs(oldPrice-item.Price) > 1e-9 {
			var old *float64
			if exists {
				value := oldPrice
				old = &value
			}
			value := item.Price
			result.PriceChanges = append(result.PriceChanges, AistarsLabPriceChange{Model: item.PublicModel, Old: old, New: &value})
		}
		if oldMappings[item.PublicModel] != item.UpstreamModel {
			result.MappingChanges = append(result.MappingChanges, AistarsLabMappingChange{Model: item.PublicModel, Old: oldMappings[item.PublicModel], New: item.UpstreamModel})
		}
	}
	removed := make(map[string]bool)
	markRemoved := func(name string) {
		if !removed[name] {
			removed[name] = true
			result.RemovedModels = append(result.RemovedModels, name)
		}
	}
	for name := range oldModels {
		if (isAistarsLabSeedanceAlias(name) || isAistarsLabRawModel(name)) && !current[name] {
			markRemoved(name)
		}
	}
	for name, oldMapping := range oldMappings {
		if oldModels[name] && (isAistarsLabSeedanceAlias(name) || isAistarsLabRawModel(name)) && !current[name] {
			markRemoved(name)
			result.MappingChanges = append(result.MappingChanges, AistarsLabMappingChange{Model: name, Old: oldMapping})
		}
	}
	sort.Strings(result.AddedModels)
	sort.Strings(result.RemovedModels)
	sort.Slice(result.ExpressionChanges, func(i, j int) bool { return result.ExpressionChanges[i].Model < result.ExpressionChanges[j].Model })
	sort.Slice(result.PriceChanges, func(i, j int) bool { return result.PriceChanges[i].Model < result.PriceChanges[j].Model })
	sort.Slice(result.MappingChanges, func(i, j int) bool { return result.MappingChanges[i].Model < result.MappingChanges[j].Model })
	return result
}

func filterOutAistarsLabModels(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || isAistarsLabSeedanceAlias(value) || isAistarsLabRawModel(value) || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func parseAistarsLabStringMap(raw string) map[string]string {
	result := make(map[string]string)
	if strings.TrimSpace(raw) != "" {
		_ = common.Unmarshal([]byte(raw), &result)
	}
	return result
}

func isAistarsLabSeedanceAlias(name string) bool {
	return aistarsLabAliasPattern.MatchString(strings.ToLower(strings.TrimSpace(name))) || regexp.MustCompile(`^aistarslab-[a-z0-9][a-z0-9.-]*-c[0-9]+$`).MatchString(name)
}

func isAistarsLabRawModel(name string) bool {
	return aistarsLabRawPattern.MatchString(strings.ToLower(strings.TrimSpace(name)))
}
