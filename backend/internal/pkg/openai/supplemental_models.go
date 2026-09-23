package openai

import (
	"slices"
	"strings"
	"time"
)

// NormalizeSupplementalModel recognizes explicit models and supported suffixes.
// Unknown siblings must not silently become another model's price or route.
func NormalizeSupplementalModel(model string) string {
	model = CanonicalizeOpenAIModelAliasSpelling(model)
	model = strings.TrimSuffix(model, "-openai-compact")
	for _, item := range SupplementalModels {
		if model == item.ID {
			return item.ID
		}
		suffix, ok := strings.CutPrefix(model, item.ID+"-")
		if !ok {
			continue
		}
		if slices.Contains([]string{"none", "low", "medium", "high", "xhigh", "max"}, suffix) {
			return item.ID
		}
		for _, layout := range []string{"2006-01-02", "20060102"} {
			if _, err := time.Parse(layout, suffix); err == nil {
				return item.ID
			}
		}
	}
	return ""
}
