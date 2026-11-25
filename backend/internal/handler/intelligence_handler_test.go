package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/intelligence"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type intelligenceMonitorRepo struct {
	service.ChannelMonitorV2Repository
	cfg service.ChannelMonitorV2Config
}

func (r *intelligenceMonitorRepo) GetConfig(context.Context) (*service.ChannelMonitorV2Config, error) {
	return &r.cfg, nil
}

type intelligenceHistoryStore struct {
	intelligence.Store
	requested []int64
	keys      []intelligence.ProbeKey
	limit     int
	since     time.Time
	recordID  int64
	record    *intelligence.Record
	err       error
}

func (s *intelligenceHistoryStore) Configs(context.Context) ([]intelligence.Config, error) {
	c := intelligence.DefaultConfig(1)
	c.Model = "configured-model"
	c.ReasoningEffort = "high"
	additional := c
	additional.ProbeID, additional.Model, additional.IntervalMinutes = 17, "second-model", 15
	additional.Enabled = true
	pending := c
	pending.ProbeID, pending.Model = 18, "pending-model"
	return []intelligence.Config{c, additional, pending, intelligence.DefaultConfig(2), intelligence.DefaultConfig(3)}, nil
}
func (s *intelligenceHistoryStore) Timelines(_ context.Context, configs []intelligence.Config, since time.Time, limit int) (map[intelligence.ProbeKey][]intelligence.TimelineRecord, error) {
	s.limit, s.since = limit, since
	for _, c := range configs {
		s.keys = append(s.keys, c.Key())
	}
	return map[intelligence.ProbeKey][]intelligence.TimelineRecord{
		{GroupID: 1}:              {{ID: 1, GroupID: 1, Status: "normal"}},
		{GroupID: 1, ProbeID: 17}: {{ID: 2, GroupID: 1, Status: "degraded"}},
	}, s.err
}

func (s *intelligenceHistoryStore) Record(_ context.Context, groupID, id int64, since time.Time) (intelligence.Record, error) {
	s.requested = append(s.requested, groupID)
	s.recordID, s.since = id, since
	if s.err != nil {
		return intelligence.Record{}, s.err
	}
	if s.record != nil && s.record.ID == id && s.record.GroupID == groupID {
		return *s.record, nil
	}
	return intelligence.Record{}, sql.ErrNoRows
}
func TestIntelligenceStatusAuthorizationAndRedaction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &intelligenceHistoryStore{}
	repo := &intelligenceMonitorRepo{cfg: service.ChannelMonitorV2Config{Enabled: true, GroupIDs: []int64{1, 2, 3}, Platforms: []service.ChannelMonitorV2PlatformConfig{{Platform: "openai", Enabled: true}}}}
	h := &IntelligenceHandler{store: store, monitor: service.NewChannelMonitorV2Service(repo), access: &channelMonitorV2GroupAuthorizerStub{groups: []service.Group{{ID: 1, Platform: "openai"}, {ID: 3, Platform: "anthropic"}}}}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/status?group_id=1&group_id=2&group_id=3", nil)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 99})
	h.Status(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, []intelligence.ProbeKey{{GroupID: 1}, {GroupID: 1, ProbeID: 17}, {GroupID: 1, ProbeID: 18}}, store.keys)
	body := w.Body.String()
	for _, secret := range []string{"secret", "prompt", "answer", "api_key_id", "updated_by", "account_id", "paused", "private-request", `"config":`} {
		require.NotContains(t, body, secret)
	}
	var decoded struct {
		Data struct {
			Groups        map[string][]intelligence.Record
			Metadata      map[string]intelligenceDisplayMetadata
			Models        map[string][]intelligenceModelTimeline
			WindowMinutes int `json:"window_minutes"`
			RecordLimit   int `json:"record_limit"`
		}
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &decoded))
	require.Equal(t, 7*24*60, decoded.Data.WindowMinutes)
	require.Equal(t, 60, decoded.Data.RecordLimit)
	require.Equal(t, 60, store.limit)
	require.WithinDuration(t, time.Now().Add(-7*24*time.Hour), store.since, 5*time.Second)
	require.Len(t, decoded.Data.Groups, 1)
	require.Equal(t, map[string]intelligenceDisplayMetadata{"1": {Model: "configured-model", ReasoningEffort: "high", Protocol: "responses", IntervalMinutes: 10}}, decoded.Data.Metadata)
	require.Equal(t, "normal", decoded.Data.Groups["1"][0].Status)
	require.Len(t, decoded.Data.Models, 1)
	require.Len(t, decoded.Data.Models["1"], 2)
	require.Equal(t, "second-model", decoded.Data.Models["1"][0].Model)
	require.Equal(t, 15, decoded.Data.Models["1"][0].IntervalMinutes)
	require.Equal(t, "degraded", decoded.Data.Models["1"][0].Records[0].Status)
	require.Equal(t, []intelligence.TimelineRecord{}, decoded.Data.Models["1"][1].Records)
}
func TestIntelligenceStatusRejectsUnauthorizedAndDisabled(t *testing.T) {
	repo := &intelligenceMonitorRepo{}
	h := &IntelligenceHandler{monitor: service.NewChannelMonitorV2Service(repo), access: &channelMonitorV2GroupAuthorizerStub{}}
	for _, authenticated := range []bool{false, true} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/status?group_id=1", nil)
		expected := http.StatusUnauthorized
		if authenticated {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 99})
			expected = http.StatusForbidden
		}
		h.Status(c)
		require.Equal(t, expected, w.Code)
	}
}
func TestIntelligenceRejectsInvalidGroupAndCursor(t *testing.T) {
	for _, tc := range []struct{ id, url string }{{"0", "/history"}, {"1", "/history?before_id=-1"}, {"1", "/history?before_id=no"}} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = gin.Params{{Key: "groupID", Value: tc.id}}
		c.Request = httptest.NewRequest("GET", tc.url, nil)
		(&IntelligenceHandler{}).History(c)
		require.Equal(t, 400, w.Code)
	}
}

