package service

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/setting/billing_setting"
)

const (
	TaskPricingUnitPerSecond = "per_second"
	TaskPricingUnitPerItem   = "per_item"
)

type TaskPricingMigrationEntry struct {
	Model string  `json:"model"`
	Unit  string  `json:"unit"`
	Price float64 `json:"price"`
}

type TaskPricingMigrationRequest struct {
	DryRun bool                        `json:"dry_run"`
	Models []TaskPricingMigrationEntry `json:"models"`
}

type TaskPricingMigrationChange struct {
	Model string `json:"model"`
	Old   string `json:"old,omitempty"`
	New   string `json:"new"`
}

type TaskPricingMigrationResult struct {
	DryRun  bool                         `json:"dry_run"`
	Total   int                          `json:"total"`
	Changed int                          `json:"changed"`
	Changes []TaskPricingMigrationChange `json:"changes"`
}

func MigrateTaskPricing(request TaskPricingMigrationRequest) (*TaskPricingMigrationResult, error) {
	if len(request.Models) == 0 {
		return nil, fmt.Errorf("at least one task pricing model is required")
	}
	generation := jsplugin.DefaultRegistry.Generation()
	modes := billing_setting.GetConfiguredBillingModeCopy()
	expressions := billing_setting.GetConfiguredBillingExprCopy()
	seen := make(map[string]bool, len(request.Models))
	result := &TaskPricingMigrationResult{DryRun: request.DryRun, Total: len(request.Models)}

	for _, entry := range request.Models {
		entry.Model = strings.TrimSpace(entry.Model)
		entry.Unit = strings.ToLower(strings.TrimSpace(entry.Unit))
		if entry.Model == "" {
			return nil, fmt.Errorf("model name is required")
		}
		if seen[entry.Model] {
			return nil, fmt.Errorf("duplicate task pricing model: %s", entry.Model)
		}
		seen[entry.Model] = true
		if entry.Price < 0 || math.IsNaN(entry.Price) || math.IsInf(entry.Price, 0) {
			return nil, fmt.Errorf("model %s price must be finite and non-negative", entry.Model)
		}
		expression, err := buildTaskPricingExpression(entry.Unit, entry.Price)
		if err != nil {
			return nil, fmt.Errorf("model %s: %w", entry.Model, err)
		}
		plugin, found := generation.GetByModel(entry.Model)
		if !found {
			if alias, resolved := model.ResolveTaskModelAlias(generation, entry.Model); resolved {
				plugin, found = generation.Get(alias.PluginKey)
			}
		}
		if !found || plugin == nil {
			return nil, fmt.Errorf("model %s has no task plugin usage schema", entry.Model)
		}
		schema, _ := plugin.Meta.UsageForModel(entry.Model)
		if schema == nil {
			if alias, resolved := model.ResolveTaskModelAlias(generation, entry.Model); resolved {
				schema, _ = plugin.Meta.UsageForModel(alias.Declared)
			}
		}
		if err := billing_setting.SmokeTestTaskExpr(expression, schema); err != nil {
			return nil, fmt.Errorf("model %s: %w", entry.Model, err)
		}
		old := expressions[entry.Model]
		if modes[entry.Model] != billing_setting.BillingModeTieredExpr || old != expression {
			result.Changes = append(result.Changes, TaskPricingMigrationChange{Model: entry.Model, Old: old, New: expression})
		}
		modes[entry.Model] = billing_setting.BillingModeTieredExpr
		expressions[entry.Model] = expression
	}
	sort.Slice(result.Changes, func(i, j int) bool { return result.Changes[i].Model < result.Changes[j].Model })
	result.Changed = len(result.Changes)
	if request.DryRun || result.Changed == 0 {
		return result, nil
	}
	modeJSON, err := common.Marshal(modes)
	if err != nil {
		return nil, err
	}
	expressionJSON, err := common.Marshal(expressions)
	if err != nil {
		return nil, err
	}
	if err := model.UpdateOptionsBulk(map[string]string{
		"billing_setting.billing_mode": string(modeJSON),
		"billing_setting.billing_expr": string(expressionJSON),
	}); err != nil {
		return nil, err
	}
	model.RefreshPricing()
	return result, nil
}

func buildTaskPricingExpression(unit string, price float64) (string, error) {
	formatted := strconv.FormatFloat(price, 'f', -1, 64)
	switch unit {
	case TaskPricingUnitPerSecond:
		return fmt.Sprintf(`tier("base", u("seconds") * %s)`, formatted), nil
	case TaskPricingUnitPerItem:
		return fmt.Sprintf(`tier("base", u("videos") * %s)`, formatted), nil
	default:
		return "", fmt.Errorf("unit must be %s or %s", TaskPricingUnitPerSecond, TaskPricingUnitPerItem)
	}
}
