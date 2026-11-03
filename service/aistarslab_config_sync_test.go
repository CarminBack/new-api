package service

import (
	"context"
	"errors"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlattenAistarsLabSeedanceModelsBuildsBillingExpressions(t *testing.T) {
	minDuration, maxDuration := 4, 15
	configs := []aistarsLabVideoConfig{
		{
			Channel: "47", DefaultOption: true, Title: "primary",
			Models: []aistarsLabConfigModel{
				{
					Model: "seedance-2.0", Modes: []string{"text2video", "image2video"},
					AspectRatios: []string{"16:9", "9:16"}, Duration: aistarsLabDuration{Min: &minDuration, Max: &maxDuration},
					InputImagesMax: common.GetPointer(9), InputVideosMax: common.GetPointer(3), InputAudiosMax: common.GetPointer(3),
					Qualities: []aistarsLabQuality{
						newAistarsLabQuality("720p", "per_second", 54),
						newAistarsLabQuality("4K", "fixed_total", 270),
					},
				},
			},
		},
	}

	models := flattenAistarsLabSeedanceModels(configs, 100, 1.3)
	require.Len(t, models, 2)
	byName := make(map[string]AistarsLabSeedanceModel)
	for _, item := range models {
		byName[item.PublicModel] = item
	}
	perSecond := byName["seedance-720p-c47"]
	assert.Equal(t, "per_second", perSecond.BillingUnit)
	assert.Equal(t, 0.7, perSecond.Price)
	assert.Equal(t, `tier("base", u("seconds") * 0.7 * (u("video_input") == "video" ? 1.5 : 1))`, perSecond.BillingExpression)
	assert.Equal(t, "47:seedance-2.0", perSecond.UpstreamModel)
	assert.Equal(t, 9, perSecond.InputImagesMax)

	perItem := byName["seedance-4k-c47"]
	assert.Equal(t, "per_item", perItem.BillingUnit)
	assert.Equal(t, 3.51, perItem.Price)
	require.NoError(t, model.ValidateModelPricing("seedance-480p-c50", model.PricingValues{
		"billing_setting.billing_mode": "tiered_expr",
		"billing_setting.billing_expr": perSecond.BillingExpression,
	}), "an enabled AistarsLab alias must resolve its usage schema when saving pricing")
	assert.Equal(t, `tier("base", u("videos") * 3.51 * (u("video_input") == "video" ? 1.5 : 1))`, perItem.BillingExpression)
	for _, item := range []AistarsLabSeedanceModel{perSecond, perItem} {
		for _, input := range []string{"none", "video"} {
			t.Run(item.BillingUnit+"/"+input, func(t *testing.T) {
				usage := map[string]any{"seconds": 5.0, "videos": 1.0, "video_input": input}
				want := item.Price
				if item.BillingUnit == "per_second" {
					want *= 5
				}
				if input == "video" {
					want *= 1.5
				}
				cost, _, err := billingexpr.RunExprWithRequest(item.BillingExpression, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: usage})
				require.NoError(t, err)
				assert.InDelta(t, want, cost, 1e-9)
				result, err := billingexpr.ComputeTieredQuotaWithRequest(&billingexpr.BillingSnapshot{
					ExprString: item.BillingExpression, ExprHash: billingexpr.ExprHashString(item.BillingExpression),
					GroupRatio: 1, QuotaPerUnit: 500000, ExprVersion: 1, TaskUsageBilling: true,
				}, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: usage})
				require.NoError(t, err)
				assert.Equal(t, common.QuotaRound(want*500000), result.ActualQuotaAfterGroup)
			})
		}
	}
}

func TestBuildAistarsLabSeedanceAlias(t *testing.T) {
	assert.Equal(t, "seedance-720p-fast-c12", buildAistarsLabSeedanceAlias("seedance-2.0-720p-fast", "720p", "12"))
	assert.Equal(t, "seedance-1080p-c30", buildAistarsLabSeedanceAlias("seedance-2.0", "1080p", "30"))
	assert.Equal(t, "seedance-720p-fast-4img-c18", buildAistarsLabSeedanceAlias("seedance-2.0-720p-fast-4img", "720p", "18"))
	assert.Equal(t, "seedance-1080p-seedance-2.5-c54", buildAistarsLabSeedanceAlias("seedance-2.5", "1080p", "54"))
}

