package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrDomainBrandConfigInvalid = infraerrors.BadRequest("DOMAIN_BRAND_CONFIG_INVALID", "invalid domain brand configuration")

// DomainBrandConfig keeps the small amount of per-domain portal configuration.
// Domains not listed here deliberately retain all global settings and groups.
type DomainBrandConfig struct {
	Domains []DomainBrandProfile `json:"domains"`
}

// DomainBrandProfile is stored in domain_brand_config. Pointer display fields
// distinguish an omitted value (inherit the global setting) from an explicitly
// empty value (for example, use the packaged default logo).
type DomainBrandProfile struct {
	Domain          string  `json:"domain"`
	SiteName        *string `json:"site_name,omitempty"`
	SiteLogo        *string `json:"site_logo,omitempty"`
	SiteSubtitle    *string `json:"site_subtitle,omitempty"`
	AllowedGroupIDs []int64 `json:"allowed_group_ids"`
	Configured      bool    `json:"-"`
}

// AllowsGroup returns whether a portal operation may use groupID. An
// unconfigured host is intentionally unrestricted for backward compatibility.
func (p DomainBrandProfile) AllowsGroup(groupID int64) bool {
	if !p.Configured {
		return true
	}
	for _, allowedID := range p.AllowedGroupIDs {
		if allowedID == groupID {
			return true
		}
	}
	return false
}

type domainBrandContextKey struct{}

// WithDomainBrandProfile records the resolved domain profile for one request.
func WithDomainBrandProfile(ctx context.Context, profile DomainBrandProfile) context.Context {
	return context.WithValue(ctx, domainBrandContextKey{}, profile)
}

// DomainBrandProfileFromContext returns the profile selected by the request host.
func DomainBrandProfileFromContext(ctx context.Context) DomainBrandProfile {
	if ctx == nil {
		return DomainBrandProfile{}
	}
	profile, _ := ctx.Value(domainBrandContextKey{}).(DomainBrandProfile)
	return profile
}

// NormalizeDomainHost makes Request.Host and configured hostnames comparable.
func NormalizeDomainHost(raw string) string {
	host := strings.TrimSpace(strings.ToLower(raw))
	if host == "" || strings.ContainsAny(host, "/?#@") || strings.Contains(host, "://") {
		return ""
	}
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	}
	host = strings.Trim(strings.TrimSuffix(host, "."), "[]")
	if host == "" || strings.ContainsAny(host, " \t\r\n") {
		return ""
	}
	return host
}

// GetDomainBrandConfig reads the persisted configuration. An empty value is a
// valid disabled state rather than an error, so existing installations retain
// their exact historical behavior until an administrator adds a domain.
func (s *SettingService) GetDomainBrandConfig(ctx context.Context) (*DomainBrandConfig, error) {
	if s == nil || s.settingRepo == nil {
		return &DomainBrandConfig{Domains: []DomainBrandProfile{}}, nil
	}
	values, err := s.settingRepo.GetMultiple(ctx, []string{SettingKeyDomainBrandConfig})
	if err != nil {
		return nil, fmt.Errorf("get domain brand config: %w", err)
	}
	return parseDomainBrandConfig(values[SettingKeyDomainBrandConfig])
}

// ResolveDomainBrandProfile resolves a request host to its optional profile.
func (s *SettingService) ResolveDomainBrandProfile(ctx context.Context, host string) (DomainBrandProfile, error) {
	normalizedHost := NormalizeDomainHost(host)
	if normalizedHost == "" {
		return DomainBrandProfile{}, nil
	}
	config, err := s.GetDomainBrandConfig(ctx)
	if err != nil {
		return DomainBrandProfile{}, err
	}
	for _, profile := range config.Domains {
		if profile.Domain == normalizedHost {
			profile.Configured = true
			return profile, nil
		}
	}
	return DomainBrandProfile{}, nil
}

