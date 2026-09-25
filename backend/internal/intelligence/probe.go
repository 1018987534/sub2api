package intelligence

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type CredentialResolver func(context.Context, Config) (string, error)
type Prober interface {
	Run(context.Context, Config) Record
}

// Endpoint is set by the host bridge to the local gateway, never by user input.
// Credentials therefore cannot be forwarded to an arbitrary origin or redirect.
type HTTPProbe struct {
	Endpoint string
	Resolve  CredentialResolver
	Client   *http.Client
}

func (p *HTTPProbe) Run(parent context.Context, c Config) Record {
	start := time.Now()
	r := Record{GroupID: c.GroupID, CheckedAt: start.UTC(), Status: "error"}
	ctx, cancel := context.WithTimeout(parent, time.Duration(c.TimeoutSeconds)*time.Second)
	defer cancel()
	answer, err := p.request(ctx, c)
	r.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		r.Error = err.Error()
		return r
	}
	r.Answer = answer
	r.Status = "degraded"
	if Matches(answer, c) {
		r.Status = "normal"
	}
	return r
}

func (p *HTTPProbe) request(ctx context.Context, c Config) (string, error) {
	key, err := p.Resolve(ctx, c)
	if err != nil || strings.TrimSpace(key) == "" {
		return "", errors.New("专用 API Key 不可用、已失效或分组/所有者不匹配")
	}
	payload := map[string]any{"model": c.Model}
	path := "/v1/responses"
	switch c.Protocol {
	case "responses":
		payload["input"] = c.Prompt
		payload["stream"] = true
		payload["store"] = false
		if c.ReasoningEffort != "none" {
			payload["reasoning"] = map[string]string{"effort": c.ReasoningEffort}
		}
	case "chat_completions":
		path = "/v1/chat/completions"
		payload["messages"] = []map[string]string{{"role": "user", "content": c.Prompt}}
		payload["stream"] = false
		if c.ReasoningEffort != "none" {
			payload["reasoning_effort"] = c.ReasoningEffort
		}
	case "messages":
		path = "/v1/messages"
		payload["messages"] = []map[string]string{{"role": "user", "content": c.Prompt}}
		payload["max_tokens"] = 4096
	default:
		return "", ErrInvalid
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", errors.New("无法编码检测请求")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint+path, bytes.NewReader(raw))
	if err != nil {
		return "", errors.New("无法构造检测请求")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	if c.Protocol == "messages" {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	client := p.Client
	if client == nil {
		client = &http.Client{}
	}
	safeClient := *client
	safeClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := safeClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", errors.New("检测超时或已取消")
		}
		return "", errors.New("检测网络错误")
	}
	defer res.Body.Close()
	// Never persist response headers, bearer credentials or raw gateway error bodies.
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("检测 HTTP %d", res.StatusCode)
	}
	const maxBytes = 2 * 1024 * 1024
	reader := io.LimitReader(res.Body, maxBytes+1)
	var answer string
	if strings.Contains(res.Header.Get("Content-Type"), "text/event-stream") {
		answer, err = parseSSE(reader, maxBytes)
	} else {
		var data []byte
		data, err = io.ReadAll(reader)
		if len(data) > maxBytes {
			return "", errors.New("检测响应过大")
		}
		if err == nil {
			answer, err = parseJSON(data, c.Protocol)
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return "", errors.New("检测超时或已取消")
		}
		return "", errors.New("检测响应不完整、为空或格式无效")
	}
	answer = strings.ReplaceAll(answer, key, "[REDACTED]")
	if len(answer) > 65536 {
		return "", errors.New("检测回答超过存储上限")
	}
	return answer, nil
}

func responseText(raw json.RawMessage) (string, error) {
	var data struct {
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", err
	}
	if data.Status != "completed" {
		return "", errors.New("incomplete response")
	}
	var parts []string
	for _, o := range data.Output {
		if o.Type == "message" {
			for _, c := range o.Content {
				if c.Type == "output_text" {
					parts = append(parts, c.Text)
				}
			}
		}
	}
	text := strings.TrimSpace(strings.Join(parts, "\n"))
	if text == "" {
		return "", errors.New("empty response")
	}
	return text, nil
}

func parseJSON(raw []byte, protocol string) (string, error) {
	if protocol == "responses" {
		return responseText(raw)
	}
	var data struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", err
	}
	var text string
	if protocol == "chat_completions" && len(data.Choices) > 0 && data.Choices[0].FinishReason == "stop" {
		text = data.Choices[0].Message.Content
	}
	if protocol == "messages" && (data.StopReason == "end_turn" || data.StopReason == "stop_sequence") {
		for _, c := range data.Content {
			if c.Type == "text" {
				text += c.Text
			}
		}
	}
	if strings.TrimSpace(text) == "" {
		return "", errors.New("empty or incomplete response")
	}
	return text, nil
}

func parseSSE(reader io.Reader, maxBytes int) (string, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maxBytes)
	var data []string
	total := 0
	consume := func() (string, bool, error) {
		if len(data) == 0 {
			return "", false, nil
		}
		raw := strings.Join(data, "\n")
		data = nil
		if raw == "[DONE]" {
			return "", false, nil
		}
		var event struct {
			Type     string          `json:"type"`
			Response json.RawMessage `json:"response"`
		}
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return "", false, err
		}
		if event.Type == "response.completed" {
			a, e := responseText(event.Response)
			return a, true, e
		}
		if event.Type == "response.failed" || event.Type == "response.incomplete" || event.Type == "error" {
			return "", false, errors.New("failed stream")
		}
		return "", false, nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		total += len(line) + 1
		if total > maxBytes {
			return "", errors.New("oversized stream")
		}
		if line == "" {
			a, done, err := consume()
			if done || err != nil {
				return a, err
			}
		} else if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	a, done, err := consume()
	if done || err != nil {
		return a, err
	}
	return "", errors.New("missing completed event")
}
