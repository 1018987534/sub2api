package handler

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/intelligence"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

type intelligenceRoutingProvider interface {
	GetPublicSettings(context.Context) (*service.PublicSettings, error)
}

func intelligenceProbeEndpoint(cfg *config.Config, settings intelligenceRoutingProvider) func(context.Context, intelligence.Config) (string, error) {
	return func(ctx context.Context, probe intelligence.Config) (string, error) {
		if cfg == nil {
			return "", errors.New("probe deployment configuration is unavailable")
		}
		// A configured runtime token identifies the multi-node deployment. Only
		// protocols served by its gateways participate in weighted routing.
		if strings.TrimSpace(cfg.GatewayRoutingRuntimeToken) == "" || (probe.Protocol != "responses" && probe.Protocol != "chat_completions") {
			return fmt.Sprintf("http://127.0.0.1:%d", cfg.Server.Port), nil
		}
		if settings == nil {
			return "", errors.New("probe routing is unavailable")
		}
		public, err := settings.GetPublicSettings(ctx)
		if err != nil || public == nil {
			return "", errors.New("probe routing is unavailable")
		}
		// Reuse the administrator's public API URL so probes pass through the
		// same Worker as clients. Origin hosts intentionally reject direct calls.
		origin, err := url.Parse(strings.TrimSpace(public.APIBaseURL))
		if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil ||
			origin.RawQuery != "" || origin.Fragment != "" || origin.RawPath != "" || urlvalidator.IsBlockedHost(origin.Hostname()) {
			return "", errors.New("invalid public probe API URL")
		}
		switch origin.Path {
		case "", "/", "/v1", "/v1/":
			origin.Path = ""
		default:
			return "", errors.New("unsupported public probe API path")
		}
		return origin.String(), nil
	}
}
