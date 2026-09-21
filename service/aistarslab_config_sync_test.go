package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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
}

func newAistarsLabQuality(quality, pricingType string, credits float64) aistarsLabQuality {
	item := aistarsLabQuality{Quality: quality}
	item.Pricing.Type = pricingType
	item.Pricing.Credits = credits
	return item
}
