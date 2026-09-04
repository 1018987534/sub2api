package service

import (
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

var upstreamModelNotFoundKeywords = []string{"model not found", "unknown model", "not found"}

func isUpstreamModelNotFoundError(statusCode int, body []byte) bool {
	if statusCode != http.StatusNotFound && statusCode != http.StatusBadRequest {
		return false
	}
	if statusCode == http.StatusBadRequest {
		code := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "error.code").String()))
		param := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "error.param").String()))
		if code != "model_not_found" || (param != "" && param != "model") {
			return false
		}
	}
	normalized := normalizeModelNotFoundBody(body)
	if normalized == "" || !strings.Contains(normalized, "model") {
		return false
	}
	return containsModelNotFoundKeyword(normalized)
}

func isModelNotFoundError(statusCode int, body []byte) bool {
	return isUpstreamModelNotFoundError(statusCode, body) || statusCode == http.StatusNotFound
}

// isOpenAIExplicitModelNotFoundBody detects a structured model-routing error
// even when an intermediary incorrectly wraps it in a 5xx. It only guards
// same-account transient retries; normal account failover still handles it.
func isOpenAIExplicitModelNotFoundBody(body []byte) bool {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	for _, path := range []string{"error.code", "response.error.code", "code"} {
		if strings.EqualFold(strings.TrimSpace(gjson.GetBytes(body, path).String()), "model_not_found") {
			return true
		}
	}
	return false
}

// openAICodexPlanGatedModelPhrase matches the deterministic Codex 400 returned
// when a ChatGPT OAuth account's plan cannot serve the requested model, e.g.
// {"detail":"The 'gpt-5.6-sol' model is not supported when using Codex with a ChatGPT account."}
// The phrase is compared against the normalized body (lowercased, "_"/"-"
// folded to spaces), so it also matches the same message embedded in
// error.message-style payloads.
const openAICodexPlanGatedModelPhrase = "model is not supported when using codex"

// isOpenAICodexPlanGatedModelError reports whether the upstream response is the
// deterministic Codex rejection of a plan-gated model on a ChatGPT account.
// Unlike transient failures, retrying the same account cannot succeed until the
// account's plan changes, so callers should treat it like model-not-found and
// cool the (account, model) pair down instead of re-selecting the account.
func isOpenAICodexPlanGatedModelError(statusCode int, body []byte) bool {
	if statusCode != http.StatusBadRequest {
		return false
	}
	normalized := normalizeModelNotFoundBody(body)
	if normalized == "" {
		return false
	}
	return strings.Contains(normalized, openAICodexPlanGatedModelPhrase)
}

func containsModelNotFoundKeyword(normalizedBody string) bool {
	if normalizedBody == "" {
		return false
	}
	for _, keyword := range upstreamModelNotFoundKeywords {
		if strings.Contains(normalizedBody, keyword) {
			return true
		}
	}
	return false
}

func normalizeModelNotFoundBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	normalized := strings.ToLower(string(body))
	normalized = strings.NewReplacer("_", " ", "-", " ", "\n", " ", "\r", " ", "\t", " ").Replace(normalized)
	return strings.Join(strings.Fields(normalized), " ")
}
