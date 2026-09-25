package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/intelligence"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type intelligencePickerKeys struct {
	service.APIKeyRepository
	items   []service.APIKey
	owner   int64
	params  pagination.PaginationParams
	filters service.APIKeyListFilters
	err     error
}

func (r *intelligencePickerKeys) ListByUserID(_ context.Context, owner int64, p pagination.PaginationParams, f service.APIKeyListFilters) ([]service.APIKey, *pagination.PaginationResult, error) {
	r.owner, r.params, r.filters = owner, p, f
	return r.items, &pagination.PaginationResult{Total: 101}, r.err
}

type intelligencePickerGroups struct {
	service.GroupRepository
	group *service.Group
	err   error
}

func (r *intelligencePickerGroups) GetByID(context.Context, int64) (*service.Group, error) {
	return r.group, r.err
}

func intelligencePickerRequest(h *IntelligenceHandler, url, group, role string, authenticated bool) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", url, nil)
	c.Params = gin.Params{{Key: "groupID", Value: group}}
	if authenticated {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
	}
	c.Set(string(middleware.ContextKeyUserRole), role)
	h.KeyOptions(c)
	return w
}

func TestIntelligenceKeyOptionsOwnershipAndRedaction(t *testing.T) {
	group, other := int64(7), int64(8)
	keys := &intelligencePickerKeys{items: []service.APIKey{
		{ID: 12, UserID: 9, GroupID: &group, Name: "Probe", Key: "secret-probe-token", Status: service.StatusActive, Quota: 10, QuotaUsed: 3},
		{ID: 13, UserID: 8, GroupID: &group, Name: "Other owner", Key: "other-owner-secret"},
		{ID: 14, UserID: 9, GroupID: &other, Name: "Other group", Key: "other-group-secret"},
		{ID: 15, UserID: 9, Name: "Unbound", Key: "unbound-secret"},
	}}
	h := &IntelligenceHandler{keys: keys, groups: &intelligencePickerGroups{group: &service.Group{ID: group, Status: service.StatusActive}}}
	w := intelligencePickerRequest(h, "/keys?page=2&search=%20Probe%20", "7", service.RoleAdmin, true)
	require.Equal(t, 200, w.Code)
	require.EqualValues(t, 9, keys.owner)
	require.Equal(t, pagination.PaginationParams{Page: 2, PageSize: 50, SortBy: "id", SortOrder: "desc"}, keys.params)
	require.Equal(t, service.APIKeyListFilters{GroupID: &group, Search: "Probe"}, keys.filters)
	var result struct {
		Data struct {
			Items   []map[string]any
			Page    int
			HasMore bool `json:"has_more"`
		}
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Len(t, result.Data.Items, 1)
	require.Equal(t, map[string]any{"id": float64(12), "name": "Probe", "group_id": float64(7), "available": true, "quota_remaining": float64(7), "expires_at": nil}, result.Data.Items[0])
	require.Equal(t, 2, result.Data.Page)
	require.True(t, result.Data.HasMore)
	for _, secret := range []string{"secret", "Other owner", "Other group", "Unbound", "\"key\"", "user_id"} {
		require.NotContains(t, w.Body.String(), secret)
	}
}

func TestIntelligenceKeyOptionsUnavailableStates(t *testing.T) {
	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	for _, tc := range []struct {
		name, groupStatus, status, key string
		expiry                         *time.Time
		quota, used                    float64
		reason                         string
	}{
		{"active unlimited", "active", "active", "secret", &future, 0, 0, ""},
		{"inactive group", "inactive", "active", "secret", nil, 0, 0, "分组已停用"},
		{"missing credential", "active", "active", "", nil, 0, 0, "Key 不可用"},
		{"inactive key", "active", "inactive", "secret", nil, 0, 0, "Key 已停用或不可用"},
		{"expired", "active", "active", "secret", &past, 0, 0, "Key 已过期"},
		{"exhausted", "active", "active", "secret", nil, 10, 10, "Key 额度已用完"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			option := intelligenceKeyChoice(service.APIKey{ID: 12, Key: tc.key, Status: tc.status, ExpiresAt: tc.expiry, Quota: tc.quota, QuotaUsed: tc.used}, &service.Group{ID: 7, Status: tc.groupStatus})
			require.Equal(t, tc.reason, option.UnavailableReason)
			require.Equal(t, tc.reason == "", option.Available)
			if tc.quota == 0 {
				require.Equal(t, float64(-1), option.QuotaRemaining)
			}
		})
	}
}

