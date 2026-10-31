package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// ChannelModelScope describes which model names the model management list
// should show: names a channel actually exposes and can bill. It deliberately
// mirrors relay behaviour so the list never shows a name a request cannot use,
// and never hides one that works.
type ChannelModelScope struct {
	// exposed holds every name listed in some channel's model list. An upstream
	// name reached only through an alias is not listed, so it is never exposed.
	exposed map[string]struct{}
	// tails maps an exposed alias to every mapping tail it resolves to.
	tails map[string][]string
}

// followModelMapping mirrors relay's chained redirect: it returns the tail of
// the chain, or ok=false when the chain is cyclic.
func followModelMapping(mapping map[string]string, start string) (string, bool) {
	current := start
	visited := map[string]bool{current: true}
	for {
		next, exists := mapping[current]
		if !exists || next == "" {
			return current, true
		}
		if visited[next] {
			// A self-mapping is a no-op; any other loop has no defined target.
			return current, next == current
		}
		visited[next] = true
		current = next
	}
}

// BuildChannelModelScope reads every channel once. Disabled channels still
// count: an administrator pricing or preparing a channel before enabling it
// must be able to find its models.
func BuildChannelModelScope() (*ChannelModelScope, error) {
	var channels []Channel
	if err := DB.Select("id", "models", "model_mapping").Find(&channels).Error; err != nil {
		return nil, err
	}
	scope := &ChannelModelScope{
		exposed: make(map[string]struct{}),
		tails:   make(map[string][]string),
	}
	for i := range channels {
		channel := &channels[i]
		names := normalizeLookupValues(channel.GetModels())
		mapping := make(map[string]string)
		if raw := strings.TrimSpace(channel.GetModelMapping()); raw != "" && raw != "{}" {
			// A malformed mapping is ignored rather than hiding the channel's
			// own models; relay rejects such requests separately.
			_ = common.UnmarshalJsonStr(raw, &mapping)
		}
		for _, name := range names {
			scope.exposed[name] = struct{}{}
			tail, ok := followModelMapping(mapping, name)
			if !ok || tail == "" || tail == name {
				continue
			}
			scope.tails[name] = append(scope.tails[name], tail)
		}
	}
	return scope, nil
}

// hasOwnModelPrice reports whether name has a price that relay would bill:
// a fixed price, a configured ratio, a tiered expression, or a plugin-scoped
// expression.
func hasOwnModelPrice(name string, pluginExprModels map[string]struct{}) bool {
	if _, ok := ratio_setting.GetModelPrice(name, false); ok {
		return true
	}
	if ratio_setting.HasConfiguredModelRatio(name) {
		return true
	}
	if billing_setting.GetBillingMode(name) == billing_setting.BillingModeTieredExpr {
		if expr, ok := billing_setting.GetBillingExpr(name); ok && strings.TrimSpace(expr) != "" {
			return true
		}
	}
	_, ok := pluginExprModels[name]
	return ok
}

// Includes reports whether an exact-name row belongs in the channel-scoped
// list: a channel must list it, which also excludes upstream names reached
// only through an alias, and it must be priced on its own or through its
// mapping tail (relay falls back to the tail price when the alias has none).
func (s *ChannelModelScope) Includes(name string, pluginExprModels map[string]struct{}) bool {
	if s == nil {
		return true
	}
	if _, exposed := s.exposed[name]; !exposed {
		return false
	}
	if hasOwnModelPrice(name, pluginExprModels) {
		return true
	}
	for _, tail := range s.tails[name] {
		if hasOwnModelPrice(tail, pluginExprModels) {
			return true
		}
	}
	return false
}

// PluginBillingExprModels indexes plugin-scoped expressions by model name.
func PluginBillingExprModels() map[string]struct{} {
	result := make(map[string]struct{})
	for key := range billing_setting.GetPluginBillingExprCopy() {
		if _, name, ok := billing_setting.SplitPluginBillingExprKey(key); ok {
			result[name] = struct{}{}
		}
	}
	return result
}
