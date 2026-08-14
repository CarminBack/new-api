package setting

import (
	"sort"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

var groupDisplayOrder = []string{}
var groupDisplayOrderMutex sync.RWMutex

func UpdateGroupDisplayOrderByJSONString(jsonString string) error {
	if strings.TrimSpace(jsonString) == "" {
		jsonString = "[]"
	}

	updated := make([]string, 0)
	if err := common.Unmarshal([]byte(jsonString), &updated); err != nil {
		return err
	}

	normalized := make([]string, 0, len(updated))
	seen := make(map[string]struct{}, len(updated))
	for _, group := range updated {
		if group == "" {
			continue
		}
		if _, exists := seen[group]; exists {
			continue
		}
		seen[group] = struct{}{}
		normalized = append(normalized, group)
	}

	groupDisplayOrderMutex.Lock()
	groupDisplayOrder = normalized
	groupDisplayOrderMutex.Unlock()
	return nil
}

func GroupDisplayOrder2JSONString() string {
	order := GetGroupDisplayOrder()
	jsonBytes, err := common.Marshal(order)
	if err != nil {
		return "[]"
	}
	return string(jsonBytes)
}

func GetGroupDisplayOrder() []string {
	groupDisplayOrderMutex.RLock()
	defer groupDisplayOrderMutex.RUnlock()

	return append([]string(nil), groupDisplayOrder...)
}

func OrderGroupNames(names []string) []string {
	available := make(map[string]struct{}, len(names))
	for _, name := range names {
		available[name] = struct{}{}
	}

	ordered := make([]string, 0, len(available))
	for _, name := range GetGroupDisplayOrder() {
		if _, exists := available[name]; !exists {
			continue
		}
		ordered = append(ordered, name)
		delete(available, name)
	}

	remaining := make([]string, 0, len(available))
	for name := range available {
		remaining = append(remaining, name)
	}
	sort.Strings(remaining)
	return append(ordered, remaining...)
}
