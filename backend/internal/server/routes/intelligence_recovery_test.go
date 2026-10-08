package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestIntelligenceRecoveryAdminRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerIntelligenceRoutes(r.Group("/admin"), &handler.Handlers{Intelligence: &handler.IntelligenceHandler{}}, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/admin/intelligence-checks/recovery/history", nil))
	require.Equal(t, 401, w.Code)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/admin/intelligence-checks/0/history", nil))
	require.Equal(t, 400, w.Code)
}
