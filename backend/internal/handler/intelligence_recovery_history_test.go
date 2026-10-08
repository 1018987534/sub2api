package handler

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/intelligence"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
	"time"
)

type recoveryAdminStub struct {
	before int64
	limit  int
	calls  int
}

func (s *recoveryAdminStub) RecoveryHistory(_ context.Context, before int64, limit int) ([]intelligence.RecoveryRecord, error) {
	s.before = before
	s.limit = limit
	s.calls++
	items := []intelligence.RecoveryRecord{}
	for i := 0; i < 25; i++ {
		items = append(items, intelligence.RecoveryRecord{ID: int64(99 - i), AccountID: 12459, AccountName: "paused-account", Status: "degraded", Outcome: "retry", StartedAt: time.Now()})
	}
	return items, nil
}
func (s *recoveryAdminStub) RecoveryQueue(context.Context) ([]intelligence.RecoveryQueueEntry, error) {
	return []intelligence.RecoveryQueueEntry{{AccountID: 12459, AccountName: "paused-account", IntervalMinutes: 8, NextRunAt: time.Now()}}, nil
}
func TestIntelligenceRecoveryHistoryAdminOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		auth      bool
		role, url string
		code      int
	}{{false, "", "/recovery/history", 401}, {true, service.RoleUser, "/recovery/history", 403}, {true, service.RoleAdmin, "/recovery/history?before_id=-1", 400}, {true, service.RoleAdmin, "/recovery/history?before_id=no", 400}, {true, service.RoleAdmin, "/recovery/history?before_id=100", 200}} {
		s := &recoveryAdminStub{}
		h := &IntelligenceHandler{recoveryAdmin: s}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", tc.url, nil)
		if tc.auth {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		}
		c.Set(string(middleware.ContextKeyUserRole), tc.role)
		h.RecoveryHistory(c)
		require.Equal(t, tc.code, w.Code)
		if tc.code == 200 {
			require.EqualValues(t, 100, s.before)
			require.Equal(t, 25, s.limit)
			require.Contains(t, w.Body.String(), `"account_name":"paused-account"`)
			require.Contains(t, w.Body.String(), `"has_more":true`)
			require.NotContains(t, w.Body.String(), `"lease_token"`)
		} else {
			require.Zero(t, s.calls)
		}
	}
}
