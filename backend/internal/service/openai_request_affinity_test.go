package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIRequestAffinityDetachCleansBindingsAndKeepsHTTPOwner(t *testing.T) {
	cache := &stubGatewayCache{}
	svc := &OpenAIGatewayService{cache: cache}
	groupID := int64(77)
	sessionHash, legacyHash := deriveOpenAISessionHashes("session-77")
	ctx, cancel := context.WithCancel(withOpenAILegacySessionHash(WithOpenAIRequestAffinity(context.Background()), legacyHash))
	defer cancel()

	require.NoError(t, svc.setStickySessionAccountID(ctx, &groupID, sessionHash, 101, time.Hour))
	store := svc.getOpenAIWSStateStore()
	require.NoError(t, svc.bindOpenAIResponseAccount(ctx, store, groupID, "resp-current", 101, time.Hour))
	require.NoError(t, store.BindResponseAccount(ctx, groupID, "resp-previous", 101, time.Hour))
	require.NoError(t, store.BindHTTPResponseOwner(ctx, groupID, "resp-current", 500, 600, time.Hour))

	bindings, detached := detachOpenAIRequestAffinity(ctx)
	require.True(t, detached)
	cancel()
	svc.cleanupOpenAIRequestAffinity(ctx, groupID, "resp-previous", bindings)
	verifyCtx := context.Background()

	_, ok := cache.sessionBindings[svc.openAISessionCacheKey(sessionHash)]
	require.False(t, ok)
	_, ok = cache.sessionBindings["openai:"+legacyHash]
	require.False(t, ok, "legacy session binding must be removed too")
	accountID, err := store.GetResponseAccount(verifyCtx, groupID, "resp-current")
	require.NoError(t, err)
	require.Zero(t, accountID)
	accountID, err = store.GetResponseAccount(verifyCtx, groupID, "resp-previous")
	require.NoError(t, err)
	require.Zero(t, accountID)
	userID, apiKeyID, found, err := store.GetHTTPResponseOwner(verifyCtx, groupID, "resp-current")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(500), userID)
	require.Equal(t, int64(600), apiKeyID)

	require.NoError(t, svc.setStickySessionAccountID(ctx, &groupID, sessionHash, 202, time.Hour))
	_, ok = cache.sessionBindings[svc.openAISessionCacheKey(sessionHash)]
	require.False(t, ok, "detached request must not recreate session affinity")
}

func TestOpenAIRequestAffinityExpiryDoesNotCancelContext(t *testing.T) {
	ctx, cancel := context.WithCancel(WithOpenAIRequestAffinity(context.Background()))
	defer cancel()

	stop := (&OpenAIGatewayService{}).StartOpenAIRequestAffinityExpiry(
		ctx,
		time.Now().Add(-OpenAIRequestAffinityMaxAge),
		1,
		"session-hash",
		"",
	)
	require.Eventually(t, func() bool {
		state := openAIRequestAffinityFromContext(ctx)
		state.mu.RLock()
		defer state.mu.RUnlock()
		return state.detached
	}, time.Second, time.Millisecond)
	stop()

	require.NoError(t, ctx.Err())
}
