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

	"github.com/QuantumNous/new-api/common"
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
	DryRun     bool    `json:"dry_run"`
	ChannelID  int     `json:"channel_id"`
	ConfigURL  string  `json:"config_url"`
	CreditRate float64 `json:"credit_rate"`
	MarkupRate float64 `json:"markup_rate"`
}

type AistarsLabSyncResult struct {
	DryRun            bool                         `json:"dry_run"`
	ChannelID         int                          `json:"channel_id"`
	ConfigURL         string                       `json:"config_url"`
	CreditRate        float64                      `json:"credit_rate"`
	MarkupRate        float64                      `json:"markup_rate"`
	TotalModels       int                          `json:"total_models"`
	AddedModels       []string                     `json:"added_models"`
	RemovedModels     []string                     `json:"removed_models"`
	ExpressionChanges []AistarsLabExpressionChange `json:"expression_changes"`
	MappingChanges    []AistarsLabMappingChange    `json:"mapping_changes"`
	Models            []AistarsLabSeedanceModel    `json:"models"`
}

type AistarsLabExpressionChange struct {
	Model string `json:"model"`
	Old   string `json:"old,omitempty"`
	New   string `json:"new,omitempty"`
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
	InputImagesMax int                 `json:"inputImagesMax"`
	InputVideosMax int                 `json:"inputVideosMax"`
	InputAudiosMax int                 `json:"inputAudiosMax"`
}

type aistarsLabQuality struct {
	Quality string `json:"quality"`
	Pricing struct {
		Type    string  `json:"type"`
		Credits float64 `json:"credits"`
	} `json:"pricing"`
}

