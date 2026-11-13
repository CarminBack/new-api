package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/shopspring/decimal"
)

// YeepaySyncRequest always requires explicit pricing inputs. No legacy price
// policy is silently inherited from another provider or an old deployment.
type YeepaySyncRequest struct {
	DryRun        bool    `json:"dry_run"`
	ChannelID     int     `json:"channel_id"`
	PreviewToken  string  `json:"preview_token"`
	CreditRate    float64 `json:"credit_rate"`
	MarkupRate    float64 `json:"markup_rate"`
	DecimalPlaces int     `json:"decimal_places"`
	AllModels     bool    `json:"all_models"`
	UpdatePrices  bool    `json:"update_prices"`
}

type yeepayCatalogModel struct {
	ID             string `json:"id"`
	CapabilityType string `json:"capability_type"`
	Capabilities   struct {
		DurationOptions []int           `json:"duration_options"`
		Resolutions     []string        `json:"resolution_options"`
		Ratios          []string        `json:"aspect_ratio_options"`
		MinSeconds      *int            `json:"min_duration_seconds"`
		MaxSeconds      *int            `json:"max_duration_seconds"`
		MaxImages       *int            `json:"max_image_refs"`
		MaxAudios       *int            `json:"max_audio_refs"`
		MaxVideos       *int            `json:"max_video_refs"`
		Materials       []string        `json:"reference_materials"`
		Support         map[string]bool `json:"reference_support"`
		VideoModes      []string        `json:"video_reference_modes"`
	} `json:"capabilities"`
	Pricing struct {
		Currency         string   `json:"currency"`
		Mode             string   `json:"mode"`
		Base             *float64 `json:"base_price"`
		Seconds          *float64 `json:"generated_second_price"`
		WithVideo        *float64 `json:"generated_second_price_with_video"`
		ReferenceBilling string   `json:"reference_video_billing"`
		Reference        *float64 `json:"reference_video_second_price"`
		Tokens           *float64 `json:"token_price_no_video_per_million"`
		VideoTokens      *float64 `json:"token_price_with_video_per_million"`
	} `json:"pricing"`
}

type YeepaySyncResult struct {
	AistarsLabSyncResult
	Settings YeepaySyncRequest `json:"settings"`
}

