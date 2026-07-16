package admin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type manualMatchAffiliateRepoStub struct {
	service.AffiliateRepository
	summaries map[int64]*service.AffiliateSummary
	bindCalls []struct {
		inviteeID int64
		inviterID int64
		source    service.AffiliateBindingSource
	}
}

func (r *manualMatchAffiliateRepoStub) EnsureUserAffiliate(_ context.Context, userID int64) (*service.AffiliateSummary, error) {
	summary, ok := r.summaries[userID]
	if !ok {
		return nil, service.ErrAffiliateProfileNotFound
	}
	copy := *summary
	return &copy, nil
}

func (r *manualMatchAffiliateRepoStub) BindInviter(_ context.Context, inviteeID, inviterID int64, source service.AffiliateBindingSource) (bool, error) {
	r.bindCalls = append(r.bindCalls, struct {
		inviteeID int64
		inviterID int64
		source    service.AffiliateBindingSource
	}{inviteeID: inviteeID, inviterID: inviterID, source: source})
	return true, nil
}

func newManualMatchRouter(repo service.AffiliateRepository) *gin.Engine {
	gin.SetMode(gin.TestMode)
	adminService := &stubAdminService{users: []service.User{
		{ID: 10, Email: "inviter@example.com", Status: service.StatusActive},
		{ID: 21, Email: "customer@example.com", Status: service.StatusActive},
	}}
	handler := NewAffiliateHandler(service.NewAffiliateService(repo, nil, nil, nil), adminService)
	router := gin.New()
	router.POST("/api/v1/admin/affiliates/invites", handler.CreateInviteMatch)
	router.GET("/api/v1/admin/affiliates/users/lookup", handler.LookupUsers)
	return router
}

func TestAffiliateHandlerLookupUsersByID(t *testing.T) {
	t.Parallel()
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/affiliates/users/lookup?q=%2321", nil)

	newManualMatchRouter(&manualMatchAffiliateRepoStub{}).ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, "{\"code\":0,\"message\":\"success\",\"data\":[{\"id\":21,\"email\":\"customer@example.com\",\"username\":\"\"}]}", recorder.Body.String())
}

func TestAffiliateHandlerCreateInviteMatch(t *testing.T) {
	t.Parallel()

	t.Run("creates admin relationship", func(t *testing.T) {
		repo := &manualMatchAffiliateRepoStub{summaries: map[int64]*service.AffiliateSummary{
			10: {UserID: 10},
			21: {UserID: 21},
		}}
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/affiliates/invites", bytes.NewBufferString("{\"inviter_id\":10,\"invitee_id\":21}"))
		req.Header.Set("Content-Type", "application/json")

		newManualMatchRouter(repo).ServeHTTP(recorder, req)

		require.Equal(t, http.StatusOK, recorder.Code)
		require.JSONEq(t, "{\"code\":0,\"message\":\"success\",\"data\":{\"inviter_id\":10,\"invitee_id\":21,\"bind_source\":\"admin\"}}", recorder.Body.String())
		require.Len(t, repo.bindCalls, 1)
		require.Equal(t, service.AffiliateBindingSourceAdmin, repo.bindCalls[0].source)
	})

	t.Run("rejects self binding", func(t *testing.T) {
		repo := &manualMatchAffiliateRepoStub{summaries: map[int64]*service.AffiliateSummary{
			10: {UserID: 10},
		}}
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/affiliates/invites", bytes.NewBufferString("{\"inviter_id\":10,\"invitee_id\":10}"))
		req.Header.Set("Content-Type", "application/json")

		newManualMatchRouter(repo).ServeHTTP(recorder, req)

		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Contains(t, recorder.Body.String(), "AFFILIATE_SELF_BINDING")
		require.Empty(t, repo.bindCalls)
	})

	t.Run("rejects an existing relationship", func(t *testing.T) {
		oldInviterID := int64(9)
		repo := &manualMatchAffiliateRepoStub{summaries: map[int64]*service.AffiliateSummary{
			10: {UserID: 10},
			21: {UserID: 21, InviterID: &oldInviterID},
		}}
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/affiliates/invites", bytes.NewBufferString("{\"inviter_id\":10,\"invitee_id\":21}"))
		req.Header.Set("Content-Type", "application/json")

		newManualMatchRouter(repo).ServeHTTP(recorder, req)

		require.Equal(t, http.StatusConflict, recorder.Code)
		require.Contains(t, recorder.Body.String(), "AFFILIATE_ALREADY_BOUND")
		require.Empty(t, repo.bindCalls)
	})
}
