package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type reengagementAdminServiceStub struct {
	*stubAdminService
	reengagementMu sync.Mutex
	filters        service.UserListFilters
	listCalls      int
}

func (s *reengagementAdminServiceStub) ListUsers(_ context.Context, page, pageSize int, filters service.UserListFilters, _, _ string) ([]service.User, int64, error) {
	s.reengagementMu.Lock()
	s.filters = filters
	s.listCalls++
	s.reengagementMu.Unlock()

	cutoff := time.Now().UTC().AddDate(0, 0, -filters.InactiveDays)
	users := make([]service.User, 0, len(s.users))
	for _, user := range s.users {
		if len(filters.UserIDs) > 0 && !slices.Contains(filters.UserIDs, user.ID) {
			continue
		}
		if filters.Status != "" && user.Status != filters.Status {
			continue
		}
		if filters.Role != "" && user.Role != filters.Role {
			continue
		}
		if filters.HasRecharged != nil && (user.TotalRecharged > 0) != *filters.HasRecharged {
			continue
		}
		search := strings.ToLower(filters.Search)
		if search != "" && !strings.Contains(strings.ToLower(user.Email), search) && !strings.Contains(strings.ToLower(user.Username), search) {
			continue
		}
		if filters.NeverUsed && user.LastUsedAt != nil {
			continue
		}
		if !filters.NeverUsed && user.LastUsedAt != nil && !user.LastUsedAt.Before(cutoff) {
			continue
		}
		users = append(users, user)
	}
	slices.SortFunc(users, func(a, b service.User) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	total := int64(len(users))
	start := (page - 1) * pageSize
	if start >= len(users) {
		return []service.User{}, total, nil
	}
	end := min(start+pageSize, len(users))
	return users[start:end], total, nil
}

func (s *reengagementAdminServiceStub) lastFilters() service.UserListFilters {
	s.reengagementMu.Lock()
	defer s.reengagementMu.Unlock()
	return s.filters
}

func (s *reengagementAdminServiceStub) callCount() int {
	s.reengagementMu.Lock()
	defer s.reengagementMu.Unlock()
	return s.listCalls
}

type recordingNotificationEmailSender struct {
	mu     sync.Mutex
	inputs []service.NotificationEmailSendInput
}

func (s *recordingNotificationEmailSender) SendWithResult(_ context.Context, input service.NotificationEmailSendInput) (service.NotificationEmailSendResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inputs = append(s.inputs, input)
	return service.NotificationEmailSendResult{Sent: true}, nil
}

func TestUserHandlerSendReengagementEmailsFiltersRecipientsAndSendsTemplate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Now().UTC()
	oldUsage := now.Add(-20 * 24 * time.Hour)
	recentUsage := now.Add(-2 * 24 * time.Hour)
	base := newStubAdminService()
	base.users = []service.User{
		{ID: 1, Email: "inactive@example.com", Username: "Inactive", Role: service.RoleUser, Status: service.StatusActive, LastUsedAt: &oldUsage},
		{ID: 2, Email: "recent@example.com", Role: service.RoleUser, Status: service.StatusActive, LastUsedAt: &recentUsage},
		{ID: 3, Email: "admin@example.com", Role: service.RoleAdmin, Status: service.StatusActive},
		{ID: 4, Email: "disabled@example.com", Role: service.RoleUser, Status: service.StatusDisabled},
		{ID: 5, Email: "never-used@example.com", Role: service.RoleUser, Status: service.StatusActive},
	}
	adminSvc := &reengagementAdminServiceStub{stubAdminService: base}
	sender := &recordingNotificationEmailSender{}
	handler := NewUserHandler(adminSvc, nil, nil, nil, nil, nil, nil)
	handler.notificationEmail = sender

	router := gin.New()
	router.POST("/admin/users/send-reengagement-email", handler.SendReengagementEmails)
	body := bytes.NewBufferString(`{"user_ids":[1,2,3,4,5,5],"inactive_days":14}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/admin/users/send-reengagement-email", body)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	filters := adminSvc.lastFilters()
	require.Equal(t, service.StatusActive, filters.Status)
	require.Equal(t, service.RoleUser, filters.Role)
	require.Equal(t, 14, filters.InactiveDays)
	require.Equal(t, []int64{1, 2, 3, 4, 5}, filters.UserIDs)
	require.False(t, *filters.IncludeSubscriptions)

	sender.mu.Lock()
	inputs := append([]service.NotificationEmailSendInput(nil), sender.inputs...)
	sender.mu.Unlock()
	require.Len(t, inputs, 2)
	slices.SortFunc(inputs, func(a, b service.NotificationEmailSendInput) int {
		if a.UserID < b.UserID {
			return -1
		}
		if a.UserID > b.UserID {
			return 1
		}
		return 0
	})
	require.Equal(t, int64(1), inputs[0].UserID)
	require.Equal(t, int64(5), inputs[1].UserID)
	for _, input := range inputs {
		require.Equal(t, service.NotificationEmailEventUserReengagement, input.Event)
		require.Equal(t, "0.06", input.Variables["rate_multiplier"])
		require.Equal(t, "user_reengagement", input.SourceType)
		require.NotEmpty(t, input.ReminderKey)
	}

	var responseBody struct {
		Data struct {
			Selected int `json:"selected"`
			Matched  int `json:"matched"`
			Sent     int `json:"sent"`
			Skipped  int `json:"skipped"`
			Failed   int `json:"failed"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &responseBody))
	require.Equal(t, 5, responseBody.Data.Selected)
	require.Equal(t, 2, responseBody.Data.Matched)
	require.Equal(t, 2, responseBody.Data.Sent)
	require.Equal(t, 3, responseBody.Data.Skipped)
	require.Zero(t, responseBody.Data.Failed)
}

