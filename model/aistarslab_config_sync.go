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
	// Empty preserves the legacy AistarsLab option key.
	SettingsOptionKey string
}

// ApplyAistarsLabChannelSync commits prices, routing and the generated plugin
// together. The existing plugin loader publishes the committed snapshot.
func ApplyAistarsLabChannelSync(sync AistarsLabChannelSync) error {
	optionKey := sync.SettingsOptionKey
	if optionKey == "" {
		optionKey = "AistarsLabMarkupRate"
	}
	if optionKey != "AistarsLabMarkupRate" && optionKey != "YeepaySyncSettings" {
		return errors.New("unsupported sync settings option")
	}
	var yeepayBackup string
	err := mutateModelPricingOptions(func(tx *gorm.DB, values map[string]map[string]any) error {
		var channel Channel
		if err := lockForUpdate(tx).First(&channel, sync.Channel.Id).Error; err != nil {
			return err
		}
		if channel.Type != sync.Channel.Type || !channel.GetSetting().BindsTaskPlugin(sync.Plugin.Key) || channel.Models != sync.Channel.Models || channel.GetModelMapping() != sync.Channel.GetModelMapping() {
			return errors.New("channel changed; preview again")
		}
		if sync.SettingsOptionKey == "YeepaySyncSettings" {
			before, _ := common.Marshal([]any{sync.Channel.Key, sync.Channel.BaseURL, sync.Channel.Setting, sync.Channel.OtherSettings, sync.Channel.Status, sync.Channel.Group})
			after, _ := common.Marshal([]any{channel.Key, channel.BaseURL, channel.Setting, channel.OtherSettings, channel.Status, channel.Group})
			if string(before) != string(after) {
				return errors.New("channel settings changed; preview again")
			}
		}
		var incumbent TaskPlugin
		err := lockForUpdate(tx).Where(&TaskPlugin{Key: sync.Plugin.Key, Active: true}).First(&incumbent).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if incumbent.Id != 0 && !incumbent.Enabled {
			return errors.New("task plugin is disabled; enable it before synchronization")
		}
		if incumbent.SourceHash != sync.ExpectedPluginHash {
			return errors.New("plugin changed; preview again")
		}
		for name, version := range sync.ExpectedPricingVersions {
			if ModelPricingVersion(modelPricingValues(values, name)) != version {
				return fmt.Errorf("model %s changed; preview again", name)
			}
		}
		if sync.SettingsOptionKey == "YeepaySyncSettings" {
			names := make([]string, 0, len(sync.Pricing))
			for name := range sync.Pricing {
				names = append(names, name)
			}
			if err := ValidateYeepayPricingScope(lockForUpdate(tx), names); err != nil {
				return err
			}
		}
		if sync.SettingsOptionKey == "YeepaySyncSettings" {
			previousPrices := map[string]PricingValues{}
			for name := range sync.Pricing {
				previousPrices[name] = modelPricingValues(values, name)
			}
			data, err := common.Marshal(map[string]any{"created_at": time.Now().UTC(), "channel_id": channel.Id, "models": channel.Models, "model_mapping": channel.GetModelMapping(), "plugin_version": incumbent.Version, "plugin_source_hash": incumbent.SourceHash, "pricing": previousPrices})
			if err != nil {
				return err
			}
			yeepayBackup = string(data)
			option := Option{Key: "YeepaySyncBackup"}
			if err := tx.FirstOrCreate(&option, Option{Key: option.Key}).Error; err != nil {
				return err
			}
			if err := tx.Model(&option).Update("value", yeepayBackup).Error; err != nil {
				return err
			}
		}
		for name, price := range sync.Pricing {
			draft := modelPricingValues(values, name)
			maps.Copy(draft, price)
			if _, updatingBase := price["billing_setting.billing_expr"]; updatingBase {
				if variants, ok := draft[billing_setting.PluginBillingExprOption].(map[string]any); ok {
					if _, exists := variants[sync.Plugin.Key]; exists {
						variants[sync.Plugin.Key] = price["billing_setting.billing_expr"]
					}
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
		option := Option{Key: optionKey}
		if err := tx.FirstOrCreate(&option, Option{Key: option.Key}).Error; err != nil {
			return err
		}
		return tx.Model(&option).Update("value", sync.MarkupRate).Error
	})
	if err != nil {
		return err
	}
	if err := updateOptionMap(optionKey, sync.MarkupRate); err != nil {
		return err
	}
	if yeepayBackup != "" {
		if err := updateOptionMap("YeepaySyncBackup", yeepayBackup); err != nil {
			return err
		}
	}
	if common.MemoryCacheEnabled {
		InitChannelCache()
	}
	return nil
}

// Model prices are shared across channels. Refuse cross-provider collisions
// rather than introducing hidden provider pricing or changing unrelated routes.
func ValidateYeepayPricingScope(db *gorm.DB, names []string) error {
	if len(names) == 0 {
		return nil
	}
	var channels []Channel
	if err := db.Select("id", "models", "model_mapping", "setting").Where("id <> ?", 282).Find(&channels).Error; err != nil {
		return err
	}
	selected := make(map[string]bool, len(names))
	for _, name := range names {
		selected[name] = true
	}
	for _, channel := range channels {
		if channel.GetSetting().BindsTaskPlugin("yeepay") {
			continue
		}
		var mapping map[string]string
		if err := common.UnmarshalJsonStr(channel.GetModelMapping(), &mapping); err != nil && channel.GetModelMapping() != "" {
			return errors.New("invalid channel mapping; cannot establish safe Yeepay pricing scope")
		}
		for _, name := range channel.GetModels() {
			seen := map[string]bool{}
			for name != "" && !seen[name] {
				if selected[name] {
					return fmt.Errorf("model %s is also used by channel %d from another provider; resolve the model-name conflict before synchronizing prices", name, channel.Id)
				}
				seen[name] = true
				name = mapping[name]
			}
		}
	}
	return nil
}
