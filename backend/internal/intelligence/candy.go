package intelligence

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Only the built-in question with its default answer policy is upgraded. Saved
// legacy configs keep working without rewriting settings or custom prompt rules.
func isBuiltinCandyProbe(c Config) bool {
	normalize := func(s string) string {
		return strings.Join(strings.Fields(norm.NFKC.String(s)), "")
	}
	if normalize(c.Prompt) != normalize(DefaultPrompt) {
		return false
	}
	if c.MatchMode != "exact" && c.MatchMode != "contains_any" {
		return false
	}
	expected := make(map[string]bool, len(c.Expected))
	for _, value := range c.Expected {
		expected[norm.NFKC.String(strings.TrimSpace(value))] = true
	}
	if len(expected) == 1 && expected["21"] {
		return true
	}
	return c.MatchMode == "contains_any" && len(expected) == 2 && expected["21"] && expected["手感"]
}

const candyAnswerInstruction = "\n\n请自行推理。回复必须只包含最终最少数量的一个阿拉伯数字整数，不得包含解释、单位、Markdown、多个候选数或计算过程。"

func probePrompt(c Config) string {
	if isBuiltinCandyProbe(c) {
		return c.Prompt + candyAnswerInstruction
	}
	return c.Prompt
}
