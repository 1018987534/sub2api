package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFeedbackRoutesAreIndependentAndAdminReadOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{}, Feedback: handler.NewFeedbackHandler(nil)}
	userAuth := middleware.JWTAuthMiddleware(func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) })
	adminAuth := middleware.AdminAuthMiddleware(func(c *gin.Context) { c.AbortWithStatus(http.StatusForbidden) })
	RegisterUserRoutes(router.Group("/api/v1"), handlers, userAuth, nil, nil, nil)
	RegisterAdminRoutes(router.Group("/api/v1"), handlers, adminAuth, nil, nil, nil, nil)
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"POST", "/api/v1/feedback", 401},
		{"GET", "/api/v1/admin/feedback", 403},
		{"POST", "/api/v1/admin/feedback", 404},
		{"POST", "/api/v1/admin/feedback/41/reply", 404},
		{"PATCH", "/api/v1/admin/feedback/41", 404},
		{"DELETE", "/api/v1/admin/feedback/41", 404},
		{"GET", "/api/v1/feedback", 404},
		{"POST", "/api/v1/chat/messages", 401},
		{"POST", "/api/v1/admin/chat/conversations/41/messages", 403},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.path, nil))
			require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
		})
	}
}
