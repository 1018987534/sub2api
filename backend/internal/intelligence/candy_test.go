package intelligence

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func legacyCandyConfig() Config {
	c := DefaultConfig(1)
	c.MatchMode = "contains_any"
	c.Expected = []string{"手感", "21"}
	return c
}

func TestCandyRejectsRecordedFalseNormal(t *testing.T) {
	answer, err := os.ReadFile("testdata/candy_false_normal_67653.txt")
	if err != nil {
		t.Fatal(err)
	}
	if Matches(string(answer), legacyCandyConfig()) {
		t.Fatal("recorded wrong answer 28/24 was accepted as normal")
	}
}

func TestCandyAnswerContract(t *testing.T) {
	legacy := legacyCandyConfig()
	normalized := legacyCandyConfig()
	normalized.Prompt = strings.ReplaceAll(DefaultPrompt, "\n", " \n\t")
	normalized.Expected = []string{"２１", "手感"}
	for _, c := range []Config{DefaultConfig(1), legacy, normalized} {
		for _, answer := range []string{"21", " ２１\n"} {
			if !Matches(answer, c) {
				t.Errorf("valid answer rejected: %q", answer)
			}
		}
		for _, answer := range []string{
			"", "根据手感区分", "凭手感选择形状，答案是28颗", "121", "210", "21.5", "-21",
			"不是21，而是27", "21不够，至少27颗", "最少21颗；更正：最少27颗", "先考虑21，最终答案24",
			"答案21", "**21**", "21颗", "{\"answer\":21,\"minimum\":27}",
		} {
			if Matches(answer, c) {
				t.Errorf("non-numeric or wrong answer accepted: %q", answer)
			}
		}
	}
}

func TestCandyPromptContractAcrossProtocols(t *testing.T) {
	for _, base := range []Config{DefaultConfig(1), legacyCandyConfig()} {
		for _, protocol := range []string{"responses", "chat_completions", "messages"} {
			c := base
			c.Protocol = protocol
			_, raw, err := ProbeRequest(c)
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			prompt, _ := body["input"].(string)
			if protocol != "responses" {
				prompt = body["messages"].([]any)[0].(map[string]any)["content"].(string)
			}
			if prompt != c.Prompt+candyAnswerInstruction {
				t.Errorf("%s did not request the numeric answer contract", protocol)
			}
			if strings.Contains(prompt, "21") {
				t.Errorf("%s leaks the expected answer into its prompt", protocol)
			}
		}
	}
}

func TestCandyDoesNotRewriteCustomMatching(t *testing.T) {
	c := legacyCandyConfig()
	c.Prompt = "自定义题目"
	if !Matches("根据手感区分", c) || !Matches("答案21", c) {
		t.Fatal("custom contains_any rule changed")
	}
	if probePrompt(c) != c.Prompt {
		t.Fatal("custom prompt changed")
	}
	c.MatchMode = "exact"
	c.Expected = []string{"21"}
	if !Matches("２１", c) || Matches("答案21", c) {
		t.Fatal("custom exact rule changed")
	}
	c = legacyCandyConfig()
	c.Expected = []string{"27"}
	if !Matches("答案27", c) || probePrompt(c) != c.Prompt {
		t.Fatal("explicit custom candy policy changed")
	}
}
