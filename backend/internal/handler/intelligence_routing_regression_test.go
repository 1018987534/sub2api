package handler

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/intelligence"
	"github.com/stretchr/testify/require"
)

type intelligenceRoutingTransport func(*http.Request) (*http.Response, error)

func (f intelligenceRoutingTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestIntelligenceProbeDoesNotBypassRouting(t *testing.T) {
	t.Setenv("INTELLIGENCE_CHECKS_DISABLE_RUNNER", "1")
	h := NewIntelligenceHandler(nil, &config.Config{
		GatewayRoutingRuntimeToken: "configured-routing-token",
		Server:                     config.ServerConfig{Port: 8080},
	}, nil, nil, nil, nil, nil)
	t.Cleanup(h.Stop)
	probe := h.runner.Probe.(*intelligence.HTTPProbe)
	probe.Resolve = func(context.Context, intelligence.Config) (string, error) { return "fixture-key", nil }
	calls := 0
	probe.Client = &http.Client{Transport: intelligenceRoutingTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		t.Logf("unexpected model request host=%s", r.URL.Host)
		return nil, errors.New("fixture has no network")
	})}
	c := intelligence.DefaultConfig(1)
	c.Protocol = "responses"
	result := probe.Run(context.Background(), c)
	t.Logf("runtime unavailable: model_requests=%d status=%s", calls, result.Status)
	require.Equal(t, 0, calls, "configured multi-node probes must not fall back to the local control")
	require.Equal(t, "error", result.Status)
}
