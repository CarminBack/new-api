package common

import (
	"fmt"
	"path"
)

// TextFirstResponseRule matches the original client model, before channel mapping.
// Rules are ordered; an empty RequestPath matches all eligible text endpoints.
type TextFirstResponseRule struct {
	ModelPattern string `json:"model_pattern"`
	RequestPath  string `json:"request_path,omitempty"`
	Seconds      *int   `json:"seconds"`
}

var textFirstResponseRules []TextFirstResponseRule
var TextRetryMinRemainingSeconds = 5

// ParseTextFirstResponseRules validates an entire configuration before use.
func ParseTextFirstResponseRules(raw string) ([]TextFirstResponseRule, error) {
	if raw == "" {
		return nil, nil
	}
	var rules []TextFirstResponseRule
	if err := UnmarshalJsonStr(raw, &rules); err != nil {
		return nil, fmt.Errorf("invalid TEXT_FIRST_RESPONSE_TIMEOUT_RULES JSON")
	}
	for i, rule := range rules {
		if rule.ModelPattern == "" || rule.Seconds == nil || *rule.Seconds < 0 || *rule.Seconds > 86400 {
			return nil, fmt.Errorf("TEXT_FIRST_RESPONSE_TIMEOUT_RULES[%d] requires a model_pattern and seconds between 0 and 86400", i)
		}
		if _, err := path.Match(rule.ModelPattern, ""); err != nil {
			return nil, fmt.Errorf("invalid model pattern in timeout rule %d", i)
		}
		if rule.RequestPath != "" && rule.RequestPath[0] != '/' {
			return nil, fmt.Errorf("timeout rule %d request_path must start with /", i)
		}
	}
	return rules, nil
}

func TextFirstResponseSeconds(model, requestPath string) int {
	for _, rule := range textFirstResponseRules {
		if rule.RequestPath != "" && rule.RequestPath != requestPath {
			continue
		}
		if matched, _ := path.Match(rule.ModelPattern, model); matched {
			return *rule.Seconds
		}
	}
	return TextFirstResponseTimeout
}
