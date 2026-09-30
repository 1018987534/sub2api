//go:build unit

package handler

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSettingHandler_SiteLogo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	image := []byte("image fixture")
	logo := "data:image/png;base64," + base64.StdEncoding.EncodeToString(image)
	r := &settingHandlerPublicRepoStub{values: map[string]string{service.SettingKeySiteLogo: logo}}
	h := NewSettingHandler(service.NewSettingService(r, &config.Config{}), "fixture")
	version := strings.TrimPrefix(service.PublicSiteLogoURL(logo), "/api/v1/settings/logo/")
	request := func(method, version, etag string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(method, "/api/v1/settings/logo/"+version, nil)
		c.Request.Header.Set("If-None-Match", etag)
		c.Params = gin.Params{{Key: "version", Value: version}}
		h.GetSiteLogo(c)
		return rec
	}
	rec := request(http.MethodGet, version, "")
	require.Equal(t, 200, rec.Code)
	require.Equal(t, image, rec.Body.Bytes())
	require.Equal(t, "image/png", rec.Header().Get("Content-Type"))
	require.Equal(t, "public, max-age=31536000, immutable", rec.Header().Get("Cache-Control"))
	require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	require.Contains(t, rec.Header().Get("Content-Security-Policy"), "sandbox")
	for _, etag := range []string{rec.Header().Get("ETag"), "W/" + rec.Header().Get("ETag"), `"other", ` + rec.Header().Get("ETag"), "*"} {
		cached := request(http.MethodGet, version, etag)
		require.Equalf(t, 304, cached.Code, "etag=%q response=%q", etag, cached.Body.String())
		require.Empty(t, cached.Body.Bytes())
	}
	head := request(http.MethodHead, version, "")
	require.Equal(t, 200, head.Code)
	require.Empty(t, head.Body.Bytes())
	stale := request(http.MethodGet, "stale", "")
	require.Equal(t, 404, stale.Code)
	require.Equal(t, "no-store", stale.Header().Get("Cache-Control"))
	r.values[service.SettingKeySiteLogo] = "data:image/png;base64,bmV3"
	require.Equal(t, 404, request(http.MethodGet, version, "").Code)
	r.values[service.SettingKeySiteLogo] = "https://example.com/logo.png"
	require.Equal(t, 404, request(http.MethodGet, version, "").Code)
}