func fetchYeepayCatalog(ctx context.Context, channel *model.Channel) ([]yeepayCatalogModel, error) {
	root := strings.TrimSuffix(strings.TrimRight(channel.GetBaseURL(), "/"), "/v1")
	parsed, err := url.Parse(root)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() != "www.yeepay.top" || parsed.Port() != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return nil, errors.New("Yeepay sync requires https://www.yeepay.top as channel base URL")
	}
	key, _, keyErr := channel.GetNextEnabledKey()
	if keyErr != nil {
		return nil, errors.New("no enabled channel key")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, root+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	settings := channel.GetSetting()
	client, err := GetHttpClientWithProxySettings(settings.Proxy, settings)
	if err != nil {
		return nil, errors.New("channel HTTP client configuration is invalid")
	}
	// Do not forward the channel key to any redirect target, including another
	// provider host; the model directory is a single documented endpoint.
	scoped := *client
	scoped.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := scoped.Do(req)
	if err != nil {
		return nil, errors.New("Yeepay model directory request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Yeepay model directory returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (10<<20)+1))
	if err != nil || len(body) > 10<<20 {
		return nil, errors.New("invalid or oversized Yeepay model directory")
	}
	var envelope struct {
		Data []yeepayCatalogModel `json:"data"`
	}
	if err = common.Unmarshal(body, &envelope); err != nil || len(envelope.Data) == 0 {
		return nil, errors.New("Yeepay returned an empty or invalid model directory")
	}
	return envelope.Data, nil
}

func SyncYeepayConfig(ctx context.Context, request YeepaySyncRequest) (*YeepaySyncResult, error) {
	if request.ChannelID != 282 {
		return nil, errors.New("select Yeepay channel 282")
	}
	if request.CreditRate <= 0 || request.MarkupRate < 1 || math.IsNaN(request.CreditRate) || math.IsInf(request.CreditRate, 0) || math.IsNaN(request.MarkupRate) || math.IsInf(request.MarkupRate, 0) || request.DecimalPlaces < 0 || request.DecimalPlaces > 6 {
		return nil, errors.New("invalid exchange rate, markup or decimal precision")
	}
	channel, err := model.GetChannelById(request.ChannelID, true)
	if err != nil {
		return nil, err
	}
	if channel.Type != constant.ChannelTypeTaskPlugin || channel.GetSetting().TaskPluginKey != "yeepay" {
		return nil, errors.New("select a Yeepay task-plugin channel")
	}
	var current model.TaskPlugin
	if err = model.DB.Where(&model.TaskPlugin{Key: "yeepay", Active: true}).First(&current).Error; err != nil {
		return nil, errors.New("an active persisted Yeepay plugin is required")
	}
	if !current.Enabled {
		return nil, errors.New("enable the Yeepay plugin before synchronization")
	}
	catalog, err := fetchYeepayCatalog(ctx, channel)
	if err != nil {
		return nil, err
	}
	return syncYeepaySnapshot(request, channel, &current, catalog)
}

// syncYeepaySnapshot shares the existing atomic price/channel/plugin transaction.
// Both preview and apply compute the same candidate afresh and reject drift.
func syncYeepaySnapshot(request YeepaySyncRequest, channel *model.Channel, current *model.TaskPlugin, catalog []yeepayCatalogModel) (*YeepaySyncResult, error) {
	incumbent, err := jsplugin.NewRegistry().Register(string(current.Source), jsplugin.Options{})
	if err != nil {
		return nil, errors.New("current Yeepay plugin cannot be compiled")
	}
	plugin, loaded, models, warnings, err := buildYeepaySyncedPlugin(string(current.Source), catalog, request)
	if err != nil {
		return nil, err
	}
	names := slices.Clone(channel.GetModels())
	mapping := map[string]string{}
	if strings.TrimSpace(channel.GetModelMapping()) != "" {
		if err = common.UnmarshalJsonStr(channel.GetModelMapping(), &mapping); err != nil {
			return nil, errors.New("channel mapping is invalid")
		}
	}
	// Add-new respects intentional exclusions; all-models is an explicit opt-in.
	for _, item := range models {
		if request.AllModels || !slices.Contains(incumbent.Meta.Models, item.PublicModel) {
			names = append(names, item.PublicModel)
		}
	}
	slices.Sort(names)
	names = slices.Compact(names)
	snapshot, err := model.GetModelPricingSnapshot(append(slices.Clone(names), loaded.Meta.Models...))
	if err != nil {
		return nil, err
	}
	versions := map[string]string{}
	entries := map[string]model.ModelPricingEntry{}
	for _, e := range snapshot.Entries {
		versions[e.ModelName] = e.Version
		entries[e.ModelName] = e
	}
	result := &YeepaySyncResult{AistarsLabSyncResult: AistarsLabSyncResult{DryRun: true, ChannelID: channel.Id, CreditRate: request.CreditRate, MarkupRate: request.MarkupRate, TotalModels: len(names), PluginVersion: plugin.Version, PreviousPluginVersion: current.Version, Warnings: warnings, Models: models}, Settings: request}
	result.Settings.DryRun = true
	result.Settings.PreviewToken = ""
	result.AddedModels = []string{}
	result.RemovedModels = []string{}
	result.MappingChanges = []AistarsLabMappingChange{}
	for _, n := range names {
		if !slices.Contains(channel.GetModels(), n) {
			result.AddedModels = append(result.AddedModels, n)
		}
	}
	prices := map[string]model.PricingValues{}
	for _, item := range models {
		n := item.PublicModel
		if !slices.Contains(names, n) {
			continue
		}
		// A mapping alias inherits the canonical target's price; never replace
		// administrator aliases or manufacture provider route IDs.
		if target := mapping[n]; target != "" && target != n {
			result.Warnings = append(result.Warnings, n+": mapping preserved; mapped pricing needs manual review")
			continue
		}
		oldExpr := ""
		if variants, ok := entries[n].Configured[billing_setting.PluginBillingExprOption].(map[string]any); ok {
			oldExpr, _ = variants["yeepay"].(string)
		}
		if oldExpr == "" {
			oldExpr, _ = entries[n].Configured["billing_setting.billing_expr"].(string)
		}
		hasExisting := oldExpr != "" || entries[n].Configured["ModelPrice"] != nil || entries[n].Configured["ModelRatio"] != nil
		if request.UpdatePrices || !hasExisting {
			// Model-level pricing remains authoritative; preserve other provider variants.
			variants := map[string]any{}
			if value, ok := entries[n].Configured[billing_setting.PluginBillingExprOption].(map[string]any); ok {
				for k, v := range value {
					variants[k] = v
				}
			}
			delete(variants, "yeepay")
			prices[n] = model.PricingValues{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": item.BillingExpression, "ModelPrice": item.Price, billing_setting.PluginBillingExprOption: variants}
			var oldPrice *float64
			if value, ok := entries[n].Configured["ModelPrice"].(float64); ok {
				oldPrice = &value
			}
			result.PriceChanges = append(result.PriceChanges, AistarsLabPriceChange{Model: n, Old: oldPrice, New: &item.Price})
			if oldExpr != item.BillingExpression {
				result.ExpressionChanges = append(result.ExpressionChanges, AistarsLabExpressionChange{Model: n, Old: oldExpr, New: item.BillingExpression})
			}
		} else if oldExpr != "" {
			schema, _ := loaded.Meta.UsageForModel(n)
			if err = billing_setting.SmokeTestTaskExpr(oldExpr, schema); err != nil {
				return nil, fmt.Errorf("%s: existing pricing is incompatible with refreshed capabilities; enable price updates or correct pricing", n)
			}
		}
	}
	for _, item := range models {
		before := incumbent.Meta.VideoCapabilities[item.PublicModel]
		after := loaded.Meta.VideoCapabilities[item.PublicModel]
		oldText, newText := aistarsLabCapabilitiesText(&before), aistarsLabCapabilitiesText(&after)
		if oldText != newText {
			result.CapabilityChanges = append(result.CapabilityChanges, AistarsLabExpressionChange{Model: item.PublicModel, Old: oldText, New: newText})
		}
	}
	available := make(map[string]bool, len(models))
	for _, item := range models {
		available[item.PublicModel] = true
	}
	for _, name := range channel.GetModels() {
		if !available[name] && mapping[name] == "" {
			result.Warnings = append(result.Warnings, name+": no longer listed upstream; retained for manual review")
		}
	}
	priceNames := make([]string, 0, len(prices))
	for name := range prices {
		priceNames = append(priceNames, name)
	}
	if err := model.ValidateYeepayPricingScope(model.DB, priceNames); err != nil {
		return nil, err
	}
	result.Warnings = append(result.Warnings, "Existing channel choices, mappings and retired models are preserved. Model-level prices are used; cross-provider name conflicts are rejected. Review all changes before applying.")
	encoded, err := common.Marshal([]any{result, current.SourceHash, channel.Models, channel.GetModelMapping(), channel.Setting, channel.OtherSettings, channel.BaseURL, channel.Key, channel.Status, channel.Group, versions, catalog})
	if err != nil {
		return nil, err
	}
	// channel contains credentials only in memory; only the digest leaves the server.
	result.PreviewToken = fmt.Sprintf("%x", sha256.Sum256(encoded))
	if request.DryRun {
		return result, nil
	}
	if request.PreviewToken == "" || request.PreviewToken != result.PreviewToken {
		return nil, errors.New("upstream or local configuration changed; preview again before applying")
	}
	settings := request
	settings.DryRun = true
	settings.PreviewToken = ""
	saved, _ := common.Marshal(settings)
	if err = model.ApplyAistarsLabChannelSync(model.AistarsLabChannelSync{Channel: channel, ExpectedPluginHash: current.SourceHash, Models: names, Mapping: channel.GetModelMapping(), Plugin: plugin, Pricing: prices, ExpectedPricingVersions: versions, MarkupRate: string(saved), SettingsOptionKey: "YeepaySyncSettings"}); err != nil {
		return nil, err
	}
	result.DryRun = false
	model.RefreshPricing()
	return result, nil
}

var yeepayRatioPattern = regexp.MustCompile(`^[1-9][0-9]*:[1-9][0-9]*$`)

func yeepaySalePrice(value *float64, request YeepaySyncRequest) (string, error) {
	if value == nil || *value < 0 || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return "", errors.New("missing or invalid upstream price")
	}
	sale := decimal.NewFromFloat(*value).Div(decimal.NewFromFloat(request.CreditRate)).Mul(decimal.NewFromFloat(request.MarkupRate)).RoundCeil(int32(request.DecimalPlaces))
	if sale.GreaterThan(decimal.NewFromInt(math.MaxInt32)) {
		return "", errors.New("unit price exceeds supported bound")
	}
	return sale.String(), nil
}