func TestIntelligenceRecordDetailReturnsAnswerWithoutTimingOrPrivateFields(t *testing.T) {
	for _, status := range []string{"normal", "degraded"} {
		t.Run(status, func(t *testing.T) {
			cfg := intelligence.DefaultConfig(1)
			cfg.ProbeID = 17
			cfg.Model, cfg.ReasoningEffort, cfg.Prompt = "saved-model", "high", "private prompt"
			cfg.APIKeyID, cfg.UpdatedBy = 123, 456
			store := &intelligenceHistoryStore{record: &intelligence.Record{
				ID: 42, GroupID: 1, Status: status, Answer: "完整回答：21\n保留推导",
				CheckedAt: time.Now(), DurationMS: 12345, AccountID: 99, Paused: true,
				RequestID: "private-request", Error: "private error", Config: &cfg,
			}}
			repo := &intelligenceMonitorRepo{cfg: service.ChannelMonitorV2Config{Enabled: true, Platforms: []service.ChannelMonitorV2PlatformConfig{{Platform: "openai", Enabled: true}}}}
			h := &IntelligenceHandler{store: store, monitor: service.NewChannelMonitorV2Service(repo), access: &channelMonitorV2GroupAuthorizerStub{groups: []service.Group{{ID: 1, Platform: "openai"}}}}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/intelligence-checks/1/records/42", nil)
			c.Params = gin.Params{{Key: "groupID", Value: "1"}, {Key: "recordID", Value: "42"}}
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 99})
			h.RecordDetail(c)
			require.Equal(t, http.StatusOK, w.Code)
			var body struct{ Data map[string]any }
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			require.Equal(t, map[string]any{"id": float64(42), "group_id": float64(1), "status": status, "answer": "完整回答：21\n保留推导", "model": "saved-model", "reasoning_effort": "high"}, body.Data)
			require.Equal(t, []int64{1}, store.requested)
			require.Equal(t, int64(42), store.recordID)
			require.WithinDuration(t, time.Now().Add(-7*24*time.Hour), store.since, 5*time.Second)
			for _, private := range []string{"checked_at", "duration_ms", "private", "account_id", "paused", "api_key_id", "updated_by", "config"} {
				require.NotContains(t, w.Body.String(), private)
			}
		})
	}
}

func TestIntelligenceRecordDetailRejectsUnavailableRecordsAndGroups(t *testing.T) {
	for _, tc := range []struct {
		name, group, record                    string
		unauthenticated, disabled, hiddenGroup bool
		storeError                             error
		status                                 int
	}{
		{name: "unauthenticated", group: "1", record: "1", unauthenticated: true, status: 401},
		{name: "disabled", group: "1", record: "1", disabled: true, status: 403},
		{name: "other user group", group: "2", record: "1", status: 404},
		{name: "disabled platform", group: "3", record: "1", status: 404},
		{name: "hidden monitor group", group: "1", record: "1", hiddenGroup: true, status: 404},
		{name: "invalid group", group: "0", record: "1", status: 400},
		{name: "invalid record", group: "1", record: "0", status: 400},
		{name: "cursor overflow", group: "1", record: "9223372036854775807", status: 400},
		{name: "missing exact record", group: "1", record: "2", status: 404},
		{name: "database error", group: "1", record: "1", storeError: errors.New("db unavailable"), status: 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &intelligenceHistoryStore{err: tc.storeError}
			cfg := service.ChannelMonitorV2Config{Enabled: !tc.disabled, Platforms: []service.ChannelMonitorV2PlatformConfig{{Platform: "openai", Enabled: true}}}
			if tc.hiddenGroup {
				cfg.GroupIDs = []int64{2}
			}
			repo := &intelligenceMonitorRepo{cfg: cfg}
			h := &IntelligenceHandler{store: store, monitor: service.NewChannelMonitorV2Service(repo), access: &channelMonitorV2GroupAuthorizerStub{groups: []service.Group{{ID: 1, Platform: "openai"}, {ID: 3, Platform: "anthropic"}}}}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/intelligence-checks/"+tc.group+"/records/"+tc.record, nil)
			c.Params = gin.Params{{Key: "groupID", Value: tc.group}, {Key: "recordID", Value: tc.record}}
			if !tc.unauthenticated {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 99})
			}
			h.RecordDetail(c)
			require.Equal(t, tc.status, w.Code)
			require.NotContains(t, w.Body.String(), "secret answer")
			if tc.status != 500 && tc.name != "missing exact record" {
				require.Empty(t, store.requested)
			}
		})
	}
}
