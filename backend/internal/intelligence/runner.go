package intelligence

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Runner has its own bounded pool, database leases and lifecycle. Actual
// IntervalMinutes is authoritative; public display copy never controls requests.
type Runner struct {
	Store   Store
	Probe   Prober
	Enabled func(context.Context) bool
	ctx     context.Context
	cancel  context.CancelFunc
	slots   chan struct{}
	mu      sync.Mutex
	closed  bool
	started bool
	wg      sync.WaitGroup
}

func NewRunner(store Store, probe Prober, enabled func(context.Context) bool) *Runner {
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{Store: store, Probe: probe, Enabled: enabled, ctx: ctx, cancel: cancel, slots: make(chan struct{}, 4)}
}
func (r *Runner) Start() {
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
		prune := time.NewTicker(time.Hour)
		defer prune.Stop()
		legacyReconciled := false
		for {
			select {
			case <-r.ctx.Done():
				return
			case <-tick.C:
				ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
				if err := r.reconcileGroupEligibility(ctx); err != nil {
					slog.Warn("intelligence group eligibility reconciliation failed", "error", err)
				} else if r.Enabled == nil || r.Enabled(ctx) {
					legacyReconciled = r.reconcileProtection(r.ctx, legacyReconciled)
					for i := 0; i < cap(r.slots); i++ {
						if err := r.launch(ctx, ProbeKey{}, false); err != nil {
							if !errors.Is(err, ErrConflict) && ctx.Err() == nil {
								slog.Warn("intelligence check scheduling failed", "error", err)
							}
							break
						}
					}
				}
				cancel()
			case <-prune.C:
				ctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
				if err := r.Store.Prune(ctx); err != nil && ctx.Err() == nil {
					slog.Warn("intelligence history cleanup failed", "error", err)
				}
				cancel()
			}
		}
	}()
}

func (r *Runner) reconcileGroupEligibility(ctx context.Context) error {
	if reconciler, ok := r.Store.(interface{ ReconcileGroupEligibility(context.Context) error }); ok {
		return reconciler.ReconcileGroupEligibility(ctx)
	}
	return nil
}

func (r *Runner) reconcileProtection(ctx context.Context, legacyReconciled bool) bool {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if !legacyReconciled {
		if reconciler, ok := r.Store.(interface{ ReconcileLegacyProtection(context.Context) error }); ok {
			if err := reconciler.ReconcileLegacyProtection(ctx); err != nil {
				slog.Warn("intelligence legacy pause reconciliation failed", "error", err)
				return false
			}
		}
	}
	if reconciler, ok := r.Store.(interface{ ReconcileProtectionScopes(context.Context) error }); ok {
		if err := reconciler.ReconcileProtectionScopes(ctx); err != nil {
			slog.Warn("intelligence protection scope reconciliation failed", "error", err)
		}
	}
	return true
}

func (r *Runner) RunNow(ctx context.Context, key ProbeKey) error {
	if key.GroupID <= 0 || key.ProbeID < 0 {
		return ErrInvalid
	}
	if err := r.reconcileGroupEligibility(ctx); err != nil {
		return err
	}
	if r.Enabled != nil && !r.Enabled(ctx) {
		return errors.New("V2 intelligence checks are disabled")
	}
	return r.launch(ctx, key, true)
}
func (r *Runner) launch(ctx context.Context, key ProbeKey, manual bool) error {
	// Serialize Stop/Add to avoid WaitGroup.Add racing with shutdown.
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
	claimCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	claim, err := r.Store.Claim(claimCtx, key, manual)
	if err != nil {
		<-r.slots
		return err
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer func() { <-r.slots }()
		record := r.Probe.Run(r.ctx, claim.Config)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := r.Store.Finish(ctx, claim, record); err != nil {
			slog.Error("intelligence result persistence failed", "group_id", claim.Config.GroupID, "probe_id", claim.Config.ProbeID, "error", err)
		}
	}()
	return nil
}
func (r *Runner) Stop() { r.mu.Lock(); r.closed = true; r.cancel(); r.mu.Unlock(); r.wg.Wait() }