func TestIntelligenceKeyOptionsRejectsInvalidRequests(t *testing.T) {
	for _, tc := range []struct {
		name, url, group, role string
		auth                   bool
		code                   int
	}{
		{"anonymous", "/keys", "7", "admin", false, 401},
		{"non admin", "/keys", "7", "user", true, 403},
		{"missing role", "/keys", "7", "", true, 403},
		{"invalid group", "/keys", "0", "admin", true, 400},
		{"negative page", "/keys?page=-1", "7", "admin", true, 400},
		{"invalid page", "/keys?page=no", "7", "admin", true, 400},
		{"oversized page", "/keys?page=100001", "7", "admin", true, 400},
		{"long search", "/keys?search=" + strings.Repeat("x", 101), "7", "admin", true, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &IntelligenceHandler{groups: &intelligencePickerGroups{group: &service.Group{ID: 7}}}
			w := intelligencePickerRequest(h, tc.url, tc.group, tc.role, tc.auth)
			require.Equal(t, tc.code, w.Code)
		})
	}
}

func TestIntelligenceKeyOptionsPaginationAndErrors(t *testing.T) {
	keys := &intelligencePickerKeys{}
	groups := &intelligencePickerGroups{group: &service.Group{ID: 7, Status: "active"}}
	h := &IntelligenceHandler{keys: keys, groups: groups}
	w := intelligencePickerRequest(h, "/keys", "7", "admin", true)
	require.Equal(t, 200, w.Code)
	require.Equal(t, 1, keys.params.Page)
	require.Contains(t, w.Body.String(), `"items":[]`)
	w = intelligencePickerRequest(h, "/keys?page=3", "7", "admin", true)
	require.Contains(t, w.Body.String(), `"has_more":false`)
	keys.err = errors.New("private database details")
	w = intelligencePickerRequest(h, "/keys", "7", "admin", true)
	require.Equal(t, 500, w.Code)
	require.NotContains(t, w.Body.String(), "private database")
	groups.group = nil
	w = intelligencePickerRequest(h, "/keys", "7", "admin", true)
	require.Equal(t, 404, w.Code)
}

type intelligenceSelectedKey struct {
	service.APIKeyRepository
	key *service.APIKey
}

func (r *intelligenceSelectedKey) GetByID(context.Context, int64) (*service.APIKey, error) {
	return r.key, nil
}

type intelligencePickerStore struct {
	intelligence.Store
	saved *intelligence.Config
}

func (s *intelligencePickerStore) Save(_ context.Context, c intelligence.Config) (intelligence.Config, error) {
	s.saved = &c
	c.Version++
	return c, nil
}

func TestIntelligenceSelectedKeySaveValidatesActualOwnerAndGroup(t *testing.T) {
	for _, tc := range []struct {
		name         string
		owner, group int64
		status       string
		code         int
	}{
		{"current admin key", 9, 7, service.StatusActive, 200},
		{"other admin key", 8, 7, service.StatusActive, 400},
		{"different group key", 9, 8, service.StatusActive, 400},
		{"revoked after listing", 9, 7, "inactive", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &intelligencePickerStore{}
			h := &IntelligenceHandler{store: store,
				keys:   &intelligenceSelectedKey{key: &service.APIKey{ID: 12, UserID: tc.owner, GroupID: &tc.group, Key: "never-return-this-secret", Status: tc.status}},
				groups: &intelligencePickerGroups{group: &service.Group{ID: 7, Status: service.StatusActive}},
			}
			input := intelligence.DefaultConfig(7)
			input.APIKeyID = 12
			input.UpdatedBy = 8 // The client cannot impersonate the key owner.
			payload, err := json.Marshal(input)
			require.NoError(t, err)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Params = gin.Params{{Key: "groupID", Value: "7"}}
			c.Request = httptest.NewRequest("PUT", "/7/config", strings.NewReader(string(payload)))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9})
			h.Save(c)
			require.Equal(t, tc.code, w.Code)
			require.NotContains(t, w.Body.String(), "never-return-this-secret")
			if tc.code == 200 {
				require.NotNil(t, store.saved)
				require.EqualValues(t, 9, store.saved.UpdatedBy)
				require.EqualValues(t, 12, store.saved.APIKeyID)
			} else {
				require.Nil(t, store.saved)
			}
		})
	}
}
