package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBuildUpstreamRequestOpenAIPassthroughGzipsOptedInLargeAPIKeyBody(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-terra","input":"` + strings.Repeat("compressible-context-", 5000) + `"}`)
	req := buildOpenAIUpstreamRequestForGzipTest(t, body, &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra: map[string]any{
			OpenAIUpstreamRequestGzipEnabledExtraKey: true,
		},
	}, nil)

	require.Equal(t, "gzip", req.Header.Get("Content-Encoding"))
	require.Equal(t, "application/json", req.Header.Get("Content-Type"))
	compressed := readAllRequestBody(t, req)
	require.Equal(t, int64(len(compressed)), req.ContentLength)
	require.Less(t, len(compressed), len(body))

	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	require.NoError(t, err)
	decoded, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, body, decoded)
}

func TestBuildUpstreamRequestOpenAIPassthroughGzipOptInGuards(t *testing.T) {
	largeBody := []byte(`{"model":"gpt-5.6-terra","input":"` + strings.Repeat("x", openAIUpstreamRequestGzipMinBytes) + `"}`)
	tests := []struct {
		name     string
		body     []byte
		account  *Account
		headers  http.Header
		wantGzip bool
	}{
		{
			name:    "small body remains uncompressed",
			body:    []byte(`{"model":"gpt-5.6-terra"}`),
			account: openAIUpstreamRequestGzipTestAccount(true),
		},
		{
			name:    "disabled account remains uncompressed",
			body:    largeBody,
			account: openAIUpstreamRequestGzipTestAccount(false),
		},
		{
			name: "oauth account remains uncompressed",
			body: largeBody,
			account: &Account{
				Platform: PlatformOpenAI,
				Type:     AccountTypeOAuth,
				Extra:    map[string]any{OpenAIUpstreamRequestGzipEnabledExtraKey: true},
			},
		},
		{
			name:    "existing content encoding is not recompressed",
			body:    largeBody,
			account: openAIUpstreamRequestGzipTestAccount(true),
			headers: http.Header{"Content-Encoding": []string{"br"}},
		},
		{
			name:    "signed body remains uncompressed",
			body:    largeBody,
			account: openAIUpstreamRequestGzipTestAccount(true),
			headers: http.Header{"Digest": []string{"sha-256=signature"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := buildOpenAIUpstreamRequestForGzipTest(t, tt.body, tt.account, tt.headers)
			require.Equal(t, tt.wantGzip, req.Header.Get("Content-Encoding") == "gzip")
			require.Equal(t, tt.body, readAllRequestBody(t, req))
			require.Equal(t, int64(len(tt.body)), req.ContentLength)
		})
	}
}

func openAIUpstreamRequestGzipTestAccount(enabled bool) *Account {
	return &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra: map[string]any{
			OpenAIUpstreamRequestGzipEnabledExtraKey: enabled,
		},
	}
}

func buildOpenAIUpstreamRequestForGzipTest(t *testing.T, body []byte, account *Account, headers http.Header) *http.Request {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	for name, values := range headers {
		for _, value := range values {
			c.Request.Header.Add(name, value)
		}
	}

	svc := &OpenAIGatewayService{
		cfg: &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
	}
	req, err := svc.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, account, body, "token")
	require.NoError(t, err)
	return req
}

func readAllRequestBody(t *testing.T, req *http.Request) []byte {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	return body
}
