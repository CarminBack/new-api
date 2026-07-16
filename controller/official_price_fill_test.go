package controller

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/stretchr/testify/require"
)

func TestOfficialPriceFillAddsOnlyMissingModels(t *testing.T) {
	official := addOfficialLongContextPricing(map[string]any{
		"model_ratio": map[string]any{
			"gemini-3.5-flash": 0.75,
			"gpt-5.4":          1.25,
			"gpt-5.5":          2.5,
			"grok-build-0.1":   0.5,
		},
		"completion_ratio": map[string]any{
			"gemini-3.5-flash": 6.0,
			"gpt-5.4":          6.0,
			"gpt-5.5":          6.0,
			"grok-build-0.1":   2.0,
		},
		"cache_ratio": map[string]any{
			"gemini-3.5-flash": 0.1,
			"gpt-5.4":          0.1,
			"gpt-5.5":          0.1,
			"grok-build-0.1":   0.2,
		},
	})
	local := map[string]any{
		"model_ratio": map[string]any{
			"already-priced": 9.0,
		},
		"cache_ratio": map[string]any{
			"gpt-5.4-openai-compact": 0.25,
		},
	}

	plan := buildOfficialPriceFillPlan([]string{
		"already-priced",
		"gemini-3.5-flash",
		"gpt-5.4-2026-03-05",
		"gpt-5.4-openai-compact",
		"gpt-5.5-none",
		"grok-build",
		"grok-search",
	}, local, official)

	require.Equal(t, 7, plan.Result.EnabledModels)
	require.Equal(t, 1, plan.Result.AlreadyPriced)
	require.Len(t, plan.Result.FilledModels, 5)
	require.Equal(t, []OfficialPriceFillSkip{{Model: "grok-search", Reason: "official price not found"}}, plan.Result.SkippedModels)

	items := make(map[string]OfficialPriceFillItem, len(plan.Result.FilledModels))
	for _, item := range plan.Result.FilledModels {
		items[item.Model] = item
	}
	require.Equal(t, "gpt-5.4", items["gpt-5.4-2026-03-05"].SourceModel)
	require.Equal(t, "gpt-5.4", items["gpt-5.4-openai-compact"].SourceModel)
	require.Equal(t, "gpt-5.5", items["gpt-5.5-none"].SourceModel)
	require.Equal(t, "grok-build-0.1", items["grok-build"].SourceModel)
	require.NotContains(t, items["gpt-5.4-openai-compact"].Fields, "cache_ratio")

	localRatios := valueMap(plan.PricingData["model_ratio"])
	require.Equal(t, 9.0, localRatios["already-priced"])
	require.Equal(t, 1.25, localRatios["gpt-5.4-openai-compact"])
	localCacheRatios := valueMap(plan.PricingData["cache_ratio"])
	require.Equal(t, 0.25, localCacheRatios["gpt-5.4-openai-compact"])

	secondPlan := buildOfficialPriceFillPlan([]string{
		"already-priced",
		"gemini-3.5-flash",
		"gpt-5.4-2026-03-05",
		"gpt-5.4-openai-compact",
		"gpt-5.5-none",
		"grok-build",
		"grok-search",
	}, plan.PricingData, official)
	require.Empty(t, secondPlan.Result.FilledModels)
	require.Equal(t, 6, secondPlan.Result.AlreadyPriced)
	require.Equal(t, []OfficialPriceFillSkip{{Model: "grok-search", Reason: "official price not found"}}, secondPlan.Result.SkippedModels)
}

func TestOfficialPriceFillBuildsLongContextTier(t *testing.T) {
	official := addOfficialLongContextPricing(map[string]any{
		"model_ratio":      map[string]any{"gpt-5.4": 1.25},
		"completion_ratio": map[string]any{"gpt-5.4": 6.0},
		"cache_ratio":      map[string]any{"gpt-5.4": 0.1},
	})

	modes := valueMap(official[billing_setting.BillingModeField])
	exprs := valueMap(official[billing_setting.BillingExprField])
	require.Equal(t, billing_setting.BillingModeTieredExpr, modes["gpt-5.4"])
	expr, ok := exprs["gpt-5.4"].(string)
	require.True(t, ok)
	require.Contains(t, expr, "len <= 272000")
	require.Contains(t, expr, `tier("standard", p * 2.5 + c * 15 + cr * 0.25)`)
	require.Contains(t, expr, `tier("long_context", p * 5 + c * 22.5 + cr * 0.5)`)
	require.NoError(t, billing_setting.SmokeTestExpr(expr))
}

func TestOfficialPriceFillOptionValuesOnlyIncludesChangedFields(t *testing.T) {
	plan := officialPriceFillPlan{
		PricingData: map[string]any{
			"model_ratio":      map[string]any{"gpt-5.4": 1.25},
			"completion_ratio": map[string]any{"gpt-5.4": 6.0},
		},
		ChangedFields: map[string]struct{}{"model_ratio": {}},
	}

	values, err := officialPriceFillOptionValues(plan)
	require.NoError(t, err)
	require.Len(t, values, 1)
	require.JSONEq(t, `{"gpt-5.4":1.25}`, values["ModelRatio"])
	require.False(t, strings.Contains(values["ModelRatio"], "completion"))
}
