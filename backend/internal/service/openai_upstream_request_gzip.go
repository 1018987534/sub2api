package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const openAIUpstreamRequestGzipMinBytes = 64 << 10

var openAIUpstreamRequestBodyIntegrityHeaders = []string{
	"Content-MD5",
	"Digest",
	"Signature",
	"Signature-Input",
	"X-Content-SHA256",
	"X-Signature",
}

type openAIUpstreamRequestBody struct {
	body          io.ReadCloser
	contentLength int64
	gzipped       bool
	getBody       func() (io.ReadCloser, error)
}

func buildOpenAIUpstreamRequestBody(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
) (*openAIUpstreamRequestBody, error) {
	if !shouldGzipOpenAIUpstreamRequestBody(c, account, body) {
		return &openAIUpstreamRequestBody{
			body:          io.NopCloser(bytes.NewReader(body)),
			contentLength: int64(len(body)),
			getBody: func() (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(body)), nil
			},
		}, nil
	}

	trace := OpenAILatencyTraceFromContext(ctx)
	return &openAIUpstreamRequestBody{
		body:          newOpenAIStreamingGzipBody(body, trace),
		contentLength: -1,
		gzipped:       true,
		getBody: func() (io.ReadCloser, error) {
			return newOpenAIStreamingGzipBody(body, trace), nil
		},
	}, nil
}

type openAIStreamingGzipBody struct {
	reader    *io.PipeReader
	writer    *io.PipeWriter
	source    []byte
	trace     *OpenAILatencyTrace
	startOnce sync.Once
	closeOnce sync.Once
	done      chan struct{}
}

func newOpenAIStreamingGzipBody(source []byte, trace *OpenAILatencyTrace) *openAIStreamingGzipBody {
	reader, writer := io.Pipe()
	return &openAIStreamingGzipBody{
		reader: reader,
		writer: writer,
		source: source,
		trace:  trace,
		done:   make(chan struct{}),
	}
}

func (b *openAIStreamingGzipBody) Read(p []byte) (int, error) {
	b.startOnce.Do(func() {
		go b.compress()
	})
	return b.reader.Read(p)
}

func (b *openAIStreamingGzipBody) Close() error {
	b.closeOnce.Do(func() {
		b.startOnce.Do(func() {
			_ = b.writer.CloseWithError(io.ErrClosedPipe)
			close(b.done)
		})
		_ = b.reader.Close()
	})
	return nil
}

func (b *openAIStreamingGzipBody) compress() {
	startedAt := time.Now()
	counter := &openAICountingWriter{writer: b.writer}
	writer, err := gzip.NewWriterLevel(counter, gzip.BestSpeed)
	if err == nil {
		_, err = writer.Write(b.source)
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
	}
	if err != nil {
		_ = b.writer.CloseWithError(fmt.Errorf("gzip upstream request body: %w", err))
	} else {
		_ = b.writer.Close()
	}
	if b.trace != nil {
		b.trace.MarkUpstreamRequestGzip(len(b.source), counter.written, time.Since(startedAt), err)
	}
	close(b.done)
}

type openAICountingWriter struct {
	writer  io.Writer
	written int64
}

func (w *openAICountingWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.written += int64(n)
	return n, err
}

func shouldGzipOpenAIUpstreamRequestBody(c *gin.Context, account *Account, body []byte) bool {
	if !account.IsOpenAIUpstreamRequestGzipEnabled() || len(body) < openAIUpstreamRequestGzipMinBytes {
		return false
	}

	if c != nil && c.Request != nil {
		contentType := strings.TrimSpace(c.Request.Header.Get("Content-Type"))
		if contentType != "" && !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
			return false
		}
		if strings.TrimSpace(c.Request.Header.Get("Content-Encoding")) != "" {
			return false
		}
		for _, name := range openAIUpstreamRequestBodyIntegrityHeaders {
			if strings.TrimSpace(c.Request.Header.Get(name)) != "" {
				return false
			}
		}
	}

	for name, value := range account.GetHeaderOverrides() {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if strings.EqualFold(name, "Content-Encoding") {
			return false
		}
		for _, integrityHeader := range openAIUpstreamRequestBodyIntegrityHeaders {
			if strings.EqualFold(name, integrityHeader) {
				return false
			}
		}
	}
	return true
}
