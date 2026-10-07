package intelligence

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

type RecoveryProber interface {
	RunIntelligenceRecovery(context.Context, *RecoveryClaim) Record
}

type RecoveryRunner struct {
	Store           RecoveryStore
	Probe           RecoveryProber
	Template        func(context.Context) (Config, error)
	ctx             context.Context
	cancel          context.CancelFunc
	mu              sync.Mutex
	started, closed bool
	slots           chan struct{}
	wg              sync.WaitGroup
}

func NewRecoveryRunner(store RecoveryStore, probe RecoveryProber, template func(context.Context) (Config, error)) *RecoveryRunner {
	ctx, cancel := context.WithCancel(context.Background())
	return &RecoveryRunner{Store: store, Probe: probe, Template: template, ctx: ctx, cancel: cancel, slots: make(chan struct{}, 4)}
}

func (r *RecoveryRunner) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started || r.closed {
		return
	}
	r.started = true
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		tick := time.NewTicker(10 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-r.ctx.Done():
				return
			case <-tick.C:
				ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
				err := r.tick(ctx)
				cancel()
				if err != nil && !errors.Is(err, ErrConflict) && !errors.Is(err, ErrInvalid) && r.ctx.Err() == nil {
					slog.Warn("intelligence recovery scheduling failed", "error", err)
				}
			}
		}
	}()
}

func (r *RecoveryRunner) tick(ctx context.Context) error {
	if err := r.Store.Sync(ctx); err != nil {
		return err
	}
	c, err := r.Template(ctx)
	if err != nil {
		return err
	}
	for i := 0; i < cap(r.slots); i++ {
		if err = r.launch(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

func (r *RecoveryRunner) launch(ctx context.Context, c Config) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return context.Canceled
	}
	select {
	case r.slots <- struct{}{}:
	default:
		return ErrConflict
	}
	claim, err := r.Store.ClaimRecovery(ctx, c)
	if err != nil {
		<-r.slots
		return err
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer func() { <-r.slots }()
		result := r.Probe.RunIntelligenceRecovery(r.ctx, claim)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := r.Store.FinishRecovery(ctx, claim, result); err != nil && !errors.Is(err, ErrConflict) {
			slog.Warn("intelligence recovery result failed", "account_id", claim.AccountID, "error", err)
		}
	}()
	return nil
}

func (r *RecoveryRunner) Stop() {
	r.mu.Lock()
	r.closed = true
	r.cancel()
	r.mu.Unlock()
	r.wg.Wait()
}
