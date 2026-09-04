package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
)

const (
	openAIFirstOutputStageMemoryLimit        = 64 * 1024
	openAIFirstOutputStageMaxBytes           = 8 * 1024 * 1024
	openAIFirstOutputScannerFramingAllowance = 64
	openAIFirstOutputGuardQueueSize          = 1
	openAIDefaultStreamQueueSize             = 16
)

var (
	errOpenAIFirstOutputStageLimit   = errors.New("openai first-output staging limit exceeded")
	errOpenAIFirstOutputScannerLimit = errors.New("openai pre-output scanner token limit exceeded")
)

type openAIFirstOutputStage struct {
	limit      int64
	size       int64
	memory     bytes.Buffer
	tempFile   *os.File
	tempPath   string
	createTemp func() (*os.File, error)
	removeFile func(string) error
	memoryOnly bool
	cleanupErr error
	closed     bool
}

func newOpenAIFirstOutputStage(limit int64) *openAIFirstOutputStage {
	if limit < 1 {
		limit = 1
	}
	return &openAIFirstOutputStage{
		limit:      limit,
		createTemp: func() (*os.File, error) { return os.CreateTemp("", "sub2api-openai-first-output-*") },
		removeFile: os.Remove,
		memoryOnly: runtime.GOOS == "windows",
	}
}

func newDefaultOpenAIFirstOutputStage() *openAIFirstOutputStage {
	return newOpenAIFirstOutputStage(openAIFirstOutputStageMaxBytes)
}

func openAIFirstOutputEventQueueSize(guardFirstOutput bool) int {
	if guardFirstOutput {
		return openAIFirstOutputGuardQueueSize
	}
	return openAIDefaultStreamQueueSize
}

func openAIFirstOutputDynamicScanLines(guardActive *atomic.Bool) bufio.SplitFunc {
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		advance, token, err = bufio.ScanLines(data, atEOF)
		if err != nil || guardActive == nil || !guardActive.Load() {
			return advance, token, err
		}
		limit := openAIFirstOutputStageMaxBytes + openAIFirstOutputScannerFramingAllowance
		if token != nil {
			if len(token) > limit {
				return 0, nil, errOpenAIFirstOutputScannerLimit
			}
			return advance, token, nil
		}
		// At the limit with no delimiter, another byte would necessarily exceed
		// the guarded token budget. Fail before Scanner grows toward MaxLineSize.
		if len(data) >= limit {
			return 0, nil, errOpenAIFirstOutputScannerLimit
		}
		return advance, token, nil
	}
}

func (s *openAIFirstOutputStage) Buffered() int64 {
	if s == nil {
		return 0
	}
	return s.size
}

func (s *openAIFirstOutputStage) WriteString(value string) (int, error) {
	if err := s.prepareWrite(len(value)); err != nil {
		return 0, err
	}
	var n int
	var err error
	if s.tempFile == nil {
		n, err = s.memory.WriteString(value)
	} else {
		n, err = io.WriteString(s.tempFile, value)
	}
	s.size += int64(n)
	if err != nil {
		return n, fmt.Errorf("write first-output stage: %w", err)
	}
	return n, nil
}

func (s *openAIFirstOutputStage) Write(p []byte) (int, error) {
	if err := s.prepareWrite(len(p)); err != nil {
		return 0, err
	}
	var n int
	var err error
	if s.tempFile == nil {
		n, err = s.memory.Write(p)
	} else {
		n, err = s.tempFile.Write(p)
	}
	s.size += int64(n)
	if err != nil {
		return n, fmt.Errorf("write first-output stage: %w", err)
	}
	return n, nil
}

func (s *openAIFirstOutputStage) prepareWrite(incoming int) error {
	if s == nil || s.closed {
		return os.ErrClosed
	}
	if int64(incoming) > s.limit-s.size {
		return fmt.Errorf("%w: buffered=%d incoming=%d limit=%d", errOpenAIFirstOutputStageLimit, s.size, incoming, s.limit)
	}
	if s.tempFile != nil || s.memoryOnly || s.size+int64(incoming) <= openAIFirstOutputStageMemoryLimit {
		return nil
	}
	file, err := s.createTemp()
	if err != nil {
		return fmt.Errorf("create first-output spool: %w", err)
	}
	path := file.Name()
	// Unlink before writing any request data. Unix keeps the file descriptor
	// readable, while crashes and SIGKILL cannot leave a named plaintext spool.
	if unlinkErr := s.removeFile(path); unlinkErr != nil {
		closeErr := file.Close()
		removeErr := s.removeFile(path)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
		s.memoryOnly = true
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			s.tempPath = path
		}
		s.cleanupErr = errors.Join(
			s.cleanupErr,
			fmt.Errorf("unlink first-output spool before use: %w", unlinkErr),
			closeErr,
			removeErr,
		)
		return nil
	}
	if _, err := file.Write(s.memory.Bytes()); err != nil {
		_ = file.Close()
		return fmt.Errorf("initialize first-output spool: %w", err)
	}
	s.tempFile = file
	s.tempPath = path
	s.memory.Reset()
	return nil
}