func TestUserHandlerSendReengagementEmailsFiltersSelectedUsersByRechargeStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldUsage := time.Now().UTC().Add(-20 * 24 * time.Hour)
	base := newStubAdminService()
	base.users = []service.User{
		{ID: 1, Email: "recharged@example.com", Role: service.RoleUser, Status: service.StatusActive, LastUsedAt: &oldUsage, TotalRecharged: 10},
		{ID: 2, Email: "not-recharged@example.com", Role: service.RoleUser, Status: service.StatusActive, LastUsedAt: &oldUsage},
	}
	adminSvc := &reengagementAdminServiceStub{stubAdminService: base}
	sender := &recordingNotificationEmailSender{}
	handler := NewUserHandler(adminSvc, nil, nil, nil, nil, nil, nil)
	handler.notificationEmail = sender

	router := gin.New()
	router.POST("/admin/users/send-reengagement-email", handler.SendReengagementEmails)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/admin/users/send-reengagement-email",
		bytes.NewBufferString(`{"user_ids":[1,2],"inactive_days":14,"has_recharged":false}`),
	)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	filters := adminSvc.lastFilters()
	require.NotNil(t, filters.HasRecharged)
	require.False(t, *filters.HasRecharged)

	sender.mu.Lock()
	inputs := append([]service.NotificationEmailSendInput(nil), sender.inputs...)
	sender.mu.Unlock()
	require.Len(t, inputs, 1)
	require.Equal(t, int64(2), inputs[0].UserID)
}

func TestUserHandlerSendAllReengagementEmailsProcessesEveryFilteredPage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldUsage := time.Now().UTC().Add(-20 * 24 * time.Hour)
	base := newStubAdminService()
	base.users = make([]service.User, 0, reengagementCampaignPageSize+1)
	for id := int64(1); id <= reengagementCampaignPageSize+1; id++ {
		base.users = append(base.users, service.User{
			ID:             id,
			Email:          fmt.Sprintf("inactive-%03d@example.com", id),
			Role:           service.RoleUser,
			Status:         service.StatusActive,
			LastUsedAt:     &oldUsage,
			TotalRecharged: 0,
		})
	}
	base.users = append(base.users, service.User{
		ID:             9999,
		Email:          "inactive-recharged@example.com",
		Role:           service.RoleUser,
		Status:         service.StatusActive,
		LastUsedAt:     &oldUsage,
		TotalRecharged: 10,
	})
	adminSvc := &reengagementAdminServiceStub{stubAdminService: base}
	sender := &recordingNotificationEmailSender{}
	handler := NewUserHandler(adminSvc, nil, nil, nil, nil, nil, nil)
	handler.notificationEmail = sender

	router := gin.New()
	router.POST("/admin/users/send-reengagement-email", handler.SendReengagementEmails)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/admin/users/send-reengagement-email",
		bytes.NewBufferString(`{"send_all":true,"inactive_days":14,"search":"inactive","has_recharged":false}`),
	)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusAccepted, recorder.Code)
	var responseBody struct {
		Data struct {
			Queued  bool `json:"queued"`
			Matched int  `json:"matched"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &responseBody))
	require.True(t, responseBody.Data.Queued)
	require.Equal(t, reengagementCampaignPageSize+1, responseBody.Data.Matched)

	require.Eventually(t, func() bool {
		sender.mu.Lock()
		defer sender.mu.Unlock()
		return len(sender.inputs) == reengagementCampaignPageSize+1 && !handler.reengagementRunning.Load()
	}, 5*time.Second, 10*time.Millisecond)
	require.GreaterOrEqual(t, adminSvc.callCount(), 3)
	filters := adminSvc.lastFilters()
	require.Equal(t, "inactive", filters.Search)
	require.NotNil(t, filters.HasRecharged)
	require.False(t, *filters.HasRecharged)
}

func TestUserHandlerSendReengagementEmailsRejectsInvalidInactivityWindow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adminSvc := &reengagementAdminServiceStub{stubAdminService: newStubAdminService()}
	handler := NewUserHandler(adminSvc, nil, nil, nil, nil, nil, nil)
	handler.notificationEmail = &recordingNotificationEmailSender{}

	router := gin.New()
	router.POST("/admin/users/send-reengagement-email", handler.SendReengagementEmails)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/admin/users/send-reengagement-email", bytes.NewBufferString(`{"user_ids":[1],"inactive_days":0}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Zero(t, adminSvc.lastFilters().InactiveDays)
}
