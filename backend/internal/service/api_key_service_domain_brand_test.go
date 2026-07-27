//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type domainAPIKeyUserRepoStub struct {
	UserRepository
	user *User
}

func (r *domainAPIKeyUserRepoStub) GetByID(context.Context, int64) (*User, error) {
	return r.user, nil
}

type domainAPIKeyGroupRepoStub struct {
	GroupRepository
	groups   []Group
	getCalls int
}

func (r *domainAPIKeyGroupRepoStub) ListActive(context.Context) ([]Group, error) {
	return append([]Group(nil), r.groups...), nil
}

func (r *domainAPIKeyGroupRepoStub) GetByID(_ context.Context, id int64) (*Group, error) {
	r.getCalls++
	for i := range r.groups {
		if r.groups[i].ID == id {
			return &r.groups[i], nil
		}
	}
	return nil, ErrGroupNotFound
}

type domainAPIKeySubRepoStub struct{ UserSubscriptionRepository }

func (r *domainAPIKeySubRepoStub) ListActiveByUserID(context.Context, int64) ([]UserSubscription, error) {
	return []UserSubscription{}, nil
}

type domainAPIKeyRepoStub struct {
	APIKeyRepository
	apiKey *APIKey
}

func (r *domainAPIKeyRepoStub) GetByID(context.Context, int64) (*APIKey, error) {
	return r.apiKey, nil
}

func TestAPIKeyService_DomainBrandScopesAvailableAndMutatedGroups(t *testing.T) {
	userRepo := &domainAPIKeyUserRepoStub{user: &User{ID: 10, Status: StatusActive}}
	groupRepo := &domainAPIKeyGroupRepoStub{groups: []Group{
		{ID: 5, Name: "C", Status: StatusActive},
		{ID: 79, Name: "B", Status: StatusActive},
	}}
	subRepo := &domainAPIKeySubRepoStub{}
	apiKeyRepo := &domainAPIKeyRepoStub{apiKey: &APIKey{ID: 100, UserID: 10}}
	svc := NewAPIKeyService(apiKeyRepo, userRepo, groupRepo, subRepo, nil, nil, &config.Config{})

	groups, err := svc.GetAvailableGroups(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, groups, 2)

	ctx := WithDomainBrandProfile(context.Background(), DomainBrandProfile{
		Configured: true, AllowedGroupIDs: []int64{79},
	})
	groups, err = svc.GetAvailableGroups(ctx, 10)
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Equal(t, int64(79), groups[0].ID)

	groupID := int64(5)
	_, err = svc.Create(ctx, 10, CreateAPIKeyRequest{Name: "cross-domain", GroupID: &groupID})
	require.ErrorIs(t, err, ErrGroupNotAllowed)
	require.Zero(t, groupRepo.getCalls, "domain rejection must happen before loading the forbidden group")

	_, err = svc.Update(ctx, 100, 10, UpdateAPIKeyRequest{GroupID: &groupID})
	require.ErrorIs(t, err, ErrGroupNotAllowed)
	require.Zero(t, groupRepo.getCalls, "domain rejection must happen before loading the forbidden group")
}

func TestAPIKeyService_LegacyPortalHostRejectsSecondBrandGroup(t *testing.T) {
	settingSvc := NewSettingService(&domainBrandSettingRepoStub{values: map[string]string{
		SettingKeyDomainBrandConfig: `{"domains":[{"domain":"xiaohondou.com","allowed_group_ids":[5]},{"domain":"xiaofanqie.org","allowed_group_ids":[92]}]}`,
	}}, &config.Config{})
	profile, err := settingSvc.ResolveDomainBrandProfile(context.Background(), "api.nideyiyi.com")
	require.NoError(t, err)
	require.True(t, profile.Configured)

	userRepo := &domainAPIKeyUserRepoStub{user: &User{ID: 10, Status: StatusActive}}
	groupRepo := &domainAPIKeyGroupRepoStub{groups: []Group{
		{ID: 5, Name: "C", Status: StatusActive},
		{ID: 92, Name: "B", Status: StatusActive},
	}}
	apiKeyRepo := &domainAPIKeyRepoStub{apiKey: &APIKey{ID: 100, UserID: 10}}
	svc := NewAPIKeyService(apiKeyRepo, userRepo, groupRepo, &domainAPIKeySubRepoStub{}, nil, nil, &config.Config{})
	ctx := WithDomainBrandProfile(context.Background(), profile)
	forbiddenGroupID := int64(92)

	_, err = svc.Create(ctx, 10, CreateAPIKeyRequest{Name: "cross-domain", GroupID: &forbiddenGroupID})
	require.ErrorIs(t, err, ErrGroupNotAllowed)
	_, err = svc.Update(ctx, 100, 10, UpdateAPIKeyRequest{GroupID: &forbiddenGroupID})
	require.ErrorIs(t, err, ErrGroupNotAllowed)
	require.Zero(t, groupRepo.getCalls, "legacy portal rejection must happen before loading the forbidden group")
}
