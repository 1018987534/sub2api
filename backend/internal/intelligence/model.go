// Package intelligence owns the optional V2 probe. It does not import official
// monitor implementations, and stores no credentials.
package intelligence

import (
	"errors"
	"golang.org/x/text/unicode/norm"
	"strings"
	"time"
)

var ErrConflict = errors.New("configuration changed or probe already running")
var ErrInvalid = errors.New("invalid intelligence check configuration")

const DefaultPrompt = "不使用任何外部工具回答以下问题：在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目，那么，最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）\n         苹果味 桃子味 西瓜味\n圆形     7      9      8\n五角星形 7      6      4"

type Config struct {
	GroupID         int64    `json:"group_id"`
	Version         int      `json:"version"`
	Enabled         bool     `json:"enabled"`
	IntervalMinutes int      `json:"interval_minutes"`
	Model           string   `json:"model"`
	ReasoningEffort string   `json:"reasoning_effort"`
	Protocol        string   `json:"protocol"`
	Prompt          string   `json:"prompt"`
	TimeoutSeconds  int      `json:"timeout_seconds"`
	APIKeyID        int64    `json:"api_key_id"`
	MatchMode       string   `json:"match_mode"`
	Expected        []string `json:"expected"`
	UpdatedBy       int64    `json:"updated_by"`
}

type Record struct {
	ID         int64     `json:"id"`
	GroupID    int64     `json:"group_id"`
	CheckedAt  time.Time `json:"checked_at"`
	DurationMS int64     `json:"duration_ms"`
	Status     string    `json:"status"`
	Answer     string    `json:"answer,omitempty"`
	Error      string    `json:"error,omitempty"`
	Config     *Config   `json:"config,omitempty"`
}

type Claim struct {
	Config Config
	Token  string
}

func DefaultConfig(groupID int64) Config {
	return Config{GroupID: groupID, IntervalMinutes: 10, Model: "gpt-6-astra", ReasoningEffort: "low", Protocol: "responses", Prompt: DefaultPrompt, TimeoutSeconds: 900, MatchMode: "contains_any", Expected: []string{"手感", "21"}}
}

func (c Config) Validate() error {
	if c.GroupID <= 0 || c.Version < 0 || c.IntervalMinutes < 1 || c.IntervalMinutes > 1440 || c.TimeoutSeconds < 5 || c.TimeoutSeconds > 900 || len(c.Model) > 128 || strings.TrimSpace(c.Model) == "" || strings.TrimSpace(c.Prompt) == "" || len(c.Prompt) > 32768 || c.APIKeyID < 0 || (c.Enabled && c.APIKeyID == 0) {
		return ErrInvalid
	}
	if c.Protocol != "responses" && c.Protocol != "chat_completions" && c.Protocol != "messages" {
		return ErrInvalid
	}
	if c.ReasoningEffort != "low" && c.ReasoningEffort != "medium" && c.ReasoningEffort != "high" && c.ReasoningEffort != "xhigh" && c.ReasoningEffort != "none" {
		return ErrInvalid
	}
	if c.MatchMode != "contains_any" && c.MatchMode != "exact" {
		return ErrInvalid
	}
	if len(c.Expected) < 1 || len(c.Expected) > 16 {
		return ErrInvalid
	}
	for _, s := range c.Expected {
		if strings.TrimSpace(s) == "" || len(s) > 256 {
			return ErrInvalid
		}
	}
	return nil
}

// Matches ports FP CandyAnswerPolicy's NFKC + full-answer keyword rule by
// default. This is a configurable heuristic, NOT a model-identity/IQ assertion.
func Matches(answer string, c Config) bool {
	text := norm.NFKC.String(strings.TrimSpace(answer))
	for _, expected := range c.Expected {
		token := norm.NFKC.String(strings.TrimSpace(expected))
		if token != "" && ((c.MatchMode == "exact" && text == token) || (c.MatchMode == "contains_any" && strings.Contains(text, token))) {
			return true
		}
	}
	return false
}
