package service

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strings"
	"sync"

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

	return &openAIUpstreamRequestBody{
		body:          newOpenAIStreamingGzipBody(body),
		contentLength: -1,
		gzipped:       true,
		getBody: func() (io.ReadCloser, error) {
			return newOpenAIStreamingGzipBody(body), nil
		},
	}, nil
}

type openAIStreamingGzipBody struct {
	reader    *io.PipeReader
	writer    *io.PipeWriter
	source    []byte
	startOnce sync.Once
	closeOnce sync.Once
	done      chan struct{}
}

func newOpenAIStreamingGzipBody(source []byte) *openAIStreamingGzipBody {
	reader, writer := io.Pipe()
	return &openAIStreamingGzipBody{
		reader: reader,
		writer: writer,
		source: source,
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
	writer, err := gzip.NewWriterLevel(b.writer, gzip.BestSpeed)
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
	close(b.done)
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
