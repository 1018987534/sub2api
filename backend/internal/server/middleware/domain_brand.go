package middleware

import (
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// DomainBrandContext resolves the optional portal profile before handlers read
// public settings, groups, registration defaults, or payment plans.
func DomainBrandContext(settingService *service.SettingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !needsDomainBrandProfile(c.Request.URL.Path) {
			c.Next()
			return
		}
		profile, err := settingService.ResolveDomainBrandProfile(c.Request.Context(), c.Request.Host)
		if err != nil {
			// A malformed persisted optional setting must not take down gateway or
			// existing portal traffic. It falls back to the historical global mode.
			slog.Error("resolve domain brand profile", "host", c.Request.Host, "error", err)
		}
		c.Request = c.Request.WithContext(service.WithDomainBrandProfile(c.Request.Context(), profile))
		c.Next()
	}
}

func needsDomainBrandProfile(requestPath string) bool {
	portalAPIPrefixes := []string{
		"/api/v1/auth",
		"/api/v1/user",
		"/api/v1/keys",
		"/api/v1/groups/available",
		"/api/v1/channel-monitors",
		"/api/v1/payment",
		"/api/v1/settings/public",
	}
	for _, prefix := range portalAPIPrefixes {
		if requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/") {
			return true
		}
	}
	if strings.HasPrefix(requestPath, "/api/") {
		return false
	}

	gatewayPrefixes := []string{
		"/v1", "/v1beta", "/responses", "/models", "/messages",
		"/backend-api", "/chat", "/embeddings", "/images", "/videos",
		"/antigravity", "/alpha", "/health",
	}
	for _, prefix := range gatewayPrefixes {
		if requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/") {
			return false
		}
	}
	if strings.HasPrefix(requestPath, "/assets/") || filepath.Ext(requestPath) != "" {
		return false
	}
	return true
}
