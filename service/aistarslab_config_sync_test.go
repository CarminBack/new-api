package service

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
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
					InputImagesMax: 9, InputVideosMax: 3, InputAudiosMax: 3,
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
	assert.Equal(t, `tier("base", u("seconds") * 0.7)`, perSecond.BillingExpression)
	assert.Equal(t, "47:seedance-2.0", perSecond.UpstreamModel)
	assert.Equal(t, 9, perSecond.InputImagesMax)

	perItem := byName["seedance-4k-c47"]
	assert.Equal(t, "per_item", perItem.BillingUnit)
	assert.Equal(t, 3.51, perItem.Price)
	assert.Equal(t, `tier("base", u("videos") * 3.51)`, perItem.BillingExpression)
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
	t.Setenv("AISTARSLAB_API_KEY", "test-key")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "Bearer test-key", request.Header.Get("Authorization"))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"code":0,"data":{"videoConfig":[{"channel":"47","defaultOption":true,"models":[{"model":"seedance-2.0","qualities":[{"quality":"720p","pricing":{"type":"per_second","credits":54}}]}]}]}}`))
	}))
	defer server.Close()

	result, err := SyncAistarsLabConfig(context.Background(), AistarsLabSyncRequest{
		DryRun: true, ChannelID: 99999, ConfigURL: server.URL, CreditRate: 100, MarkupRate: 1.3,
	})
	require.NoError(t, err)
	require.Len(t, result.Models, 1)
	assert.True(t, result.DryRun)
	assert.Equal(t, "seedance-720p-c47", result.Models[0].PublicModel)
	assert.Equal(t, `tier("base", u("seconds") * 0.7)`, result.Models[0].BillingExpression)
	require.Len(t, result.PriceChanges, 1)
	assert.Equal(t, "seedance-720p-c47", result.PriceChanges[0].Model)
	require.NotNil(t, result.PriceChanges[0].New)
	assert.InDelta(t, 0.7, *result.PriceChanges[0].New, 1e-9)
	require.Len(t, result.ExpressionChanges, 1)
	assert.Equal(t, `tier("base", u("seconds") * 0.7)`, result.ExpressionChanges[0].New)
	require.Len(t, result.MappingChanges, 1)
	assert.Equal(t, "47:seedance-2.0", result.MappingChanges[0].New)
}

func TestAistarsLabApplyPersistsEffectivePricing(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
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
			for _, table := range []any{&model.Option{}, &model.Channel{}, &model.Ability{}} {
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
				for _, table := range []any{&model.Ability{}, &model.Channel{}, &model.Option{}} {
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
				require.NoError(t, db.AutoMigrate(&model.Option{}, &model.Channel{}, &model.Ability{}))
			}
			initial := map[string]string{
				"billing_setting.billing_mode": `{"seedance-720p-c47":"tiered_expr","seedance-720p-c53":"tiered_expr","other-model":"tiered_expr"}`,
				"billing_setting.billing_expr": `{"seedance-720p-c47":"tier(\"base\", u(\"seconds\") * 0.2)","seedance-720p-c53":"tier(\"base\", u(\"seconds\") * 0.62)","other-model":"tier(\"base\", u(\"seconds\") * 9.99)"}`,
				"ModelPrice":                   `{"seedance-720p-c47":0.2,"seedance-720p-c53":0.62,"other-model":9.99}`,
				"AistarsLabMarkupRate":         "1.3",
			}
			require.NoError(t, model.UpdateOptionsBulk(initial))
			mapping := `{"other-model":"upstream-other","seedance-720p-c47":"old:seedance-2.0","seedance-720p-c53":"53:seedance-2.0"}`
			channel := &model.Channel{Id: 17, Type: 1, Key: "fixture", Name: "sync-fixture", Status: common.ChannelStatusEnabled,
				Group: "default", Models: "other-model,seedance-720p-c47,seedance-720p-c53", ModelMapping: &mapping}
			require.NoError(t, channel.Insert())
			configs := []aistarsLabVideoConfig{
				{Channel: "47", Models: []aistarsLabConfigModel{{Model: "seedance-2.0", Qualities: []aistarsLabQuality{newAistarsLabQuality("720p", "per_second", 54)}}}},
				{Channel: "50", Models: []aistarsLabConfigModel{{Model: "seedance-2.0", Qualities: []aistarsLabQuality{newAistarsLabQuality("720p", "fixed_total", 450)}}}},
			}
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
				require.NoError(t, applyAistarsLabSync(17, markup, models))
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
				preview := buildAistarsLabSyncResult(AistarsLabSyncRequest{DryRun: true, ChannelID: 17, MarkupRate: markup}, models)
				assert.Empty(t, preview.AddedModels)
				assert.Empty(t, preview.RemovedModels)
				assert.Empty(t, preview.PriceChanges)
				assert.Empty(t, preview.ExpressionChanges, "apply then reload then preview must not repeat the same changes")
				assert.Empty(t, preview.MappingChanges)
				assert.NotContains(t, billing_setting.GetConfiguredBillingExprCopy(), "seedance-720p-c53")
				assert.NotContains(t, billing_setting.GetConfiguredBillingModeCopy(), "seedance-720p-c53")
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

func newAistarsLabQuality(quality, pricingType string, credits float64) aistarsLabQuality {
	item := aistarsLabQuality{Quality: quality}
	item.Pricing.Type = pricingType
	item.Pricing.Credits = credits
	return item
}
