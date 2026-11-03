package service

import (
	"crypto/sha256"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/setting/billing_setting"
)

// Generate a normal, persisted task-plugin override. The host's existing atomic
// routing generations and restart loader then use exactly the same capabilities
// as the preview; there is no second frontend-only table of vendor limits.
func buildAistarsLabSyncedPlugin(models []AistarsLabSeedanceModel) (*model.TaskPlugin, *jsplugin.LoadedPlugin, error) {
	source, err := builtinplugins.Source("aistarslab")
	if err != nil {
		return nil, nil, err
	}
	type capability struct {
		Strict       bool     `json:"strict"`
		Resolution   string   `json:"resolution"`
		Modes        []string `json:"modes"`
		Ratios       []string `json:"ratios,omitempty"`
		MinSeconds   *int     `json:"minSeconds,omitempty"`
		MaxSeconds   *int     `json:"maxSeconds,omitempty"`
		SecondsSteps []int    `json:"secondsSteps,omitempty"`
		MaxImages    int      `json:"maxImages"`
		MaxVideos    int      `json:"maxVideos"`
		MaxAudios    int      `json:"maxAudios"`
	}
	data := make(map[string]capability, len(models))
	names := make([]string, 0, len(models))
	for _, item := range models {
		if item.Price < 0 || math.IsNaN(item.Price) || math.IsInf(item.Price, 0) {
			return nil, nil, fmt.Errorf("invalid upstream price for %s", item.PublicModel)
		}
		if item.PublicModel == "" || item.Quality == "" || item.InputImagesMax < 0 || item.InputVideosMax < 0 || item.InputAudiosMax < 0 {
			return nil, nil, fmt.Errorf("invalid capabilities for %s", item.PublicModel)
		}
		for _, mode := range item.Modes {
			if !slices.Contains([]string{"text2video", "image2video", "frames2video", "first-frame-to-video"}, mode) {
				return nil, nil, fmt.Errorf("unsupported upstream mode %q for %s", mode, item.PublicModel)
			}
		}
		if len(item.Modes) == 0 || len(item.AspectRatios) == 0 {
			return nil, nil, fmt.Errorf("upstream omitted modes or aspect ratios for %s", item.PublicModel)
		}
		minSec, maxSec := item.DurationMin, item.DurationMax
		steps := slices.Clone(item.DurationOptions)
		if (minSec == nil || maxSec == nil) && len(steps) == 0 {
			return nil, nil, fmt.Errorf("upstream omitted duration constraints for %s", item.PublicModel)
		}
		if len(steps) > 0 {
			for _, value := range steps {
				if (minSec != nil && value < *minSec) || (maxSec != nil && value > *maxSec) {
					return nil, nil, fmt.Errorf("upstream duration options conflict with range for %s", item.PublicModel)
				}
			}
			slices.Sort(steps)
			steps = slices.Compact(steps)
			minSec, maxSec = &steps[0], &steps[len(steps)-1]
		}
		for _, value := range []*int{minSec, maxSec} {
			if value != nil && (*value < 1 || *value > constant.MaxTaskDurationSeconds) {
				return nil, nil, fmt.Errorf("invalid upstream duration for %s", item.PublicModel)
			}
		}
		if minSec != nil && maxSec != nil && *minSec > *maxSec {
			return nil, nil, fmt.Errorf("invalid upstream duration range for %s", item.PublicModel)
		}
		modes := slices.Clone(item.Modes)
		resolution := item.Quality
		if resolution == "4k" || resolution == "2k" {
			resolution = strings.ToUpper(resolution)
		}
		data[item.PublicModel] = capability{true, resolution, modes, slices.Clone(item.AspectRatios), minSec, maxSec, steps, item.InputImagesMax, item.InputVideosMax, item.InputAudiosMax}
		names = append(names, item.PublicModel)
	}
	encoded, err := common.Marshal(data)
	if err != nil {
		return nil, nil, err
	}
	namesJSON, err := common.Marshal(names)
	if err != nil {
		return nil, nil, err
	}
	start := strings.Index(source, "const MODELS = [")
	end := strings.Index(source, "\n];")
	if start < 0 || end < start || strings.Count(source, "const SYNCED_MODELS = {};") != 1 {
		return nil, nil, fmt.Errorf("AistarsLab plugin template changed")
	}
	source = source[:start] + "const MODELS = " + string(namesJSON) + ";" + source[end+3:]
	source = strings.Replace(source, "const SYNCED_MODELS = {};", "const SYNCED_MODELS = "+string(encoded)+";", 1)
	source = "// Upstream config synchronized\n" + source
	digest := sha256.Sum256([]byte(source))
	version := "1.1.0+sync." + fmt.Sprintf("%x", digest[:12])
	source = strings.Replace(source, `version: "1.1.0"`, `version: "`+version+`"`, 1)
	loaded, err := jsplugin.NewRegistry().Register(source, jsplugin.Options{})
	if err != nil {
		return nil, nil, err
	}
	for _, item := range models {
		schema, _ := loaded.Meta.UsageForModel(item.PublicModel)
		if !billing_setting.TaskExprCompatible(item.BillingExpression, schema) {
			return nil, nil, fmt.Errorf("invalid upstream billing expression for %s", item.PublicModel)
		}
	}
	if err := jsplugin.PreflightRoutingConflict(jsplugin.DefaultRegistry.Generation(), loaded); err != nil {
		return nil, nil, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(source)))
	return &model.TaskPlugin{Key: "aistarslab", APIVersion: 1, Version: version, Source: model.LongText(source), SourceHash: hash, Enabled: true, Remark: "Upstream config synchronized capabilities"}, loaded, nil
}

