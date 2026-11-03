package model

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"gorm.io/gorm"
)

// AistarsLabChannelSync is one previewed upstream configuration snapshot.
type AistarsLabChannelSync struct {
	Channel                 *Channel
	ExpectedPluginHash      string
	Models                  []string
	Mapping                 string
	Plugin                  *TaskPlugin
	Pricing                 map[string]PricingValues
	ExpectedPricingVersions map[string]string
	MarkupRate              string
}

// ApplyAistarsLabChannelSync commits prices, routing and the generated plugin
// together. The existing plugin loader publishes the committed snapshot.
func ApplyAistarsLabChannelSync(sync AistarsLabChannelSync) error {
	err := mutateModelPricingOptions(func(tx *gorm.DB, values map[string]map[string]any) error {
		var channel Channel
		if err := lockForUpdate(tx).First(&channel, sync.Channel.Id).Error; err != nil {
			return err
		}
		if channel.Type != sync.Channel.Type || !channel.GetSetting().BindsTaskPlugin(sync.Plugin.Key) || channel.Models != sync.Channel.Models || channel.GetModelMapping() != sync.Channel.GetModelMapping() {
			return errors.New("channel changed; preview again")
		}
		var incumbent TaskPlugin
		err := lockForUpdate(tx).Where(&TaskPlugin{Key: sync.Plugin.Key, Active: true}).First(&incumbent).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if incumbent.Id != 0 && !incumbent.Enabled {
			return errors.New("AistarsLab plugin is disabled; enable it before synchronization")
		}
		if incumbent.SourceHash != sync.ExpectedPluginHash {
			return errors.New("plugin changed; preview again")
		}
		for name, version := range sync.ExpectedPricingVersions {
			if ModelPricingVersion(modelPricingValues(values, name)) != version {
				return fmt.Errorf("model %s changed; preview again", name)
			}
		}
		for name, price := range sync.Pricing {
			draft := modelPricingValues(values, name)
			maps.Copy(draft, price)
			if variants, ok := draft[billing_setting.PluginBillingExprOption].(map[string]any); ok {
				if _, exists := variants[sync.Plugin.Key]; exists {
					variants[sync.Plugin.Key] = price["billing_setting.billing_expr"]
				}
			}
			replaceModelPricing(values, name, draft)
		}
		// Retired channel models keep historical prices and administrator overrides.
		// They may still be referenced by other channels or in-flight tasks.
		var stored TaskPlugin
		err = tx.Where(&TaskPlugin{Key: sync.Plugin.Key, Version: sync.Plugin.Version}).First(&stored).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			sync.Plugin.Icon = incumbent.Icon
			sync.Plugin.CreatedAt = time.Now().Unix()
			if err := tx.Create(sync.Plugin).Error; err != nil {
				return err
			}
			stored = *sync.Plugin
		} else if err != nil {
			return err
		} else if stored.SourceHash != sync.Plugin.SourceHash {
			return errors.New("plugin version source conflict")
		}
		if err := tx.Model(&TaskPlugin{}).Where(&TaskPlugin{Key: sync.Plugin.Key}).Update("active", false).Error; err != nil {
			return err
		}
		if err := tx.Model(&stored).Updates(map[string]any{"active": true, "enabled": true}).Error; err != nil {
			return err
		}
		if err := tx.Model(&channel).Updates(map[string]any{"models": strings.Join(sync.Models, ","), "model_mapping": sync.Mapping}).Error; err != nil {
			return err
		}
		channel.Models = strings.Join(sync.Models, ",")
		channel.ModelMapping = &sync.Mapping
		if err := channel.UpdateAbilities(tx); err != nil {
			return err
		}
		option := Option{Key: "AistarsLabMarkupRate"}
		if err := tx.FirstOrCreate(&option, Option{Key: option.Key}).Error; err != nil {
			return err
		}
		return tx.Model(&option).Update("value", sync.MarkupRate).Error
	})
	if err != nil {
		return err
	}
	if err := updateOptionMap("AistarsLabMarkupRate", sync.MarkupRate); err != nil {
		return err
	}
	if common.MemoryCacheEnabled {
		InitChannelCache()
	}
	return nil
}
