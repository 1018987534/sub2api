package handler

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientDownloadGuardsNeverReturnStaticRedirectOnFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewClientDownloadHandler(nil)
	for _, tc := range []struct {
		method, host, cookie string
		want                 int
	}{
		{"GET", "xiaohondou.com", "", 403},
		{"GET", "control-origin.xiaohondou.com", "", 403},
		{"GET", "downloads.xiaohondou.com", "", 403},
		{"HEAD", "downloads.xiaohondou.com", "", 405},
		{"GET", "downloads.xiaohondou.com", "invalid", 503},
	} {
		r := gin.New()
		r.Any("/transfer", h.Transfer)
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, "https://"+tc.host+"/transfer", nil)
		req.Header.Set("X-Real-IP", "198.51.100.2")
		if tc.cookie != "" {
			req.AddCookie(&http.Cookie{Name: downloadTicketCookie, Value: tc.cookie})
		}
		r.ServeHTTP(w, req)
		if w.Code != tc.want || w.Header().Get("X-Accel-Redirect") != "" {
			t.Fatalf("%s %s: code=%d headers=%v", tc.method, tc.host, w.Code, w.Header())
		}
	}
	r := gin.New()
	r.POST("/tickets", h.Issue)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/tickets", nil))
	if w.Code != 401 {
		t.Fatalf("anonymous ticket=%d", w.Code)
	}
}
