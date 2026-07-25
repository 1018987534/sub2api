//go:build unit

package service

import (
	"context"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type domainBrandSettingRepoStub struct {
	SettingRepository
	values map[string]string
}

func (r *domainBrandSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (r *domainBrandSettingRepoStub) Set(_ context.Context, key, value string) error {
	if r.values == nil {
		r.values = map[string]string{}
	}
	r.values[key] = value
	return nil
}

type domainBrandGroupReaderStub struct {
	groups map[int64]*Group
}

func (r *domainBrandGroupReaderStub) GetByID(_ context.Context, id int64) (*Group, error) {
	group, ok := r.groups[id]
	if !ok {
		return nil, ErrGroupNotFound
	}
	return group, nil
}

func stringPointer(value string) *string { return &value }

func TestNormalizeDomainHost(t *testing.T) {
	require.Equal(t, "xiaofanqie.org", NormalizeDomainHost("XIAOFANQIE.ORG.:443"))
	require.Equal(t, "xiaohondou.com", NormalizeDomainHost(" xiaohondou.com "))
	require.Empty(t, NormalizeDomainHost("https://xiaohondou.com"))
}

func TestSettingService_UpdateAndResolveDomainBrandConfig(t *testing.T) {
	repo := &domainBrandSettingRepoStub{values: map[string]string{}}
	svc := NewSettingService(repo, &config.Config{})
	svc.SetDefaultSubscriptionGroupReader(&domainBrandGroupReaderStub{groups: map[int64]*Group{
		5:  {ID: 5, Status: StatusActive},
		79: {ID: 79, Status: StatusActive},
	}})
	callbackCalls := 0
	svc.SetOnUpdateCallback(func() { callbackCalls++ })

	saved, err := svc.UpdateDomainBrandConfig(context.Background(), &DomainBrandConfig{Domains: []DomainBrandProfile{
		{Domain: "XIAOHONDOU.COM.", AllowedGroupIDs: []int64{5, 79}},
		{
			Domain:          "xiaofanqie.org",
			SiteLogo:        stringPointer(""),
			ContactInfo:     stringPointer("support@example.com"),
			APIBaseURL:      stringPointer("https://xiaofanqie.org/v1"),
			AllowedGroupIDs: []int64{},
		},
	}})
	require.NoError(t, err)
	require.Equal(t, "xiaohondou.com", saved.Domains[0].Domain)
	require.Equal(t, 1, callbackCalls)

	profile, err := svc.ResolveDomainBrandProfile(context.Background(), "XIAOFANQIE.ORG:443")
	require.NoError(t, err)
	require.True(t, profile.Configured)
	require.NotNil(t, profile.SiteLogo)
	require.Empty(t, *profile.SiteLogo)
	require.Equal(t, "support@example.com", *profile.ContactInfo)
	require.Equal(t, "https://xiaofanqie.org/v1", *profile.APIBaseURL)
	require.False(t, profile.AllowsGroup(5))

	fallback, err := svc.ResolveDomainBrandProfile(context.Background(), "unknown.example")
	require.NoError(t, err)
	require.False(t, fallback.Configured)
	require.True(t, fallback.AllowsGroup(5))
}

func TestSettingService_RejectsCrossDomainGroupReuse(t *testing.T) {
	svc := NewSettingService(&domainBrandSettingRepoStub{values: map[string]string{}}, &config.Config{})
	svc.SetDefaultSubscriptionGroupReader(&domainBrandGroupReaderStub{groups: map[int64]*Group{
		5: {ID: 5, Status: StatusActive},
	}})

	_, err := svc.UpdateDomainBrandConfig(context.Background(), &DomainBrandConfig{Domains: []DomainBrandProfile{
		{Domain: "a.example", AllowedGroupIDs: []int64{5}},
		{Domain: "b.example", AllowedGroupIDs: []int64{5}},
	}})
	require.ErrorIs(t, err, ErrDomainBrandConfigInvalid)
}

func TestSettingService_PublicSettingsOverlayPreservesExplicitEmptyLogo(t *testing.T) {
	repo := &settingPublicRepoStub{values: map[string]string{
		SettingKeySiteName:     "Global Name",
		SettingKeySiteLogo:     "global-logo",
		SettingKeySiteSubtitle: "Global Subtitle",
		SettingKeyContactInfo:  "global-support@example.com",
		SettingKeyAPIBaseURL:   "https://xiaohondou.com/v1",
	}}
	svc := NewSettingService(repo, &config.Config{})
	ctx := WithDomainBrandProfile(context.Background(), DomainBrandProfile{
		Configured:   true,
		SiteName:     stringPointer("xiaofanqie.org"),
		SiteLogo:     stringPointer(""),
		SiteSubtitle: stringPointer("B-end API Gateway"),
		ContactInfo:  stringPointer("b2b-support@example.com"),
		APIBaseURL:   stringPointer("https://xiaofanqie.org/v1"),
	})

	settings, err := svc.GetPublicSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, "xiaofanqie.org", settings.SiteName)
	require.Empty(t, settings.SiteLogo)
	require.Equal(t, "B-end API Gateway", settings.SiteSubtitle)
	require.Equal(t, "b2b-support@example.com", settings.ContactInfo)
	require.Equal(t, "https://xiaofanqie.org/v1", settings.APIBaseURL)
}

func TestSettingService_PublicSettingsOverlayInheritsOmittedFields(t *testing.T) {
	repo := &settingPublicRepoStub{values: map[string]string{
		SettingKeyContactInfo: "global-support@example.com",
		SettingKeyAPIBaseURL:  "https://xiaohondou.com/v1",
	}}
	svc := NewSettingService(repo, &config.Config{})
	ctx := WithDomainBrandProfile(context.Background(), DomainBrandProfile{Configured: true})

	settings, err := svc.GetPublicSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, "global-support@example.com", settings.ContactInfo)
	require.Equal(t, "https://xiaohondou.com/v1", settings.APIBaseURL)
}

func TestSettingService_GetRegistrationSiteNameSnapshotsEffectiveBrand(t *testing.T) {
	repo := &settingRepoStub{values: map[string]string{
		SettingKeySiteName: "小红豆",
	}}
	svc := NewSettingService(repo, &config.Config{})

	primary := svc.GetRegistrationSiteName(context.Background())
	require.Equal(t, "小红豆", primary)

	ctx := WithDomainBrandProfile(context.Background(), DomainBrandProfile{
		Configured: true,
		SiteName:   stringPointer("小番茄"),
	})
	require.Equal(t, "小番茄", svc.GetRegistrationSiteName(ctx))
}

func TestPaymentConfigService_FilterPlansForDomain(t *testing.T) {
	plans := []*dbent.SubscriptionPlan{{ID: 1, GroupID: 5}, {ID: 2, GroupID: 79}}
	service := &PaymentConfigService{}

	require.Len(t, service.FilterPlansForDomain(context.Background(), plans), 2)
	ctx := WithDomainBrandProfile(context.Background(), DomainBrandProfile{Configured: true, AllowedGroupIDs: []int64{79}})
	filtered := service.FilterPlansForDomain(ctx, plans)
	require.Len(t, filtered, 1)
	require.Equal(t, int64(79), filtered[0].GroupID)
}
