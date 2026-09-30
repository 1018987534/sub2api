package admin

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetSidebarSettings(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{
		service.SettingKeySiteLogo: "large logo", "smtp_password": "secret",
		service.SettingPaymentEnabled:     "true",
		service.SettingKeyCustomMenuItems: `[{"id":"admin-menu","visibility":"admin","title":"Admin"}]`,
	})
	for _, enabled := range []bool{true, false} {
		h.opsService = service.NewOpsService(nil, repo, &config.Config{Ops: config.OpsConfig{Enabled: enabled}}, nil, nil, nil, nil, nil, nil, nil, nil)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest("GET", "/api/v1/admin/settings/sidebar", nil)
		h.GetSidebarSettings(c)
		require.Equal(t, 200, rec.Code)
		var payload struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
		require.Len(t, payload.Data, 5)
		require.Equal(t, enabled, payload.Data["ops_monitoring_enabled"])
		require.Equal(t, true, payload.Data["payment_enabled"])
		require.Contains(t, rec.Body.String(), "admin-menu")
		require.NotContains(t, rec.Body.String(), "site_logo")
		require.NotContains(t, rec.Body.String(), "secret")
	}
}
