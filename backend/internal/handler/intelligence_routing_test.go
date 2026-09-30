package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/intelligence"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type intelligenceRoutingStub struct {
	public *service.PublicSettings
	err    error
}

func (s *intelligenceRoutingStub) GetPublicSettings(context.Context) (*service.PublicSettings, error) {
	return s.public, s.err
}

func TestIntelligenceProbeUsesPublicRouting(t *testing.T) {
	for _, protocol := range []string{"responses", "chat_completions"} {
		t.Run(protocol, func(t *testing.T) {
			routing := &intelligenceRoutingStub{public: &service.PublicSettings{APIBaseURL: "https://public.example/v1"}}
			probe := &intelligence.HTTPProbe{
				ResolveEndpoint: intelligenceProbeEndpoint(&config.Config{GatewayRoutingRuntimeToken: "configured"}, routing),
				Resolve:         func(context.Context, intelligence.Config) (string, error) { return "probe-key", nil },
			}
			calls := 0
			probe.Client = &http.Client{Transport: intelligenceRoutingTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "public.example", r.URL.Host)
				require.Equal(t, "Bearer probe-key", r.Header.Get("Authorization"))
				body := `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"21"}]}]}`
				if protocol == "chat_completions" {
					require.Equal(t, "/v1/chat/completions", r.URL.Path)
					body = `{"choices":[{"finish_reason":"stop","message":{"content":"21"}}]}`
				} else {
					require.Equal(t, "/v1/responses", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			c := intelligence.DefaultConfig(1)
			c.Protocol = protocol
			require.Equal(t, "normal", probe.Run(context.Background(), c).Status)
			require.Equal(t, 1, calls)
			// Missing or failed public configuration never silently calls control.
			routing.public.APIBaseURL = ""
			require.Equal(t, "error", probe.Run(context.Background(), c).Status)
			require.Equal(t, 1, calls)
			routing.err = errors.New("sensitive settings error")
			r := probe.Run(context.Background(), c)
			require.Equal(t, "error", r.Status)
			require.NotContains(t, r.Error, "sensitive")
			require.Equal(t, 1, calls)
		})
	}
}

func TestIntelligenceProbeRejectsInvalidPublicURL(t *testing.T) {
	for _, origin := range []string{"", "http://node.example", "https://user:secret@node.example", "https://node.example/path", "https://node.example?token=secret", "https://node.example/#fragment", "https://localhost", "https://127.0.0.1", "https://10.20.0.1", "https://node.example/%76%31"} {
		routing := &intelligenceRoutingStub{public: &service.PublicSettings{APIBaseURL: origin}}
		resolve := intelligenceProbeEndpoint(&config.Config{GatewayRoutingRuntimeToken: "configured"}, routing)
		_, err := resolve(context.Background(), intelligence.Config{Protocol: "responses"})
		require.Error(t, err, "URL=%s", origin)
	}
	for _, origin := range []string{"https://public.example", "https://public.example/", "https://public.example/v1", "https://public.example/v1/"} {
		resolve := intelligenceProbeEndpoint(&config.Config{GatewayRoutingRuntimeToken: "configured"}, &intelligenceRoutingStub{public: &service.PublicSettings{APIBaseURL: origin}})
		endpoint, err := resolve(context.Background(), intelligence.Config{Protocol: "responses"})
		require.NoError(t, err)
		require.Equal(t, "https://public.example", endpoint)
	}
}

func TestIntelligenceProbePreservesStandaloneAndMessages(t *testing.T) {
	for _, tc := range []struct{ token, protocol string }{{"", "responses"}, {"configured", "messages"}} {
		resolve := intelligenceProbeEndpoint(&config.Config{GatewayRoutingRuntimeToken: tc.token, Server: config.ServerConfig{Port: 8080}}, nil)
		endpoint, err := resolve(context.Background(), intelligence.Config{Protocol: tc.protocol})
		require.NoError(t, err)
		require.Equal(t, "http://127.0.0.1:8080", endpoint)
	}
}