func aistarsLabInputLimit(value *int) int {
	if value == nil {
		return -1
	}
	return *value
}

func aistarsLabCapabilitiesText(caps *jsplugin.VideoCapabilities) string {
	if caps == nil {
		return ""
	}
	data, _ := common.Marshal(caps)
	return string(data)
}

func aistarsLabPreviewToken(result *AistarsLabSyncResult, sourceHash, channelModels, channelMapping string, versions map[string]string) string {
	encoded, _ := common.Marshal([]any{result, sourceHash, channelModels, channelMapping, versions})
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

func applyAistarsLabCapabilitySync(request AistarsLabSyncRequest, result *AistarsLabSyncResult, channel *model.Channel, plugin *model.TaskPlugin, expectedHash string, versions map[string]string) error {
	names := filterOutAistarsLabModels(channel.GetModels())
	mapping := parseAistarsLabStringMap(channel.GetModelMapping())
	for name := range mapping {
		if slices.Contains(channel.GetModels(), name) && (isAistarsLabSeedanceAlias(name) || isAistarsLabRawModel(name)) {
			delete(mapping, name)
		}
	}
	prices := make(map[string]model.PricingValues, len(result.Models))
	for _, item := range result.Models {
		names = append(names, item.PublicModel)
		mapping[item.PublicModel] = item.UpstreamModel
		prices[item.PublicModel] = model.PricingValues{"billing_setting.billing_mode": "tiered_expr", "billing_setting.billing_expr": item.BillingExpression, "ModelPrice": item.Price}
	}
	slices.Sort(names)
	names = slices.Compact(names)
	encoded, err := common.Marshal(mapping)
	if err != nil {
		return err
	}
	if err := model.ApplyAistarsLabChannelSync(model.AistarsLabChannelSync{Channel: channel, ExpectedPluginHash: expectedHash, Models: names, Mapping: string(encoded), Plugin: plugin, Pricing: prices, ExpectedPricingVersions: versions, MarkupRate: strconv.FormatFloat(request.MarkupRate, 'f', -1, 64)}); err != nil {
		return err
	}
	// The database is authoritative. The controller's existing sync loop publishes
	// it immediately; background workers and other nodes use the same loader.
	return nil
}