type aistarsLabDuration struct {
	Min *int `json:"min"`
	Max *int `json:"max"`
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
	result, err := SyncAistarsLabConfig(context.Background(), AistarsLabSyncRequest{})
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
	models := flattenAistarsLabSeedanceModels(config.Data.VideoConfig, request.CreditRate, request.MarkupRate)
	if len(models) == 0 {
		return nil, fmt.Errorf("no Seedance models found in AistarsLab config")
	}
	result := buildAistarsLabSyncResult(request, models)
	if request.DryRun {
		return result, nil
	}
	if err := applyAistarsLabSync(request.ChannelID, models); err != nil {
		return nil, err
	}
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
			if !strings.HasPrefix(strings.ToLower(configModel.Model), "seedance-") {
				continue
			}
			for _, quality := range configModel.Qualities {
				unit := normalizeAistarsLabBillingUnit(quality.Pricing.Type)
				alias := buildAistarsLabSeedanceAlias(configModel.Model, quality.Quality, channel)
				if unit == "" || alias == "" {
					continue
				}
				price := math.Round((quality.Pricing.Credits/creditRate*markupRate)*100) / 100
				fact := "seconds"
				if unit == "per_item" {
					fact = "videos"
				}
				item := AistarsLabSeedanceModel{
					PublicModel: alias, UpstreamModel: channel + ":" + configModel.Model, Channel: channel,
					Quality: strings.ToLower(strings.TrimSpace(quality.Quality)), BillingUnit: unit, Price: price,
					BillingExpression: fmt.Sprintf(`tier("base", u("%s") * %s)`, fact, strconv.FormatFloat(price, 'f', -1, 64)),
					RawCredits:        quality.Pricing.Credits, Modes: append([]string(nil), configModel.Modes...),
					AspectRatios: append([]string(nil), configModel.AspectRatios...), DurationMin: configModel.Duration.Min,
					DurationMax: configModel.Duration.Max, InputImagesMax: configModel.InputImagesMax,
					InputVideosMax: configModel.InputVideosMax, InputAudiosMax: configModel.InputAudiosMax,
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

func buildAistarsLabSyncResult(request AistarsLabSyncRequest, models []AistarsLabSeedanceModel) *AistarsLabSyncResult {
	result := &AistarsLabSyncResult{DryRun: request.DryRun, ChannelID: request.ChannelID, ConfigURL: request.ConfigURL,
		CreditRate: request.CreditRate, MarkupRate: request.MarkupRate, TotalModels: len(models), Models: models}
	oldExpressions := billing_setting.GetConfiguredBillingExprCopy()
	oldMappings := getAistarsLabChannelMapping(request.ChannelID)
	current := make(map[string]bool, len(models))
	for _, item := range models {
		current[item.PublicModel] = true
		oldExpression, exists := oldExpressions[item.PublicModel]
		if !exists {
			result.AddedModels = append(result.AddedModels, item.PublicModel)
		}
		if oldExpression != item.BillingExpression {
			result.ExpressionChanges = append(result.ExpressionChanges, AistarsLabExpressionChange{Model: item.PublicModel, Old: oldExpression, New: item.BillingExpression})
		}
		if oldMappings[item.PublicModel] != item.UpstreamModel {
			result.MappingChanges = append(result.MappingChanges, AistarsLabMappingChange{Model: item.PublicModel, Old: oldMappings[item.PublicModel], New: item.UpstreamModel})
		}
	}
	for name := range oldExpressions {
		if isAistarsLabSeedanceAlias(name) && !current[name] {
			result.RemovedModels = append(result.RemovedModels, name)
			result.ExpressionChanges = append(result.ExpressionChanges, AistarsLabExpressionChange{Model: name, Old: oldExpressions[name]})
		}
	}
	sort.Strings(result.AddedModels)
	sort.Strings(result.RemovedModels)
	sort.Slice(result.ExpressionChanges, func(i, j int) bool { return result.ExpressionChanges[i].Model < result.ExpressionChanges[j].Model })
	sort.Slice(result.MappingChanges, func(i, j int) bool { return result.MappingChanges[i].Model < result.MappingChanges[j].Model })
	return result
}

func applyAistarsLabSync(channelID int, modelsToSync []AistarsLabSeedanceModel) error {
	modes := billing_setting.GetConfiguredBillingModeCopy()
	expressions := billing_setting.GetConfiguredBillingExprCopy()
	active := make(map[string]bool, len(modelsToSync))
	for _, item := range modelsToSync {
		active[item.PublicModel] = true
		modes[item.PublicModel] = billing_setting.BillingModeTieredExpr
		expressions[item.PublicModel] = item.BillingExpression
	}
	for name := range modes {
		if isAistarsLabSeedanceAlias(name) && !active[name] {
			delete(modes, name)
		}
	}
	for name := range expressions {
		if isAistarsLabSeedanceAlias(name) && !active[name] {
			delete(expressions, name)
		}
	}
	modeJSON, err := common.Marshal(modes)
	if err != nil {
		return err
	}
	expressionJSON, err := common.Marshal(expressions)
	if err != nil {
		return err
	}
	if err := model.UpdateOptionsBulk(map[string]string{
		"billing_setting.billing_mode": string(modeJSON),
		"billing_setting.billing_expr": string(expressionJSON),
	}); err != nil {
		return err
	}
	return updateAistarsLabChannelModels(channelID, modelsToSync)
}

func updateAistarsLabChannelModels(channelID int, modelsToSync []AistarsLabSeedanceModel) error {
	channel, err := model.GetChannelById(channelID, true)
	if err != nil {
		return err
	}
	models := filterOutAistarsLabModels(channel.GetModels())
	mapping := parseAistarsLabStringMap(channel.GetModelMapping())
	for key := range mapping {
		if isAistarsLabSeedanceAlias(key) || isAistarsLabRawModel(key) {
			delete(mapping, key)
		}
	}
	for _, item := range modelsToSync {
		models = append(models, item.PublicModel)
		mapping[item.PublicModel] = item.UpstreamModel
	}
	sort.Strings(models)
	mappingJSON, err := common.Marshal(mapping)
	if err != nil {
		return err
	}
	mappingValue := string(mappingJSON)
	channel.Models = strings.Join(models, ",")
	channel.ModelMapping = &mappingValue
	return channel.Update()
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

func getAistarsLabChannelMapping(channelID int) map[string]string {
	channel, err := model.GetChannelById(channelID, true)
	if err != nil {
		return map[string]string{}
	}
	return parseAistarsLabStringMap(channel.GetModelMapping())
}

func parseAistarsLabStringMap(raw string) map[string]string {
	result := make(map[string]string)
	if strings.TrimSpace(raw) != "" {
		_ = common.Unmarshal([]byte(raw), &result)
	}
	return result
}

func isAistarsLabSeedanceAlias(name string) bool {
	return aistarsLabAliasPattern.MatchString(strings.ToLower(strings.TrimSpace(name)))
}

func isAistarsLabRawModel(name string) bool {
	return aistarsLabRawPattern.MatchString(strings.ToLower(strings.TrimSpace(name)))
}
