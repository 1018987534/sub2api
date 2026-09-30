package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"mime"
	"strings"
)

type SiteLogoAsset struct {
	ContentType string
	Data        []byte
	Version     string
}

// Only embedded images become assets; stored configuration and external URLs stay intact.
func parseSiteLogoAsset(logo string) *SiteLogoAsset {
	logo = strings.TrimSpace(logo)
	header, payload, ok := strings.Cut(logo, ",")
	if !ok || !strings.HasPrefix(header, "data:") || !strings.HasSuffix(header, ";base64") {
		return nil
	}
	contentType, _, err := mime.ParseMediaType(strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64"))
	if err != nil {
		return nil
	}
	switch contentType {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/avif", "image/svg+xml", "image/x-icon", "image/vnd.microsoft.icon":
	default:
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(data) == 0 {
		return nil
	}
	hash := sha256.Sum256([]byte(logo))
	return &SiteLogoAsset{ContentType: contentType, Data: data, Version: hex.EncodeToString(hash[:])}
}

func PublicSiteLogoURL(logo string) string {
	if asset := parseSiteLogoAsset(logo); asset != nil {
		return "/api/v1/settings/logo/" + asset.Version
	}
	return logo
}

func (s *SettingService) GetSiteLogoAsset(ctx context.Context) (*SiteLogoAsset, error) {
	values, err := s.settingRepo.GetMultiple(ctx, []string{SettingKeySiteLogo})
	if err != nil {
		return nil, err
	}
	return parseSiteLogoAsset(values[SettingKeySiteLogo]), nil
}
