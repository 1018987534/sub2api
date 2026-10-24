package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientDownloadRoutesRequireJWTAndKeepTransfersNarrow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	jwt := middleware.JWTAuthMiddleware(func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) })
	RegisterClientDownloadRoutes(r.Group("/api/v1"), nil, jwt, nil, nil)
	for _, tc := range []struct {
		method, path, host string
		status             int
	}{
		{"POST", "/api/v1/client-downloads/tickets", "xiaohondou.com", 401},
		{"GET", "/api/v1/client-downloads/transfer", "downloads.xiaohondou.com", 403},
		{"GET", "/api/v1/client-downloads/transfer", "xiaohondou.com", 403},
		{"HEAD", "/api/v1/client-downloads/transfer", "downloads.xiaohondou.com", 405},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, "https://"+tc.host+tc.path, nil)
		r.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("%s %s = %d", tc.method, tc.path, w.Code)
		}
	}
}
