package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBuildUpstreamRequestOpenAIPassthroughKeepsLargeBodyUncompressed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := bytes.Repeat([]byte(`{"model":"gpt-5.6-terra","input":"large-request"}`), 4096)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	svc := &OpenAIGatewayService{
		cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra:    map[string]any{"openai_upstream_request_gzip_enabled": true},
	}

	req, err := svc.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, account, body, "token")
	require.NoError(t, err)
	require.Empty(t, req.Header.Get("Content-Encoding"))
	require.Equal(t, int64(len(body)), req.ContentLength)
	require.Equal(t, body, readOpenAIUpstreamRequestBody(t, req.Body))

	require.NotNil(t, req.GetBody)
	retryBody, err := req.GetBody()
	require.NoError(t, err)
	require.Equal(t, body, readOpenAIUpstreamRequestBody(t, retryBody))
}

func readOpenAIUpstreamRequestBody(t *testing.T, body io.ReadCloser) []byte {
	t.Helper()
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	return data
}