func (s *openAIFirstOutputStage) CommitTo(dst io.Writer) error {
	if s == nil || s.closed {
		return os.ErrClosed
	}
	if s.tempFile == nil {
		if _, err := io.Copy(dst, bytes.NewReader(s.memory.Bytes())); err != nil {
			return err
		}
	} else {
		if _, err := s.tempFile.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("seek first-output spool: %w", err)
		}
		if _, err := io.CopyN(dst, s.tempFile, s.size); err != nil {
			return err
		}
	}
	if err := s.Close(); err != nil {
		// Delivery succeeded. Preserve cleanup failures for the handler's deferred
		// cleanup/logging pass instead of turning committed bytes into a stream error.
		s.cleanupErr = errors.Join(s.cleanupErr, err)
	}
	return nil
}

func (s *openAIFirstOutputStage) Close() error {
	if s == nil {
		return nil
	}
	if s.closed && s.tempFile == nil && s.tempPath == "" && s.cleanupErr == nil {
		return nil
	}
	s.closed = true
	s.size = 0
	s.memory.Reset()
	closeErr := s.cleanupErr
	s.cleanupErr = nil
	if s.tempFile != nil {
		closeErr = errors.Join(closeErr, s.tempFile.Close())
		s.tempFile = nil
	}
	if s.tempPath != "" {
		removeErr := s.removeFile(s.tempPath)
		if removeErr == nil || errors.Is(removeErr, os.ErrNotExist) {
			s.tempPath = ""
		} else {
			closeErr = errors.Join(closeErr, removeErr)
		}
	}
	return closeErr
}

func (s *OpenAIGatewayService) openAIFirstOutputTimeout(reasoningEffort string) time.Duration {
	if s == nil || s.cfg == nil || s.cfg.Gateway.OpenAIFirstOutputTimeoutSeconds <= 0 {
		return 0
	}
	seconds := s.cfg.Gateway.OpenAIFirstOutputTimeoutSeconds
	switch strings.ToLower(strings.TrimSpace(reasoningEffort)) {
	case "high", "xhigh", "max":
		if override := s.cfg.Gateway.OpenAIHighEffortFirstOutputTimeoutSeconds; override > 0 {
			seconds = override
		}
	}
	return time.Duration(seconds) * time.Second
}

// openAIFirstOutputDeadline returns the earliest configured first-output
// deadline and the account's next periodic scheduling pause. A periodic pause
// must affect requests that are already in flight, otherwise a stalled stream
// can keep the account pinned until the upstream's own timeout expires.
func (s *OpenAIGatewayService) openAIFirstOutputDeadline(account *Account, reasoningEffort string, now time.Time) (time.Time, time.Duration) {
	deadline := time.Time{}
	if timeout := s.openAIFirstOutputTimeout(reasoningEffort); timeout > 0 {
		deadline = now.Add(timeout)
	}
	if account != nil {
		status := account.PeriodicSchedulePauseStatusAt(now)
		if status.Enabled {
			periodicDeadline := now
			if !status.Paused && status.NextPauseAt != nil {
				periodicDeadline = *status.NextPauseAt
			}
			if deadline.IsZero() || periodicDeadline.Before(deadline) {
				deadline = periodicDeadline
			}
		}
	}
	if deadline.IsZero() {
		return time.Time{}, 0
	}
	timeout := deadline.Sub(now)
	if timeout <= 0 {
		timeout = time.Nanosecond
	}
	return deadline, timeout
}

