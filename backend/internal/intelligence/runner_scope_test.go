package intelligence

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
)

type scopeReconcileStore struct {
	Store
	legacyCalls int
	scopeCalls  int
	legacyErr   error
	scopeErr    error
}

func (s *scopeReconcileStore) ReconcileLegacyProtection(context.Context) error {
	s.legacyCalls++
	return s.legacyErr
}
func (s *scopeReconcileStore) ReconcileProtectionScopes(context.Context) error {
	s.scopeCalls++
	return s.scopeErr
}

func TestIntelligenceRunnerReconcilesRenamedGroupsAfterStartup(t *testing.T) {
	s := &scopeReconcileStore{}
	r := NewRunner(s, nil, nil)
	defer r.Stop()
	reconciled := r.reconcileProtection(context.Background(), false)
	require.True(t, reconciled)
	require.True(t, r.reconcileProtection(context.Background(), reconciled))
	require.Equal(t, 1, s.legacyCalls)
	require.Equal(t, 2, s.scopeCalls)
	t.Log("startup reconciliation runs once; changed scopes are checked on every enabled tick")
}

func TestIntelligenceRunnerRetriesProtectionScopeFailures(t *testing.T) {
	s := &scopeReconcileStore{legacyErr: errors.New("fixture startup failure")}
	r := NewRunner(s, nil, nil)
	defer r.Stop()
	require.False(t, r.reconcileProtection(context.Background(), false))
	require.Zero(t, s.scopeCalls)
	s.legacyErr = nil
	s.scopeErr = errors.New("fixture scope failure")
	require.True(t, r.reconcileProtection(context.Background(), false))
	s.scopeErr = nil
	require.True(t, r.reconcileProtection(context.Background(), true))
	require.Equal(t, 2, s.legacyCalls)
	require.Equal(t, 2, s.scopeCalls)
}