func TestFlattenAistarsLabSeedanceModelsPrefersDefaultOption(t *testing.T) {
	secondary := aistarsLabVideoConfig{Channel: "47", DefaultOption: false, Models: []aistarsLabConfigModel{{
		Model: "seedance-2.0", Qualities: []aistarsLabQuality{newAistarsLabQuality("720p", "per_second", 100)},
	}}}
	primary := aistarsLabVideoConfig{Channel: "47", DefaultOption: true, Models: []aistarsLabConfigModel{{
		Model: "seedance-2.0", Qualities: []aistarsLabQuality{newAistarsLabQuality("720p", "per_second", 50)},
	}}}

	models := flattenAistarsLabSeedanceModels([]aistarsLabVideoConfig{primary, secondary}, 100, 1)
	require.Len(t, models, 1)
	assert.Equal(t, 0.5, models[0].Price)
	assert.True(t, models[0].DefaultOption)
}

func TestNormalizeAistarsLabSyncRequestUsesDefaults(t *testing.T) {
	t.Setenv("AISTARSLAB_CONFIG_SYNC_CHANNEL_ID", "23")
	t.Setenv("AISTARSLAB_CONFIG_URL", "https://config.example.test/models")
	t.Setenv("AISTARSLAB_CREDIT_RATE", "200")
	t.Setenv("AISTARSLAB_MARKUP_RATE", "1.5")

	request := normalizeAistarsLabSyncRequest(AistarsLabSyncRequest{})
	assert.Equal(t, 23, request.ChannelID)
	assert.Equal(t, "https://config.example.test/models", request.ConfigURL)
	assert.Equal(t, 200.0, request.CreditRate)
	assert.Equal(t, 1.5, request.MarkupRate)
}

func TestFilterOutAistarsLabModels(t *testing.T) {
	filtered := filterOutAistarsLabModels([]string{
		"other-model", "seedance-720p-c47", "47:seedance-2.0", "other-model", "",
	})
	assert.Equal(t, []string{"other-model"}, filtered)
}