// newOpenAIFirstOutputTimeoutError records the timeout as an upstream attempt
// and returns the failover error. proxyID/proxyName are supplied by the caller
// because the same deadline is enforced over HTTP and WebSocket transports,
// whose direct-route semantics differ (see opsUpstreamWSProxyAttribution).
func (s *OpenAIGatewayService) newOpenAIFirstOutputTimeoutError(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	proxyID *int64,
	proxyName string,
	startTime time.Time,
	originalModel string,
	reasoningEffort string,
	timeout time.Duration,
	phase string,
	responseHeaders http.Header,
) *UpstreamFailoverError {
	elapsed := time.Since(startTime)
	// Timer callbacks can run a few microseconds before the exact periodic
	// boundary. Classify from the calculated deadline as well as the current
	// wall clock so the externally visible reason remains deterministic.
	periodicPause := false
	if account != nil {
		now := time.Now()
		periodicPause = account.IsInPeriodicSchedulePause(now)
		if !periodicPause && timeout > 0 {
			deadlineStatus := account.PeriodicSchedulePauseStatusAt(startTime.Add(timeout).Add(time.Nanosecond))
			periodicPause = deadlineStatus.Enabled && deadlineStatus.Paused
		}
	}
	failureKind := "first_output_timeout"
	failureMessage := "OpenAI upstream produced no semantic output before the deadline"
	clientMessage := "Upstream produced no output before the deadline"
	if periodicPause {
		failureKind = "periodic_schedule_pause"
		failureMessage = "OpenAI account entered its periodic scheduling pause before semantic output"
		clientMessage = "Account entered its periodic scheduling pause before producing output"
	}
	logger.LegacyPrintf(
		"service.openai_gateway",
		"OpenAI first output deadline reached: account=%d model=%s effort=%s phase=%s reason=%s elapsed=%s limit=%s",
		account.ID, originalModel, reasoningEffort, phase, failureKind, elapsed, timeout,
	)
	requestID := strings.TrimSpace(responseHeaders.Get("x-request-id"))
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		ProxyID:   proxyID,
		ProxyName: proxyName,
		Platform:  account.Platform, AccountID: account.ID, AccountName: account.Name,
		UpstreamStatusCode: http.StatusGatewayTimeout, UpstreamRequestID: requestID,
		Kind: failureKind, Message: failureMessage,
		Detail: fmt.Sprintf("phase=%s reason=%s elapsed_ms=%d timeout_ms=%d", phase, failureKind, elapsed.Milliseconds(), timeout.Milliseconds()),
	})
	if !periodicPause && s.rateLimitService != nil {
		s.rateLimitService.HandleStreamTimeout(ctx, account, originalModel)
	}
	return &UpstreamFailoverError{
		StatusCode:      http.StatusGatewayTimeout,
		ResponseBody:    []byte(fmt.Sprintf(`{"error":{"type":%q,"message":%q}}`, failureKind, clientMessage)),
		ResponseHeaders: responseHeaders.Clone(), SafeToFailoverAfterWrite: true,
	}
}

type openAIFirstOutputHeaderGuard struct {
	cancel  context.CancelFunc
	release context.CancelFunc
	timer   *time.Timer
	fired   chan struct{}
	once    sync.Once
}

func newOpenAIFirstOutputHeaderGuard(
	ctx context.Context,
	release context.CancelFunc,
	deadline time.Time,
) (context.Context, *openAIFirstOutputHeaderGuard) {
	guardedCtx, cancel := context.WithCancel(ctx)
	guard := &openAIFirstOutputHeaderGuard{cancel: cancel, release: release, fired: make(chan struct{})}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		remaining = time.Nanosecond
	}
	guard.timer = time.AfterFunc(remaining, func() {
		close(guard.fired)
		cancel()
	})
	return guardedCtx, guard
}

func (g *openAIFirstOutputHeaderGuard) stopHeaderWait() bool {
	if g.timer.Stop() {
		return false
	}
	<-g.fired
	return true
}

func (g *openAIFirstOutputHeaderGuard) close() {
	g.once.Do(func() {
		g.timer.Stop()
		g.cancel()
		g.release()
	})
}

type openAIRequestContextReadCloser struct {
	io.ReadCloser
	cleanup func()
	once    sync.Once
	err     error
}

func (r *openAIRequestContextReadCloser) Close() error {
	r.once.Do(func() {
		r.cleanup()
		r.err = r.ReadCloser.Close()
	})
	return r.err
}

// openAIFirstOutputBodyGuard closes an upstream response body at the first
// output deadline, but stops that timer once semantic output has started. It
// is used by the passthrough scanner, which otherwise blocks synchronously on
// Read and cannot observe a timer through a select loop.
type openAIFirstOutputBodyGuard struct {
	io.ReadCloser
	mu        sync.Mutex
	timer     *time.Timer
	semantic  bool
	fired     bool
	closeOnce sync.Once
	closeErr  error
}

func newOpenAIFirstOutputBodyGuard(body io.ReadCloser, deadline time.Time) *openAIFirstOutputBodyGuard {
	guard := &openAIFirstOutputBodyGuard{ReadCloser: body}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		remaining = time.Nanosecond
	}
	guard.timer = time.AfterFunc(remaining, guard.fire)
	return guard
}

func (g *openAIFirstOutputBodyGuard) fire() {
	g.mu.Lock()
	if g.semantic {
		g.mu.Unlock()
		return
	}
	g.fired = true
	g.mu.Unlock()
	_ = g.closeBody()
}

func (g *openAIFirstOutputBodyGuard) MarkSemanticOutput() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.semantic = true
	if g.timer != nil {
		g.timer.Stop()
	}
	g.mu.Unlock()
}

func (g *openAIFirstOutputBodyGuard) Fired() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	fired := g.fired
	g.mu.Unlock()
	return fired
}

func (g *openAIFirstOutputBodyGuard) Close() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	if g.timer != nil {
		g.timer.Stop()
	}
	g.mu.Unlock()
	return g.closeBody()
}

func (g *openAIFirstOutputBodyGuard) closeBody() error {
	g.closeOnce.Do(func() {
		g.mu.Lock()
		body := g.ReadCloser
		g.mu.Unlock()
		if body != nil {
			g.closeErr = body.Close()
		}
	})
	return g.closeErr
}
