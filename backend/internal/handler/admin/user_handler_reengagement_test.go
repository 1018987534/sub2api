package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type reengagementAdminServiceStub struct {
	*stubAdminService
	filters service.UserListFilters
}

func (s *reengagementAdminServiceStub) ListUsers(_ context.Context, _, _ int, filters service.UserListFilters, _, _ string) ([]service.User, int64, error) {
	s.filters = filters
	cutoff := time.Now().UTC().AddDate(0, 0, -filters.InactiveDays)
	users := make([]service.User, 0, len(s.users))
	for _, user := range s.users {
		if !slices.Contains(filters.UserIDs, user.ID) || user.Status != filters.Status || user.Role != filters.Role {
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
	return users, int64(len(users)), nil
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
	require.Equal(t, service.StatusActive, adminSvc.filters.Status)
	require.Equal(t, service.RoleUser, adminSvc.filters.Role)
	require.Equal(t, 14, adminSvc.filters.InactiveDays)
	require.Equal(t, []int64{1, 2, 3, 4, 5}, adminSvc.filters.UserIDs)
	require.False(t, *adminSvc.filters.IncludeSubscriptions)

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
	require.Zero(t, adminSvc.filters.InactiveDays)
}
