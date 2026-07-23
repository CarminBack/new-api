package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFlattenAistarsLabSeedanceModels(t *testing.T) {
	durationMin := 4
	durationMax := 15
	configs := []aistarsLabVideoConfig{
		{
			Channel:       "12",
			Title:         "视频-Seedance2.0-720P-推荐1",
			DefaultOption: true,
			Models: []aistarsLabConfigModel{
				{
					Model:        "seedance-2.0-720p-fast",
					Modes:        []string{"text2video", "image2video"},
					AspectRatios: []string{"16:9", "9:16"},
					Duration: aistarsLabDuration{
						Min: &durationMin,
						Max: &durationMax,
					},
					InputImagesMax: 9,
					InputVideosMax: 3,
					InputAudiosMax: 3,
					Qualities: []aistarsLabQuality{
						{
							Quality: "720p",
							Pricing: struct {
								Type    string  `json:"type"`
								Credits float64 `json:"credits"`
							}{
								Type:    "per_second",
								Credits: 44,
							},
						},
					},
				},
				{
					Model: "seedance-2.0-720p",
					Qualities: []aistarsLabQuality{
						{
							Quality: "720p",
							Pricing: struct {
								Type    string  `json:"type"`
								Credits float64 `json:"credits"`
							}{
								Type:    "per_second",
								Credits: 52,
							},
						},
					},
				},
			},
		},
		{
			Channel: "37",
			Models: []aistarsLabConfigModel{
				{
					Model: "seedance-2.0-720p-fast",
					Qualities: []aistarsLabQuality{
						{
							Quality: "720p",
							Pricing: struct {
								Type    string  `json:"type"`
								Credits float64 `json:"credits"`
							}{
								Type:    "fixed_total",
								Credits: 350,
							},
						},
					},
				},
				{
					Model: "seedance-2.0",
					Qualities: []aistarsLabQuality{
						{
							Quality: "4k",
							Pricing: struct {
								Type    string  `json:"type"`
								Credits float64 `json:"credits"`
							}{
								Type:    "per_second",
								Credits: 260,
							},
						},
					},
				},
			},
		},
	}

	models := flattenAistarsLabSeedanceModels(configs, 100, 1.3)

	assert.Len(t, models, 4)
	byName := make(map[string]AistarsLabSeedanceModel)
	for _, item := range models {
		byName[item.PublicModel] = item
	}
	assert.Equal(t, "12:seedance-2.0-720p-fast", byName["seedance-720p-fast-c12"].UpstreamModel)
	assert.Equal(t, ratio_setting.TaskBillingUnitPerSecond, byName["seedance-720p-fast-c12"].BillingUnit)
	assert.Equal(t, 0.57, byName["seedance-720p-fast-c12"].Price)
	assert.Equal(t, 0.68, byName["seedance-720p-c12"].Price)
	assert.Equal(t, ratio_setting.TaskBillingUnitPerItem, byName["seedance-720p-fast-c37"].BillingUnit)
	assert.Equal(t, 4.55, byName["seedance-720p-fast-c37"].Price)
	assert.Equal(t, "37:seedance-2.0", byName["seedance-4k-c37"].UpstreamModel)
	assert.Equal(t, 3.38, byName["seedance-4k-c37"].Price)
	assert.Equal(t, &durationMin, byName["seedance-720p-fast-c12"].DurationMin)
}

func TestBuildAistarsLabSeedanceAlias(t *testing.T) {
	assert.Equal(t, "seedance-720p-fast-c12", buildAistarsLabSeedanceAlias("seedance-2.0-720p-fast", "720p", "12"))
	assert.Equal(t, "seedance-1080p-c30", buildAistarsLabSeedanceAlias("seedance-2.0", "1080p", "30"))
	assert.Equal(t, "seedance-720p-fast-4img-c18", buildAistarsLabSeedanceAlias("seedance-2.0-720p-fast-4img", "720p", "18"))
}

func TestFilterOutAistarsLabSeedanceAliasesRemovesRawModels(t *testing.T) {
	filtered := filterOutAistarsLabSeedanceAliases([]string{
		"grok-video-1.5",
		"seedance-720p-fast-c12",
		"12:seedance-2.0-720p-fast",
		"seedance-2.0-720p",
		"grok-video-1.5",
	})

	assert.Equal(t, []string{"grok-video-1.5"}, filtered)
}

func TestNormalizeAistarsLabSyncRequestUsesConfiguredMarkupRate(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	originalValue, existed := common.OptionMap["AistarsLabMarkupRate"]
	common.OptionMap["AistarsLabMarkupRate"] = "1.42"
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if existed {
			common.OptionMap["AistarsLabMarkupRate"] = originalValue
		} else {
			delete(common.OptionMap, "AistarsLabMarkupRate")
		}
	})

	normalized := normalizeAistarsLabSyncRequest(AistarsLabSyncRequest{})

	assert.Equal(t, 1.42, normalized.MarkupRate)
}

func TestUpsertAistarsLabSeedanceModelMetaDeletesOnlyRemovedManagedAliases(t *testing.T) {
	require.NoError(t, model.DB.AutoMigrate(&model.Model{}))

	const (
		activeAlias    = "seedance-test-c991"
		removedAlias   = "seedance-test-c992"
		unrelatedAlias = "seedance-test-c993"
		officialAlias  = "seedance-test-c994"
	)
	names := []string{activeAlias, removedAlias, unrelatedAlias, officialAlias}
	require.NoError(t, model.DB.Unscoped().Where("model_name IN ?", names).Delete(&model.Model{}).Error)
	t.Cleanup(func() {
		_ = model.DB.Unscoped().Where("model_name IN ?", names).Delete(&model.Model{}).Error
	})

	for _, item := range []*model.Model{
		{ModelName: activeAlias, VendorID: 17, Status: 0, SyncOfficial: 0, NameRule: model.NameRuleExact},
		{ModelName: removedAlias, VendorID: 17, Status: 1, SyncOfficial: 0, NameRule: model.NameRuleExact},
		{ModelName: unrelatedAlias, VendorID: 99, Status: 1, SyncOfficial: 0, NameRule: model.NameRuleExact},
		{ModelName: officialAlias, VendorID: 17, Status: 1, SyncOfficial: 1, NameRule: model.NameRuleExact},
	} {
		require.NoError(t, item.Insert())
	}

	require.NoError(t, upsertAistarsLabSeedanceModelMeta([]AistarsLabSeedanceModel{{
		PublicModel: activeAlias,
		Channel:     "991",
		Quality:     "test",
		BillingUnit: ratio_setting.TaskBillingUnitPerSecond,
	}}))

	var active model.Model
	require.NoError(t, model.DB.Where("model_name = ?", activeAlias).First(&active).Error)
	assert.Equal(t, 1, active.Status)

	var removed model.Model
	require.ErrorIs(t, model.DB.Where("model_name = ?", removedAlias).First(&removed).Error, gorm.ErrRecordNotFound)
	require.NoError(t, model.DB.Unscoped().Where("model_name = ?", removedAlias).First(&removed).Error)
	assert.True(t, removed.DeletedAt.Valid)

	for _, name := range []string{unrelatedAlias, officialAlias} {
		var preserved model.Model
		require.NoError(t, model.DB.Where("model_name = ?", name).First(&preserved).Error)
		assert.False(t, preserved.DeletedAt.Valid)
	}
}
