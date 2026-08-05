//go:build unit

package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIStreamingPassthroughCapacityShedPrecedesPassthroughRule(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &OpenAIGatewayService{cfg: &config.Config{
		Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
	}}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	rule := newNonFailoverPassthroughRule(http.StatusBadRequest, "servers are currently overloaded", http.StatusBadRequest, "")
	rule.Platforms = []string{PlatformOpenAI}
	rule.PassthroughBody = true
	rule.CustomMessage = nil
	ruleSvc := &ErrorPassthroughService{}
	ruleSvc.setLocalCache([]*model.ErrorPassthroughRule{rule})
	BindErrorPassthroughService(c, ruleSvc)

	message := "Our servers are currently overloaded. Please try again later."
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			"event: response.created",
			`data: {"type":"response.created","response":{"id":"resp_capacity"}}`,
			"",
			"event: response.failed",
			`data: {"type":"response.failed","response":{"id":"resp_capacity","error":{"type":"invalid_request_error","message":"` + message + `"}}}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-capacity-passthrough-order"}},
	}

	_, err := svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI, Name: "acc"}, time.Now(), "", "")
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.RetryableOnSameAccount)
	require.True(t, failoverErr.RequestScopedTransient)
	require.False(t, IsResponseCommitted(c))
	require.Empty(t, rec.Body.String())
}
