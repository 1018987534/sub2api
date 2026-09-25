package handler

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/intelligence"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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
	limits    []int
	since     []time.Time
}

func (s *intelligenceHistoryStore) Configs(context.Context) ([]intelligence.Config, error) {
	c := intelligence.DefaultConfig(1)
	c.Model = "configured-model"
	c.ReasoningEffort = "high"
	return []intelligence.Config{c, intelligence.DefaultConfig(2), intelligence.DefaultConfig(3)}, nil
}
func (s *intelligenceHistoryStore) History(_ context.Context, id int64, since time.Time, before int64, limit int) ([]intelligence.Record, error) {
	s.requested = append(s.requested, id)
	s.limits = append(s.limits, limit)
	s.since = append(s.since, since)
	c := intelligence.DefaultConfig(id)
	c.Prompt = "secret prompt"
	c.APIKeyID = 123
	c.UpdatedBy = 456
	return []intelligence.Record{{ID: 1, GroupID: id, CheckedAt: time.Now(), Status: "normal", Answer: "secret answer", Error: "secret error", Config: &c}}, nil
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
	require.Equal(t, []int64{1}, store.requested)
	body := w.Body.String()
	for _, secret := range []string{"secret", "prompt", "answer", "api_key_id", "updated_by", `"config":`} {
		require.NotContains(t, body, secret)
	}
	var decoded struct {
		Data struct {
			Groups        map[string][]intelligence.Record
			Metadata      map[string]map[string]string `json:"metadata"`
			WindowMinutes int                          `json:"window_minutes"`
			RecordLimit   int                          `json:"record_limit"`
		}
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &decoded))
	require.Equal(t, 7*24*60, decoded.Data.WindowMinutes)
	require.Equal(t, 60, decoded.Data.RecordLimit)
	require.Equal(t, []int{60}, store.limits)
	require.WithinDuration(t, time.Now().Add(-7*24*time.Hour), store.since[0], 5*time.Second)
	require.Len(t, decoded.Data.Groups, 1)
	require.Equal(t, map[string]map[string]string{"1": {"model": "configured-model", "reasoning_effort": "high"}}, decoded.Data.Metadata)
	require.Equal(t, "normal", decoded.Data.Groups["1"][0].Status)
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
