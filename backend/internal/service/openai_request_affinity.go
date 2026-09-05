package service

import (
	"context"
	"sync"
	"time"
)

// OpenAIRequestAffinityMaxAge releases account affinity after three minutes
// without imposing a deadline or canceling the in-flight upstream request.
const OpenAIRequestAffinityMaxAge = 3 * time.Minute

const openAIRequestAffinityCleanupTimeout = 3 * time.Second

type openAIRequestAffinityContextKey struct{}

type openAIRequestAffinityState struct {
	mu            sync.RWMutex
	detached      bool
	sessionHashes map[string]struct{}
	responseIDs   map[string]struct{}
}

// WithOpenAIRequestAffinity marks an HTTP Responses request as eligible for
// request-scoped affinity expiry. It carries state only; it never adds a
// deadline to the context.
func WithOpenAIRequestAffinity(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, openAIRequestAffinityContextKey{}, &openAIRequestAffinityState{
		sessionHashes: make(map[string]struct{}),
		responseIDs:   make(map[string]struct{}),
	})
}

func openAIRequestAffinityFromContext(ctx context.Context) *openAIRequestAffinityState {
	if ctx == nil {
		return nil
	}
	state, _ := ctx.Value(openAIRequestAffinityContextKey{}).(*openAIRequestAffinityState)
	return state
}

func withOpenAIRequestAffinityWrite(ctx context.Context, fn func() error) error {
	state := openAIRequestAffinityFromContext(ctx)
	if state == nil {
		return fn()
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.detached {
		return nil
	}
	return fn()
}

type openAIRequestAffinityBindings struct {
	sessionHashes []string
	responseIDs   []string
}

func detachOpenAIRequestAffinity(ctx context.Context) (openAIRequestAffinityBindings, bool) {
	state := openAIRequestAffinityFromContext(ctx)
	if state == nil {
		return openAIRequestAffinityBindings{}, false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.detached {
		return openAIRequestAffinityBindings{}, false
	}
	state.detached = true
	bindings := openAIRequestAffinityBindings{
		sessionHashes: make([]string, 0, len(state.sessionHashes)),
		responseIDs:   make([]string, 0, len(state.responseIDs)),
	}
	for sessionHash := range state.sessionHashes {
		bindings.sessionHashes = append(bindings.sessionHashes, sessionHash)
	}
	for responseID := range state.responseIDs {
		bindings.responseIDs = append(bindings.responseIDs, responseID)
	}
	return bindings, true
}

func trackOpenAIRequestAffinitySessionHash(ctx context.Context, sessionHash string) {
	state := openAIRequestAffinityFromContext(ctx)
	if state == nil || sessionHash == "" {
		return
	}
	state.sessionHashes[sessionHash] = struct{}{}
}

func trackOpenAIRequestAffinityResponseID(ctx context.Context, responseID string) {
	state := openAIRequestAffinityFromContext(ctx)
	if state == nil || responseID == "" {
		return
	}
	state.responseIDs[responseID] = struct{}{}
}

func (s *OpenAIGatewayService) bindOpenAIResponseAccount(ctx context.Context, store OpenAIWSStateStore, groupID int64, responseID string, accountID int64, ttl time.Duration) error {
	if store == nil {
		return nil
	}
	return withOpenAIRequestAffinityWrite(ctx, func() error {
		trackOpenAIRequestAffinityResponseID(ctx, responseID)
		return store.BindResponseAccount(ctx, groupID, responseID, accountID, ttl)
	})
}

func (s *OpenAIGatewayService) cleanupOpenAIRequestAffinity(sourceCtx context.Context, groupID int64, previousResponseID string, bindings openAIRequestAffinityBindings) {
	if sourceCtx == nil {
		sourceCtx = context.Background()
	}
	cleanupBase := context.WithoutCancel(sourceCtx)
	ctx, cancel := context.WithTimeout(cleanupBase, openAIRequestAffinityCleanupTimeout)
	defer cancel()
	for _, sessionHash := range bindings.sessionHashes {
		if sessionHash != "" {
			_ = s.deleteStickySessionAccountID(ctx, &groupID, sessionHash)
		}
	}
	store := s.getOpenAIWSStateStore()
	if store == nil {
		return
	}
	seen := make(map[string]struct{}, len(bindings.responseIDs)+1)
	for _, responseID := range append(bindings.responseIDs, previousResponseID) {
		if responseID == "" {
			continue
		}
		if _, exists := seen[responseID]; exists {
			continue
		}
		seen[responseID] = struct{}{}
		_ = store.DeleteResponseAccount(ctx, groupID, responseID)
	}
}

// DetachOpenAIRequestAffinity releases this request's routing bindings without
// affecting its context or downstream HTTP owner.
func (s *OpenAIGatewayService) DetachOpenAIRequestAffinity(ctx context.Context, groupID int64, sessionHash, previousResponseID string) bool {
	bindings, detached := detachOpenAIRequestAffinity(ctx)
	if !detached {
		return false
	}
	if sessionHash != "" {
		bindings.sessionHashes = append(bindings.sessionHashes, sessionHash)
	}
	s.cleanupOpenAIRequestAffinity(ctx, groupID, previousResponseID, bindings)
	return true
}

// StartOpenAIRequestAffinityExpiry releases routing bindings after three
// minutes while leaving the upstream request alive.
func (s *OpenAIGatewayService) StartOpenAIRequestAffinityExpiry(ctx context.Context, startedAt time.Time, groupID int64, sessionHash, previousResponseID string) func() {
	if openAIRequestAffinityFromContext(ctx) == nil {
		return func() {}
	}
	remaining := OpenAIRequestAffinityMaxAge - time.Since(startedAt)
	if remaining < 0 {
		remaining = 0
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(remaining)
		defer timer.Stop()
		select {
		case <-timer.C:
			bindings, detached := detachOpenAIRequestAffinity(ctx)
			if detached {
				if sessionHash != "" {
					bindings.sessionHashes = append(bindings.sessionHashes, sessionHash)
				}
				s.cleanupOpenAIRequestAffinity(ctx, groupID, previousResponseID, bindings)
			}
		case <-stop:
		}
	}()
	return func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
		<-done
	}
}
