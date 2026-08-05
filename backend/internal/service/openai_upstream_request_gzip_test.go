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
	"time"

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
	require.Equal(t, int64(-1), req.ContentLength)
	require.NotNil(t, req.GetBody)
	compressed := readAllRequestBody(t, req)
	require.Less(t, len(compressed), len(body))

	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	require.NoError(t, err)
	decoded, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, body, decoded)
}

func TestBuildUpstreamRequestOpenAIPassthroughStreamsGzipThroughHTTPTransport(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6-terra","input":"` + strings.Repeat("streaming-context-", 5000) + `"}`)
	for _, protocol := range []struct {
		name        string
		enableHTTP2 bool
	}{
		{name: "http1"},
		{name: "http2", enableHTTP2: true},
	} {
		t.Run(protocol.name, func(t *testing.T) {
			received := make(chan []byte, 1)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Header.Get("Content-Encoding") != "gzip" {
					t.Errorf("Content-Encoding = %q, want gzip", req.Header.Get("Content-Encoding"))
				}
				if req.ContentLength != -1 {
					t.Errorf("ContentLength = %d, want -1", req.ContentLength)
				}
				reader, err := gzip.NewReader(req.Body)
				if err != nil {
					t.Errorf("create gzip reader: %v", err)
					received <- nil
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				decoded, err := io.ReadAll(reader)
				if err != nil {
					t.Errorf("read gzip body: %v", err)
				}
				if err := reader.Close(); err != nil {
					t.Errorf("close gzip reader: %v", err)
				}
				received <- decoded
				w.WriteHeader(http.StatusNoContent)
			}))
			server.EnableHTTP2 = protocol.enableHTTP2
			if protocol.enableHTTP2 {
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()

			req := buildOpenAIUpstreamRequestForGzipTest(t, body, openAIUpstreamRequestGzipTestAccount(true), nil)
			parsedURL, err := req.URL.Parse(server.URL)
			require.NoError(t, err)
			req.URL = parsedURL
			resp, err := server.Client().Do(req)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, body, <-received)
		})
	}
}

func TestOpenAIStreamingGzipBodyCloseUnblocksProducer(t *testing.T) {
	body := newOpenAIStreamingGzipBody(bytes.Repeat([]byte("compressible"), 1<<20), nil)
	buffer := make([]byte, 1)
	_, err := body.Read(buffer)
	require.NoError(t, err)
	require.NoError(t, body.Close())

	select {
	case <-body.done:
	case <-time.After(time.Second):
		t.Fatal("gzip producer remained blocked after request body close")
	}
}

func TestOpenAIStreamingGzipBodyRecordsWireMetrics(t *testing.T) {
	source := bytes.Repeat([]byte("compressible-context-"), 5000)
	trace := NewOpenAILatencyTrace(time.Now(), len(source), "gpt-test", true)
	trace.BeginAttempt(12017, len(source), time.Now())
	body := newOpenAIStreamingGzipBody(source, trace)

	compressed, err := io.ReadAll(body)
	require.NoError(t, err)
	require.NoError(t, body.Close())
	<-body.done

	trace.mu.Lock()
	attempt := *trace.attempt
	trace.mu.Unlock()
	require.True(t, attempt.upstreamGzipEnabled)
	require.False(t, attempt.upstreamGzipError)
	require.Equal(t, len(source), attempt.upstreamBodyBytes)
	require.Equal(t, int64(len(compressed)), attempt.upstreamWireBytes)
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
