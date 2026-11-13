package service

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	builtinplugins "github.com/QuantumNous/new-api/plugins"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func yeepaySyncFixture(t *testing.T) (string, []yeepayCatalogModel) {
	t.Helper()
	source, err := builtinplugins.Source("yeepay")
	require.NoError(t, err)
	start := strings.Index(source, "const ROWS = ")
	end := strings.Index(source[start:], "\n];") + start
	require.Greater(t, end, start)
	source = source[:start] + `const ROWS = [["legacy",["720p"],5,10,[5,10],9,0,0,"request",false]];` + source[end+3:]
	source = strings.Replace(source, "  ],\n  protocols:", "  ].filter(function (profile) { return profile.models.length > 0; }),\n  protocols:", 1)
	var catalog []yeepayCatalogModel
	require.NoError(t, common.UnmarshalJsonStr(`[
 {"id":"new-video","capability_type":"video","capabilities":{"duration_options":[6,10,15,30],"resolution_options":["480p"],"aspect_ratio_options":["16:9","9:16","adaptive"],"min_duration_seconds":6,"max_duration_seconds":30,"max_image_refs":9,"max_audio_refs":1,"max_video_refs":3,"reference_materials":["image","audio","video"],"reference_support":{"image":true,"audio":true,"video":true},"video_reference_modes":["reference","edit","extend"]},"pricing":{"currency":"credits","mode":"per_generated_second","generated_second_price":2.2,"generated_second_price_with_video":3.3}},
 {"id":"new-request","capability_type":"video","capabilities":{"duration_options":[5,10],"resolution_options":["720p"],"aspect_ratio_options":[],"min_duration_seconds":5,"max_duration_seconds":10,"max_image_refs":99999,"max_audio_refs":0,"max_video_refs":99999,"reference_materials":["image"],"reference_support":{"image":true,"audio":false,"video":false}},"pricing":{"currency":"credits","mode":"per_request","base_price":30}}
 ]`, &catalog))
	return source, catalog
}

func TestYeepaySyncGeneratedContract(t *testing.T) {
	source, catalog := yeepaySyncFixture(t)
	request := YeepaySyncRequest{DryRun: true, ChannelID: 282, CreditRate: 10, MarkupRate: 1.35, DecimalPlaces: 1}
	candidate, loaded, models, warnings, err := buildYeepaySyncedPlugin(source, catalog, request)
	require.NoError(t, err)
	assert.Len(t, models, 2)
	assert.NotEmpty(t, warnings)
	caps := loaded.Meta.VideoCapabilities["new-request"]
	assert.Nil(t, caps.MaxImages)
	require.NotNil(t, caps.MaxVideos)
	assert.Zero(t, *caps.MaxVideos)
	assert.False(t, *caps.Frames)
	assert.Equal(t, []string{"16:9", "9:16"}, loaded.Meta.VideoCapabilities["new-video"].Ratios)
	assert.Contains(t, loaded.Meta.Models, "legacy")
	generatedAgain, _, _, _, err := buildYeepaySyncedPlugin(string(candidate.Source), catalog, request)
	require.NoError(t, err)
	assert.Equal(t, candidate.SourceHash, generatedAgain.SourceHash, "unchanged snapshots must not create new versions")
	body := map[string]any{"model": "new-video", "prompt": "waves", "seconds": 6, "aspect_ratio": "16:9", "resolution": "480p", "generate_audio": false}
	ctx := map[string]any{"baseUrl": "https://example.invalid", "apiKey": "fixture", "upstreamModel": "new-video", "requestBody": body}
	raw, err := loaded.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
	require.NoError(t, err)
	encoded, err := common.Marshal(raw)
	require.NoError(t, err)
	var descriptor struct {
		Body map[string]any `json:"body"`
	}
	require.NoError(t, common.Unmarshal(encoded, &descriptor))
	assert.Equal(t, false, descriptor.Body["generate_audio"])
	for _, patch := range []map[string]any{{"seconds": 8}, {"resolution": "1080p"}, {"aspect_ratio": "1:1"}, {"omni_reference_task_type": "extend"}, {"references": []any{map[string]any{"type": "image", "role": "first_frame", "url": "https://cdn.example/f.png"}}}} {
		original := map[string]any{}
		for k, v := range body {
			original[k] = v
		}
		for k, v := range patch {
			body[k] = v
		}
		_, err = loaded.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
		require.Error(t, err)
		for k := range body {
			delete(body, k)
		}
		for k, v := range original {
			body[k] = v
		}
	}
	price, err := yeepaySalePrice(common.GetPointer(2.2), request)
	require.NoError(t, err)
	assert.Equal(t, "0.3", price)
	for _, change := range []string{"tokens", "reference", "duration", "duplicate"} {
		cloned := slices.Clone(catalog)
		switch change {
		case "tokens":
			cloned[0].Pricing.Mode = "tokens"
		case "reference":
			cloned[0].Pricing.Reference = common.GetPointer(1.0)
		case "duration":
			cloned[0].Capabilities.MinSeconds = common.GetPointer(99)
		case "duplicate":
			cloned = append(cloned, cloned[0])
		}
		_, _, _, _, err = buildYeepaySyncedPlugin(source, cloned, request)
		require.Error(t, err, change)
	}
}

func TestYeepaySyncAtomicApplyDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := setupAistarsLabSyncDatabase(t, dialect)
			billing := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
			previousOverrides := billing.PluginBillingExpr
			t.Cleanup(func() { billing.PluginBillingExpr = previousOverrides })
			source, catalog := yeepaySyncFixture(t)
			setting := `{"task_plugin_key":"yeepay"}`
			mapping := `{"manual-alias":"legacy"}`
			channel := &model.Channel{Id: 282, Type: 61, Setting: &setting, Key: "fixture", Name: "Yeepay", Status: common.ChannelStatusEnabled, Group: "default", Models: "legacy,manual-alias", ModelMapping: &mapping}
			require.NoError(t, channel.Insert())
			old, err := jsplugin.NewRegistry().Register(source, jsplugin.Options{})
			require.NoError(t, err)
			current := &model.TaskPlugin{Key: "yeepay", APIVersion: 1, Version: old.Meta.Version, Source: model.LongText(source), SourceHash: fmt.Sprintf("%x", sha256.Sum256([]byte(source))), Active: true, Enabled: true}
			require.NoError(t, db.Create(current).Error)
			require.NoError(t, model.UpdateOptionsBulk(map[string]string{billing_setting.PluginBillingExprOption: `{"other::new-video":"tier(\"base\", u(\"seconds\") * 9)"}`, "AistarsLabMarkupRate": "1.7"}))
			request := YeepaySyncRequest{DryRun: true, ChannelID: 282, CreditRate: 10, MarkupRate: 1.35, DecimalPlaces: 1}
			preview, err := syncYeepaySnapshot(request, channel, current, catalog)
			require.NoError(t, err)
			assert.ElementsMatch(t, []string{"new-request", "new-video"}, preview.AddedModels)
			assert.Empty(t, billing_setting.GetPluginBillingExprCopy()["yeepay::new-video"])
			request.DryRun = false
			request.PreviewToken = preview.PreviewToken
			changed := slices.Clone(catalog)
			changed[0].Pricing.Seconds = common.GetPointer(99.0)
			_, err = syncYeepaySnapshot(request, channel, current, changed)
			require.ErrorContains(t, err, "preview again")
			// Inject a write failure and prove routing, plugin and pricing all roll back.
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("yeepay_fail_abilities", func(tx *gorm.DB) {
				if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "abilities" {
					tx.AddError(errors.New("fixture write failure"))
				}
			}))
			_, err = syncYeepaySnapshot(request, channel, current, catalog)
			require.Error(t, err)
			require.NoError(t, db.Callback().Create().Remove("yeepay_fail_abilities"))
			var after model.Channel
			require.NoError(t, db.First(&after, 282).Error)
			assert.Equal(t, channel.Models, after.Models)
			assert.Empty(t, billing_setting.GetPluginBillingExprCopy()["yeepay::new-video"])
			applied, err := syncYeepaySnapshot(request, channel, current, catalog)
			require.NoError(t, err)
			assert.False(t, applied.DryRun)
			require.NoError(t, db.First(&after, 282).Error)
			assert.ElementsMatch(t, []string{"legacy", "manual-alias", "new-video", "new-request"}, after.GetModels())
			assert.Equal(t, mapping, after.GetModelMapping())
			updatedPrices, err := model.GetModelPricingSnapshot([]string{"new-video"})
			require.NoError(t, err)
			assert.Contains(t, updatedPrices.Entries[0].Configured["billing_setting.billing_expr"], `* 0.3`)
			assert.Empty(t, billing_setting.GetPluginBillingExprCopy()["yeepay::new-video"])
			assert.Equal(t, `tier("base", u("seconds") * 9)`, billing_setting.GetPluginBillingExprCopy()["other::new-video"])
			var option model.Option
			require.NoError(t, db.Where(map[string]any{"key": "AistarsLabMarkupRate"}).First(&option).Error)
			assert.Equal(t, "1.7", option.Value)
			var activated model.TaskPlugin
			require.NoError(t, db.Where(&model.TaskPlugin{Key: "yeepay", Active: true}).First(&activated).Error)
			// Same preview cannot be applied after its incumbent plugin changed.
			_, err = syncYeepaySnapshot(request, &after, &activated, catalog)
			require.ErrorContains(t, err, "preview again")
			// Manual exclusion is preserved by default; all-models explicitly restores it.
			after.Models = "legacy,manual-alias,new-request"
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 282).Update("models", after.Models).Error)
			request.DryRun = true
			request.PreviewToken = ""
			again, err := syncYeepaySnapshot(request, &after, &activated, catalog)
			require.NoError(t, err)
			assert.Empty(t, again.AddedModels)
			request.AllModels = true
			all, err := syncYeepaySnapshot(request, &after, &activated, catalog)
			require.NoError(t, err)
			assert.Equal(t, []string{"new-video"}, all.AddedModels)
			assert.Empty(t, again.ExpressionChanges, "explicit existing prices must be retained by default")
			request.UpdatePrices = true
			foreign := model.Channel{Id: 283, Type: 1, Key: "fixture", Name: "other provider", Group: "default", Models: "new-video"}
			require.NoError(t, db.Create(&foreign).Error)
			_, err = syncYeepaySnapshot(request, &after, &activated, catalog)
			require.ErrorContains(t, err, "another provider")
			var backup model.Option
			require.NoError(t, db.Where(map[string]any{"key": "YeepaySyncBackup"}).First(&backup).Error)
			assert.Contains(t, backup.Value, `"models":"legacy,manual-alias"`)
			assert.NotContains(t, backup.Value, "fixture")
		})
	}
}