// UpdateDomainBrandConfig validates and persists the complete configuration in
// one write. Groups are exclusive across configured hosts, which makes pricing
// boundaries unambiguous and prevents an accidental cross-brand listing.
func (s *SettingService) UpdateDomainBrandConfig(ctx context.Context, config *DomainBrandConfig) (*DomainBrandConfig, error) {
	normalized, err := s.validateDomainBrandConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("marshal domain brand config: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyDomainBrandConfig, string(raw)); err != nil {
		return nil, fmt.Errorf("set domain brand config: %w", err)
	}
	if s.onUpdate != nil {
		s.onUpdate()
	}
	return normalized, nil
}

func parseDomainBrandConfig(raw string) (*DomainBrandConfig, error) {
	config := &DomainBrandConfig{Domains: []DomainBrandProfile{}}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return config, nil
	}
	if err := json.Unmarshal([]byte(trimmed), config); err != nil {
		return nil, fmt.Errorf("parse domain brand config: %w", err)
	}
	for i := range config.Domains {
		config.Domains[i].Domain = NormalizeDomainHost(config.Domains[i].Domain)
		if config.Domains[i].AllowedGroupIDs == nil {
			config.Domains[i].AllowedGroupIDs = []int64{}
		}
	}
	return config, nil
}

func (s *SettingService) validateDomainBrandConfig(ctx context.Context, config *DomainBrandConfig) (*DomainBrandConfig, error) {
	if config == nil {
		config = &DomainBrandConfig{}
	}
	normalized := &DomainBrandConfig{Domains: make([]DomainBrandProfile, 0, len(config.Domains))}
	domains := make(map[string]struct{}, len(config.Domains))
	groupOwners := make(map[int64]string)

	for _, input := range config.Domains {
		profile := input
		profile.Domain = NormalizeDomainHost(profile.Domain)
		profile.Configured = false
		if profile.Domain == "" {
			return nil, fmt.Errorf("%w: domain is required", ErrDomainBrandConfigInvalid)
		}
		if _, exists := domains[profile.Domain]; exists {
			return nil, fmt.Errorf("%w: duplicate domain %q", ErrDomainBrandConfigInvalid, profile.Domain)
		}
		domains[profile.Domain] = struct{}{}

		seenGroupIDs := make(map[int64]struct{}, len(profile.AllowedGroupIDs))
		profile.AllowedGroupIDs = make([]int64, 0, len(input.AllowedGroupIDs))
		for _, groupID := range input.AllowedGroupIDs {
			if groupID <= 0 {
				return nil, fmt.Errorf("%w: group id must be positive", ErrDomainBrandConfigInvalid)
			}
			if _, exists := seenGroupIDs[groupID]; exists {
				return nil, fmt.Errorf("%w: duplicate group %d for %s", ErrDomainBrandConfigInvalid, groupID, profile.Domain)
			}
			seenGroupIDs[groupID] = struct{}{}
			if owner, exists := groupOwners[groupID]; exists {
				return nil, fmt.Errorf("%w: group %d belongs to both %s and %s", ErrDomainBrandConfigInvalid, groupID, owner, profile.Domain)
			}
			if err := s.validateActiveDomainBrandGroup(ctx, groupID); err != nil {
				return nil, err
			}
			groupOwners[groupID] = profile.Domain
			profile.AllowedGroupIDs = append(profile.AllowedGroupIDs, groupID)
		}
		normalized.Domains = append(normalized.Domains, profile)
	}
	return normalized, nil
}

func (s *SettingService) validateActiveDomainBrandGroup(ctx context.Context, groupID int64) error {
	if s == nil || s.defaultSubGroupReader == nil {
		return fmt.Errorf("%w: group validation is unavailable", ErrDomainBrandConfigInvalid)
	}
	group, err := s.defaultSubGroupReader.GetByID(ctx, groupID)
	if err != nil {
		return fmt.Errorf("%w: get group %d: %v", ErrDomainBrandConfigInvalid, groupID, err)
	}
	if group.Status != StatusActive {
		return fmt.Errorf("%w: group %d is not active", ErrDomainBrandConfigInvalid, groupID)
	}
	return nil
}
