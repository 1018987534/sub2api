//go:build unit

package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestPublicSiteLogoURL(t *testing.T) {
	for _, logo := range []string{"", "/logo.png", "https://example.com/logo.png", "data:image/png;base64,invalid!", "data:text/html;base64,aGk=", "data:image/png;base64,", "data:image/png,abc"} {
		require.Equal(t, logo, PublicSiteLogoURL(logo))
	}
	logo := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("fixture image"))
	asset := parseSiteLogoAsset(logo)
	require.NotNil(t, asset)
	require.Equal(t, []byte("fixture image"), asset.Data)
	require.Equal(t, "image/png", asset.ContentType)
	require.Equal(t, "/api/v1/settings/logo/"+asset.Version, PublicSiteLogoURL(logo))
	require.NotEqual(t, PublicSiteLogoURL(logo), PublicSiteLogoURL("data:image/png;base64,bmV3"))
}

func TestSettingService_LogoInjectionPreservesStoredValue(t *testing.T) {
	logo := "data:image/png;base64,aGVsbG8="
	r := &settingPublicRepoStub{values: map[string]string{SettingKeySiteLogo: logo}}
	s := NewSettingService(r, &config.Config{})
	settings, err := s.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, logo, settings.SiteLogo)
	payload, err := s.GetPublicSettingsForInjection(context.Background())
	require.NoError(t, err)
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	require.Contains(t, string(raw), PublicSiteLogoURL(logo))
	require.NotContains(t, string(raw), "data:image")
	require.Equal(t, logo, r.values[SettingKeySiteLogo])
}
