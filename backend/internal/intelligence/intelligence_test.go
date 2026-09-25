package intelligence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIntelligenceDefaultAndValidation(t *testing.T) {
	c := DefaultConfig(7)
	require.NoError(t, c.Validate())
	require.False(t, c.Enabled)
	require.Equal(t, 10, c.IntervalMinutes)
	require.Equal(t, "gpt-6-astra", c.Model)
	require.Contains(t, c.Prompt, "\n")
	for name, mutate := range map[string]func(*Config){
		"group":               func(c *Config) { c.GroupID = 0 },
		"enabled without key": func(c *Config) { c.Enabled = true },
		"interval":            func(c *Config) { c.IntervalMinutes = 0 },
		"timeout":             func(c *Config) { c.TimeoutSeconds = 901 },
		"model":               func(c *Config) { c.Model = " " },
		"prompt bytes":        func(c *Config) { c.Prompt = strings.Repeat("糖", 11000) },
		"protocol":            func(c *Config) { c.Protocol = "arbitrary-url" },
		"effort":              func(c *Config) { c.ReasoningEffort = "invalid" },
		"rule":                func(c *Config) { c.MatchMode = "regex" },
		"empty keyword":       func(c *Config) { c.Expected = []string{" "} },
	} {
		t.Run(name, func(t *testing.T) {
			bad := DefaultConfig(7)
			mutate(&bad)
			require.ErrorIs(t, bad.Validate(), ErrInvalid)
		})
	}
}
func TestIntelligenceFPHeuristic(t *testing.T) {
	c := DefaultConfig(1)
	for _, answer := range []string{"根据手感区分", "答案 21", "２１"} {
		require.True(t, Matches(answer, c), answer)
	}
	for _, answer := range []string{"", "29", "不知道"} {
		require.False(t, Matches(answer, c), answer)
	}
	c.MatchMode = "exact"
	require.True(t, Matches(" ２１ ", c))
	require.False(t, Matches("答案21", c))
}
func TestIntelligenceStrictResponseParsing(t *testing.T) {
	for _, tc := range []struct {
		protocol, raw string
		valid         bool
	}{
		{"responses", `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"21"}]}]}`, true},
		{"responses", `{"status":"incomplete","output":[{"type":"message","content":[{"type":"output_text","text":"21"}]}]}`, false},
		{"responses", `{"status":"completed","output":[]}`, false},
		{"chat_completions", `{"choices":[{"finish_reason":"stop","message":{"content":"21"}}]}`, true},
		{"chat_completions", `{"choices":[{"finish_reason":"length","message":{"content":"21"}}]}`, false},
		{"messages", `{"stop_reason":"end_turn","content":[{"type":"text","text":"21"}]}`, true},
		{"messages", `{"stop_reason":"max_tokens","content":[{"type":"text","text":"21"}]}`, false},
	} {
		text, err := parseJSON([]byte(tc.raw), tc.protocol)
		if tc.valid {
			require.NoError(t, err)
			require.Equal(t, "21", text)
		} else {
			require.Error(t, err)
		}
	}
	completed := "event: response.completed\ndata: " + `{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"21"}]}]}}` + "\n\n"
	answer, err := parseSSE(strings.NewReader(completed), 4096)
	require.NoError(t, err)
	require.Equal(t, "21", answer)
	for _, raw := range []string{
		"data: [DONE]\n\n", "data: " + `{"type":"response.output_text.delta","delta":"21"}` + "\n\n",
		"data: " + `{"type":"response.failed"}` + "\n\n", "data: invalid\n\n", strings.Repeat("x", 5000),
	} {
		_, err := parseSSE(strings.NewReader(raw), 4096)
		require.Error(t, err)
	}
}
func TestIntelligenceHTTPProtocolsAndRedaction(t *testing.T) {
	for _, protocol := range []string{"responses", "chat_completions", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			var calls atomic.Int32
			key := "sk-private-probe-key"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				require.Equal(t, "POST", r.Method)
				require.Equal(t, "Bearer "+key, r.Header.Get("Authorization"))
				var body map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				require.Equal(t, "real-model", body["model"])
				switch protocol {
				case "responses":
					require.Equal(t, "/v1/responses", r.URL.Path)
					require.Equal(t, true, body["stream"])
					require.Equal(t, false, body["store"])
					require.Equal(t, "high", body["reasoning"].(map[string]any)["effort"])
					require.Equal(t, "custom prompt", body["input"])
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte("data: " + `{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"21 sk-private-probe-key"}]}]}}` + "\n\n"))
				case "chat_completions":
					require.Equal(t, "/v1/chat/completions", r.URL.Path)
					require.Equal(t, "high", body["reasoning_effort"])
					_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"21 sk-private-probe-key"}}]}`))
				case "messages":
					require.Equal(t, "/v1/messages", r.URL.Path)
					require.Equal(t, key, r.Header.Get("x-api-key"))
					require.Nil(t, body["reasoning_effort"])
					_, _ = w.Write([]byte(`{"stop_reason":"end_turn","content":[{"type":"text","text":"21 sk-private-probe-key"}]}`))
				}
			}))
			defer server.Close()
			p := HTTPProbe{Endpoint: server.URL, Resolve: func(context.Context, Config) (string, error) { return key, nil }}
			c := DefaultConfig(1)
			c.Protocol = protocol
			c.Model = "real-model"
			c.ReasoningEffort = "high"
			c.Prompt = "custom prompt"
			record := p.Run(context.Background(), c)
			require.Equal(t, "normal", record.Status)
			require.NotContains(t, record.Answer, key)
			require.Contains(t, record.Answer, "[REDACTED]")
			require.EqualValues(t, 1, calls.Load())
		})
	}
}
func TestIntelligenceHTTPFailClosedNoRetry(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		body string
	}{
		{"gateway error", 500, "secret response body"},
		{"redirect", 302, ""},
		{"partial", 200, `{"status":"incomplete","output":[]}`},
		{"oversized", 200, strings.Repeat("x", 2*1024*1024+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer s.Close()
			p := HTTPProbe{Endpoint: s.URL, Resolve: func(context.Context, Config) (string, error) { return "secret-key", nil }}
			r := p.Run(context.Background(), DefaultConfig(1))
			require.Equal(t, "error", r.Status)
			require.Empty(t, r.Answer)
			require.NotContains(t, r.Error, "secret")
			require.EqualValues(t, 1, calls.Load())
		})
	}
	p := HTTPProbe{Endpoint: "http://127.0.0.1:1", Resolve: func(context.Context, Config) (string, error) { return "", nil }}
	require.Equal(t, "error", p.Run(context.Background(), DefaultConfig(1)).Status)
	p.Resolve = func(context.Context, Config) (string, error) { return "", errors.New("secret raw credential failure") }
	require.NotContains(t, p.Run(context.Background(), DefaultConfig(1)).Error, "secret")
}
func TestIntelligenceCancellation(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	p := HTTPProbe{Endpoint: server.URL, Resolve: func(context.Context, Config) (string, error) { return "key", nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	r := p.Run(ctx, DefaultConfig(1))
	require.Equal(t, "error", r.Status)
	require.Contains(t, r.Error, "取消")
}

type runnerStore struct {
	Store
	claims   atomic.Int32
	finishes atomic.Int32
}

func (s *runnerStore) Claim(_ context.Context, id int64, manual bool) (*Claim, error) {
	s.claims.Add(1)
	return &Claim{Config: DefaultConfig(id), Token: "fence"}, nil
}
func (s *runnerStore) Finish(context.Context, *Claim, Record) error { s.finishes.Add(1); return nil }

type blockingProbe struct{}

func (blockingProbe) Run(ctx context.Context, c Config) Record {
	<-ctx.Done()
	return Record{GroupID: c.GroupID, Status: "error", CheckedAt: time.Now()}
}
func TestIntelligenceRunnerBoundedAndStops(t *testing.T) {
	s := &runnerStore{}
	r := NewRunner(s, blockingProbe{}, func(context.Context) bool { return true })
	for i := int64(1); i <= 4; i++ {
		require.NoError(t, r.RunNow(context.Background(), i))
	}
	require.ErrorIs(t, r.RunNow(context.Background(), 5), ErrConflict)
	require.EqualValues(t, 4, s.claims.Load())
	r.Stop()
	require.EqualValues(t, 4, s.finishes.Load())
	require.ErrorIs(t, r.RunNow(context.Background(), 1), context.Canceled)
}
func TestIntelligenceRunnerDisabled(t *testing.T) {
	s := &runnerStore{}
	r := NewRunner(s, blockingProbe{}, func(context.Context) bool { return false })
	defer r.Stop()
	require.Error(t, r.RunNow(context.Background(), 1))
	require.Zero(t, s.claims.Load())
}