// Optional read-only captured provider fixture; never calls submit or stores keys.
func TestYeepayCapturedCatalogCandidate(t *testing.T) {
	path := os.Getenv("YEEPAY_SYNC_CAPTURE")
	if path == "" {
		t.Skip("set YEEPAY_SYNC_CAPTURE to a read-only captured directory")
	}
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var snapshot struct {
		Plugin struct {
			Source string `json:"source"`
		} `json:"plugin"`
		Upstream struct {
			Data []yeepayCatalogModel `json:"data"`
		} `json:"upstream"`
	}
	require.NoError(t, common.Unmarshal(raw, &snapshot))
	req := YeepaySyncRequest{CreditRate: 10, MarkupRate: 1.35, DecimalPlaces: 2}
	candidate, loaded, models, _, err := buildYeepaySyncedPlugin(snapshot.Plugin.Source, snapshot.Upstream.Data, req)
	require.NoError(t, err)
	for _, item := range models {
		c := loaded.Meta.VideoCapabilities[item.PublicModel]
		body := map[string]any{"model": item.PublicModel, "prompt": "waves", "seconds": *c.MinSeconds}
		if len(c.Resolutions) > 0 {
			body["resolution"] = c.Resolutions[0]
		}
		if len(c.Ratios) > 0 {
			body["aspect_ratio"] = c.Ratios[0]
		}
		ctx := map[string]any{"requestBody": body, "upstreamModel": item.PublicModel, "baseUrl": "https://www.yeepay.top", "apiKey": "fixture"}
		raw, err := loaded.Engine.Call(t.Context(), "buildSubmitRequest", ctx)
		require.NoError(t, err, item.PublicModel)
		encoded, err := common.Marshal(raw)
		require.NoError(t, err)
		var descriptor struct {
			Body struct {
				Resolution string `json:"resolution"`
			} `json:"body"`
		}
		require.NoError(t, common.Unmarshal(encoded, &descriptor))
		for _, catalogModel := range snapshot.Upstream.Data {
			if catalogModel.ID == item.PublicModel && len(catalogModel.Capabilities.Resolutions) > 0 {
				assert.Contains(t, catalogModel.Capabilities.Resolutions, descriptor.Body.Resolution, item.PublicModel)
			}
		}
		_, err = loaded.Engine.Call(t.Context(), "extractUsage", ctx)
		require.NoError(t, err, item.PublicModel)
	}
	again, _, _, _, err := buildYeepaySyncedPlugin(string(candidate.Source), snapshot.Upstream.Data, req)
	require.NoError(t, err)
	assert.Equal(t, candidate.SourceHash, again.SourceHash)
	t.Logf("%d captured models compiled and validated without generation", len(models))
}
