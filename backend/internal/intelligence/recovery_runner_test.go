package intelligence

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

type recoveryRunnerStore struct {
	claims, finishes atomic.Int32
}

func (*recoveryRunnerStore) Sync(context.Context) error { return nil }
func (s *recoveryRunnerStore) ClaimRecovery(_ context.Context, c Config) (*RecoveryClaim, error) {
	return &RecoveryClaim{AccountID: int64(s.claims.Add(1)), Config: c}, nil
}
func (s *recoveryRunnerStore) FinishRecovery(context.Context, *RecoveryClaim, Record) error {
	s.finishes.Add(1)
	return nil
}

type recoveryBlockingProbe struct{}

func (recoveryBlockingProbe) RunIntelligenceRecovery(ctx context.Context, c *RecoveryClaim) Record {
	<-ctx.Done()
	return Record{AccountID: c.AccountID, Status: "error"}
}

func TestIntelligenceRecoveryRunnerBoundedAndStops(t *testing.T) {
	s := &recoveryRunnerStore{}
	r := NewRecoveryRunner(s, recoveryBlockingProbe{}, func(context.Context) (Config, error) { return DefaultConfig(79), nil })
	require.NoError(t, r.tick(context.Background()))
	require.ErrorIs(t, r.tick(context.Background()), ErrConflict)
	require.EqualValues(t, 4, s.claims.Load())
	r.Stop()
	require.EqualValues(t, 4, s.finishes.Load())
	require.ErrorIs(t, r.launch(context.Background(), DefaultConfig(79)), context.Canceled)
}