func TestSyncAistarsLabConfigDryRun(t *testing.T) {
	db := setupAistarsLabSyncDatabase(t, "sqlite")
	setting := `{"task_plugin_key":"aistarslab"}`
	channel := &model.Channel{Id: 99999, Type: 61, Key: "fixture", Name: "sync-preview", Status: common.ChannelStatusEnabled, Group: "default", Setting: &setting}
	require.NoError(t, db.Create(channel).Error)
	t.Setenv("AISTARSLAB_API_KEY", "test-key")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "Bearer test-key", request.Header.Get("Authorization"))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"code":0,"data":{"videoConfig":[{"channel":"47","defaultOption":true,"models":[{"model":"seedance-2.0","modes":["text2video","image2video"],"aspectRatios":["16:9","9:16"],"duration":{"min":4,"max":15},"inputImagesMax":9,"inputVideosMax":3,"inputAudiosMax":3,"qualities":[{"quality":"720p","pricing":{"type":"per_second","credits":54,"inputVideoMultiplier":1.5}}]}]}]}}`))
	}))
	defer server.Close()

	result, err := SyncAistarsLabConfig(context.Background(), AistarsLabSyncRequest{
		DryRun: true, ChannelID: 99999, ConfigURL: server.URL, CreditRate: 100, MarkupRate: 1.3,
	})
	require.NoError(t, err)
	require.Len(t, result.Models, 1)
	assert.True(t, result.DryRun)
	assert.Equal(t, "seedance-720p-c47", result.Models[0].PublicModel)
	assert.Equal(t, `tier("base", u("seconds") * 0.7 * (u("video_input") == "video" ? 1.5 : 1))`, result.Models[0].BillingExpression)
	require.Len(t, result.PriceChanges, 1)
	assert.Equal(t, "seedance-720p-c47", result.PriceChanges[0].Model)
	require.NotNil(t, result.PriceChanges[0].New)
	assert.InDelta(t, 0.7, *result.PriceChanges[0].New, 1e-9)
	require.Len(t, result.ExpressionChanges, 1)
	assert.Equal(t, `tier("base", u("seconds") * 0.7 * (u("video_input") == "video" ? 1.5 : 1))`, result.ExpressionChanges[0].New)
	require.Len(t, result.MappingChanges, 1)
	assert.Equal(t, "47:seedance-2.0", result.MappingChanges[0].New)
	var pluginCount int64
	require.NoError(t, db.Model(&model.TaskPlugin{}).Count(&pluginCount).Error)
	assert.Zero(t, pluginCount, "preview must not persist an override")
	apply := AistarsLabSyncRequest{ChannelID: 99999, ConfigURL: server.URL, CreditRate: 100, MarkupRate: 1.3}
	_, err = SyncAistarsLabConfig(t.Context(), apply)
	require.ErrorContains(t, err, "preview again")
	apply.PreviewToken = result.PreviewToken
	apply.MarkupRate = 1.5
	_, err = SyncAistarsLabConfig(t.Context(), apply)
	require.ErrorContains(t, err, "preview again")
	apply.MarkupRate = 1.3
	require.NoError(t, db.Model(channel).Update("models", "manual-model").Error)
	_, err = SyncAistarsLabConfig(t.Context(), apply)
	require.ErrorContains(t, err, "preview again")
	require.NoError(t, db.First(channel, 99999).Error)
	assert.Equal(t, "manual-model", channel.Models)
}

func TestAistarsLabApplyPersistsEffectivePricing(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := setupAistarsLabSyncDatabase(t, dialect)
			billing := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
			initial := map[string]string{
				"billing_setting.billing_mode": `{"seedance-720p-c47":"tiered_expr","seedance-720p-c53":"tiered_expr","other-model":"tiered_expr"}`,
				"billing_setting.billing_expr": `{"seedance-720p-c47":"tier(\"base\", u(\"seconds\") * 0.2)","seedance-720p-c53":"tier(\"base\", u(\"seconds\") * 0.62)","other-model":"tier(\"base\", u(\"seconds\") * 9.99)"}`,
				"ModelPrice":                   `{"seedance-720p-c47":0.2,"seedance-720p-c53":0.62,"other-model":9.99}`,
				"AistarsLabMarkupRate":         "1.3",
			}
			require.NoError(t, model.UpdateOptionsBulk(initial))
			mapping := `{"other-model":"upstream-other","seedance-720p-c47":"old:seedance-2.0","seedance-720p-c53":"53:seedance-2.0"}`
			setting := `{"task_plugin_key":"aistarslab"}`
			channel := &model.Channel{Id: 17, Type: 61, Setting: &setting, Key: "fixture", Name: "sync-fixture", Status: common.ChannelStatusEnabled,
				Group: "default", Models: "other-model,seedance-720p-c47,seedance-720p-c53", ModelMapping: &mapping}
			require.NoError(t, channel.Insert())
			configs := []aistarsLabVideoConfig{
				{Channel: "47", Models: []aistarsLabConfigModel{{Model: "seedance-2.0", Qualities: []aistarsLabQuality{newAistarsLabQuality("720p", "per_second", 54)}}}},
				{Channel: "50", Models: []aistarsLabConfigModel{{Model: "seedance-2.0", Qualities: []aistarsLabQuality{newAistarsLabQuality("720p", "fixed_total", 450)}}}},
			}
			for i := range configs {
				for j := range configs[i].Models {
					item := &configs[i].Models[j]
					item.Modes = []string{"text2video", "image2video"}
					item.AspectRatios = []string{"16:9", "9:16"}
					item.Duration = aistarsLabDuration{Min: common.GetPointer(4), Max: common.GetPointer(15)}
					item.InputImagesMax = common.GetPointer(9)
					item.InputVideosMax = common.GetPointer(3)
					item.InputAudiosMax = common.GetPointer(3)
				}
			}
			t.Setenv("AISTARSLAB_API_KEY", "fixture")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				var response aistarsLabConfigResponse
				response.Data.VideoConfig = configs
				encoded, err := common.Marshal(response)
				require.NoError(t, err)
				_, _ = w.Write(encoded)
			}))
			defer server.Close()
			for index, markup := range []float64{1.35, 1.5} {
				if index == 1 {
					// An upgraded installation may retain the ignored keys written
					// by the old synchronizer. They must not override effective pricing.
					require.NoError(t, model.UpdateOptionsBulk(map[string]string{
						"billing_expr": initial["billing_setting.billing_expr"],
						"billing_mode": initial["billing_setting.billing_mode"],
					}))
				}
				models := flattenAistarsLabSeedanceModels(configs, 100, markup)
				request := AistarsLabSyncRequest{DryRun: true, ChannelID: 17, ConfigURL: server.URL, CreditRate: 100, MarkupRate: markup}
				preview, err := SyncAistarsLabConfig(context.Background(), request)
				require.NoError(t, err)
				request.DryRun = false
				request.PreviewToken = preview.PreviewToken
				if index == 0 {
					configs[0].Models[0].Duration.Min = common.GetPointer(5)
					_, err = SyncAistarsLabConfig(t.Context(), request)
					require.ErrorContains(t, err, "preview again")
					configs[0].Models[0].Duration.Min = common.GetPointer(4)
					require.NoError(t, db.Callback().Create().Before("gorm:create").Register("aistars_fail_abilities", func(tx *gorm.DB) {
						if tx.Statement.Table == "abilities" {
							tx.AddError(errors.New("injected ability write failure"))
						}
					}))
					_, err = SyncAistarsLabConfig(context.Background(), request)
					require.ErrorContains(t, err, "injected ability write failure")
					require.NoError(t, db.Callback().Create().Remove("aistars_fail_abilities"))
					var untouched model.Channel
					require.NoError(t, db.First(&untouched, 17).Error)
					assert.Equal(t, channel.Models, untouched.Models)
					var plugins int64
					require.NoError(t, db.Model(&model.TaskPlugin{}).Count(&plugins).Error)
					assert.Zero(t, plugins)
					assert.Equal(t, 0.2, ratio_setting.GetModelPriceCopy()["seedance-720p-c47"], "failed snapshot must not publish pricing caches")
					var storedPrice model.Option
					require.NoError(t, db.Where(&model.Option{Key: "ModelPrice"}).First(&storedPrice).Error)
					assert.Contains(t, storedPrice.Value, `"seedance-720p-c47":0.2`)
				}
				_, err = SyncAistarsLabConfig(context.Background(), request)
				require.NoError(t, err)
				for _, item := range models {
					assert.Equal(t, billing_setting.BillingModeTieredExpr, billing_setting.GetBillingMode(item.PublicModel))
					expression, configured := billing_setting.GetBillingExpr(item.PublicModel)
					require.True(t, configured)
					assert.Equal(t, item.BillingExpression, expression, "actual billing must change with the profit rate")
					assert.Equal(t, item.Price, ratio_setting.GetModelPriceCopy()[item.PublicModel])
				}
				// Reload only the persisted pricing fields through the same config
				// loader used on startup, without resetting unrelated test settings.
				var rows []model.Option
				require.NoError(t, db.Find(&rows).Error)
				stored := make(map[string]string)
				for _, row := range rows {
					stored[row.Key] = row.Value
				}
				billing.BillingMode, billing.BillingExpr = make(map[string]string), make(map[string]string)
				config.UpdateConfigFromMap(billing, map[string]string{
					"billing_mode": stored["billing_setting.billing_mode"],
					"billing_expr": stored["billing_setting.billing_expr"],
				})
				require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(stored["ModelPrice"]))
				request.DryRun = true
				request.PreviewToken = ""
				preview, err = SyncAistarsLabConfig(t.Context(), request)
				require.NoError(t, err)
				assert.Empty(t, preview.CapabilityChanges, "committed capabilities reload without stale factory limits")
				assert.Empty(t, preview.AddedModels)
				assert.Empty(t, preview.RemovedModels)
				assert.Empty(t, preview.PriceChanges)
				assert.Empty(t, preview.ExpressionChanges, "apply then reload then preview must not repeat the same changes")
				assert.Empty(t, preview.MappingChanges)
				assert.Contains(t, billing_setting.GetConfiguredBillingExprCopy(), "seedance-720p-c53", "retired models keep historical administrator prices")
				assert.Contains(t, billing_setting.GetConfiguredBillingModeCopy(), "seedance-720p-c53")
				assert.Equal(t, `tier("base", u("seconds") * 9.99)`, billing_setting.GetConfiguredBillingExprCopy()["other-model"])
				assert.Equal(t, 9.99, ratio_setting.GetModelPriceCopy()["other-model"])
				var savedChannel model.Channel
				require.NoError(t, db.First(&savedChannel, 17).Error)
				assert.NotContains(t, strings.Split(savedChannel.Models, ","), "seedance-720p-c53")
				assert.Contains(t, savedChannel.Models, "other-model")
				assert.Equal(t, "upstream-other", parseAistarsLabStringMap(savedChannel.GetModelMapping())["other-model"])
				var markupValue float64
				require.NoError(t, common.UnmarshalJsonStr(stored["AistarsLabMarkupRate"], &markupValue))
				assert.Equal(t, markup, markupValue)
				if index == 0 {
					assert.NotContains(t, stored, "billing_expr", "must not create ignored pricing options")
					assert.NotContains(t, stored, "billing_mode", "must not create ignored pricing options")
				} else {
					assert.Equal(t, initial["billing_setting.billing_expr"], stored["billing_expr"], "existing ignored keys are not a migration source")
					assert.Equal(t, initial["billing_setting.billing_mode"], stored["billing_mode"])
				}
			}
		})
	}
}

func setupAistarsLabSyncDatabase(t *testing.T, dialect string) *gorm.DB {
	t.Helper()
	var driver gorm.Dialector
	switch dialect {
	case "sqlite":
		driver = sqlite.Open(":memory:")
	case "mysql":
		dsn := os.Getenv("AISTARSLAB_SYNC_TEST_MYSQL_DSN")
		if dsn == "" {
			t.Skip("set AISTARSLAB_SYNC_TEST_MYSQL_DSN to an isolated test database")
		}
		driver = mysql.Open(dsn)
	case "postgres":
		dsn := os.Getenv("AISTARSLAB_SYNC_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("set AISTARSLAB_SYNC_TEST_POSTGRES_DSN to an isolated test database")
		}
		driver = postgres.Open(dsn)
	}
	db, err := gorm.Open(driver, &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	for _, table := range []any{&model.Option{}, &model.Channel{}, &model.Ability{}, &model.TaskPlugin{}} {
		require.False(t, db.Migrator().HasTable(table), "use an empty isolated database; existing tables must never be modified")
	}
	versionQuery := "SELECT VERSION()"
	if dialect == "sqlite" {
		versionQuery = "SELECT sqlite_version()"
	}
	var version string
	require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
	t.Logf("database: %s %s", dialect, version)
	oldDB, oldCache, oldType := model.DB, common.MemoryCacheEnabled, common.MainDatabaseType()
	common.OptionMapRWMutex.Lock()
	oldOptions := maps.Clone(common.OptionMap)
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	billing := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
	oldModes, oldExpressions := billing.BillingMode, billing.BillingExpr
	oldPrices, err := common.Marshal(ratio_setting.GetModelPriceCopy())
	require.NoError(t, err)
	model.DB, common.MemoryCacheEnabled = db, false
	switch dialect {
	case "sqlite":
		common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	case "mysql":
		common.SetMainDatabaseType(common.DatabaseTypeMySQL)
	case "postgres":
		common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	}
	t.Cleanup(func() {
		for _, table := range []any{&model.Ability{}, &model.Channel{}, &model.Option{}, &model.TaskPlugin{}} {
			require.NoError(t, db.Migrator().DropTable(table))
		}
		model.DB, common.MemoryCacheEnabled = oldDB, oldCache
		common.SetMainDatabaseType(oldType)
		billing.BillingMode, billing.BillingExpr = oldModes, oldExpressions
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(oldPrices)))
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptions
		common.OptionMapRWMutex.Unlock()
		model.InvalidatePricingCache()
	})
	for range 2 {
		require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Channel{}, &model.Ability{}, &model.TaskPlugin{}))
	}

	return db
}

func TestAistarsLabSyncedDurationAndMediaContract(t *testing.T) {
	configs := []aistarsLabVideoConfig{{Channel: "50", Models: []aistarsLabConfigModel{
		{Model: "seedance-2.5", Modes: []string{"text2video", "image2video"}, AspectRatios: []string{"16:9", "9:16"}, Duration: aistarsLabDuration{Options: []int{5, 10, 15, 30}}, InputImagesMax: common.GetPointer(9), InputVideosMax: common.GetPointer(0), InputAudiosMax: common.GetPointer(0), Qualities: []aistarsLabQuality{newAistarsLabQuality("720p", "fixed_total", 750)}},
		{Model: "seedance-2.0", Modes: []string{"text2video", "image2video"}, AspectRatios: []string{"16:9", "9:16", "1:1"}, Duration: aistarsLabDuration{Min: common.GetPointer(4), Max: common.GetPointer(15)}, InputImagesMax: common.GetPointer(9), InputVideosMax: common.GetPointer(3), InputAudiosMax: common.GetPointer(3), Qualities: []aistarsLabQuality{newAistarsLabQuality("720p", "fixed_total", 450)}},
	}}}
	models := flattenAistarsLabSeedanceModels(configs, 100, 1.3)
	persisted, plugin, err := buildAistarsLabSyncedPlugin(models)
	require.NoError(t, err)
	restarted, err := jsplugin.NewRegistry().Register(string(persisted.Source), jsplugin.Options{})
	require.NoError(t, err)
	name := "seedance-720p-seedance-2.5-c50"
	caps := restarted.Meta.VideoCapabilities[name]
	assert.Equal(t, []int{5, 10, 15, 30}, caps.SecondsSteps)
	assert.Equal(t, []string{"16:9", "9:16"}, caps.Ratios)
	require.NotNil(t, caps.MaxVideos)
	assert.Zero(t, *caps.MaxVideos)
	assert.Equal(t, 15, *restarted.Meta.VideoCapabilities["seedance-720p-c50"].MaxSeconds)
	for _, seconds := range []int{5, 10, 15, 30} {
		value, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{"baseUrl": "https://api.example/openai", "apiKey": "fixture", "upstreamModel": "50:seedance-2.5", "model": name, "requestBody": map[string]any{"model": "50:seedance-2.5", "prompt": "animate", "seconds": seconds, "size": "16:9"}})
		require.NoError(t, err)
		encoded, err := common.Marshal(value)
		require.NoError(t, err)
		var request struct {
			Body struct {
				Model   string
				Seconds string
			}
		}
		require.NoError(t, common.Unmarshal(encoded, &request))
		assert.Equal(t, "50:seedance-2.5", request.Body.Model)
		assert.Equal(t, strconv.Itoa(seconds), request.Body.Seconds)
	}
	for _, body := range []map[string]any{
		{"seconds": 4}, {"seconds": 7}, {"seconds": 16}, {"seconds": 31}, {"seconds": 30, "size": "1:1"},
		{"seconds": 30, "resolution": "1080p"}, {"seconds": 30, "videos": []string{"https://cdn.example/video.mp4"}},
		{"seconds": 30, "audios": []string{"https://cdn.example/audio.mp3"}},
		{"seconds": 30, "images": []string{"https://cdn.example/image.png"}, "metadata": map[string]any{"mode_type": "frames2video"}},
	} {
		body["model"] = name
		body["prompt"] = "animate"
		_, err := plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{"baseUrl": "https://api.example/openai", "apiKey": "fixture", "upstreamModel": "50:seedance-2.5", "requestBody": body})
		require.Error(t, err, "unsupported input must fail before any upstream request: %v", body)
	}
	// Existing c50 models keep their own range and reference support.
	_, err = plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{"upstreamModel": "50:seedance-2.0", "requestBody": map[string]any{"model": "seedance-720p-c50", "prompt": "animate", "seconds": 15, "videos": []string{"https://cdn.example/video.mp4"}}})
	require.NoError(t, err)
	_, err = plugin.Engine.Call(t.Context(), "buildSubmitRequest", map[string]any{"upstreamModel": "50:seedance-2.0", "requestBody": map[string]any{"model": "seedance-720p-c50", "prompt": "animate", "seconds": 30}})
	require.Error(t, err)
}

func TestAistarsLabRecordedUpstreamConfig(t *testing.T) {
	path := os.Getenv("AISTARSLAB_SYNC_REPLAY_CONFIG")
	if path == "" {
		t.Skip("set AISTARSLAB_SYNC_REPLAY_CONFIG to a credential-free upstream config snapshot")
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var response aistarsLabConfigResponse
	require.NoError(t, common.Unmarshal(data, &response))
	models := flattenAistarsLabSeedanceModels(response.Data.VideoConfig, 100, 1.3)
	_, plugin, err := buildAistarsLabSyncedPlugin(models)
	require.NoError(t, err)
	require.Greater(t, len(models), 34)
	assert.Equal(t, []int{5, 10, 15, 30}, plugin.Meta.VideoCapabilities["seedance-720p-seedance-2.5-c50"].SecondsSteps)
	require.Contains(t, plugin.Meta.VideoCapabilities, "aistarslab-wan-3.0-720p-c68")
	assert.Equal(t, []string{"2K"}, plugin.Meta.VideoCapabilities["aistarslab-minimax-h3-2k-c59"].Resolutions)
	for _, item := range models {
		schema, _ := plugin.Meta.UsageForModel(item.PublicModel)
		require.True(t, billing_setting.TaskExprCompatible(item.BillingExpression, schema), item.PublicModel)
	}
	t.Logf("replayed %d video variants across %d upstream routes", len(models), len(response.Data.VideoConfig))
}

func newAistarsLabQuality(quality, pricingType string, credits float64) aistarsLabQuality {
	item := aistarsLabQuality{Quality: quality}
	item.Pricing.Type = pricingType
	item.Pricing.Credits = common.GetPointer(credits)
	item.Pricing.InputVideoMultiplier = common.GetPointer(1.5)
	return item
}
