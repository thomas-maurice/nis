package middleware

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
	"github.com/thomas-maurice/nis/internal/infrastructure/logging"
)

// APITokenLastUsedFlusher coalesces last_used_at updates from the auth hot path.
//
// Without coalescing, every token-authed RPC would trigger one UPDATE per request
// — a burst of CI traffic against SQLite (1-connection pool) would serialize all
// reads behind those writes. With coalescing, we hold the most-recent timestamp
// per token in memory and flush every `interval`. The trade-off: last_used_at
// can lag by up to one interval. Acceptable for an audit signal.
//
// Single goroutine consumer; no per-request goroutines. Safe under concurrent
// Touch calls. Shuts down on Stop, draining the final batch before returning.
type APITokenLastUsedFlusher struct {
	repo     repositories.APITokenRepository
	interval time.Duration

	mu      sync.Mutex
	pending map[uuid.UUID]time.Time

	stopCh chan struct{}
	doneCh chan struct{}
}

// NewAPITokenLastUsedFlusher builds a flusher with the given cadence. Caller
// must invoke Start before use, and Stop on shutdown.
func NewAPITokenLastUsedFlusher(repo repositories.APITokenRepository, interval time.Duration) *APITokenLastUsedFlusher {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &APITokenLastUsedFlusher{
		repo:     repo,
		interval: interval,
		pending:  make(map[uuid.UUID]time.Time),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Touch records that the given token was used at ts. The actual DB write happens
// at the next flush tick. Coalescing: if Touch is called N times for the same
// token between ticks, only the latest ts is written.
func (f *APITokenLastUsedFlusher) Touch(id uuid.UUID, ts time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Hold the latest stamp; an out-of-order earlier stamp is dropped because the
	// later one is the correct "last seen".
	if existing, ok := f.pending[id]; ok && existing.After(ts) {
		return
	}
	f.pending[id] = ts
}

// Start runs the flush loop until Stop is called. Safe to call once.
func (f *APITokenLastUsedFlusher) Start(ctx context.Context) {
	go func() {
		defer close(f.doneCh)
		ticker := time.NewTicker(f.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				f.flush(context.Background())
				return
			case <-f.stopCh:
				f.flush(context.Background())
				return
			case <-ticker.C:
				f.flush(ctx)
			}
		}
	}()
}

// Stop signals the flusher to drain and exit. Blocks until the final flush completes.
func (f *APITokenLastUsedFlusher) Stop() {
	select {
	case <-f.stopCh:
		// already stopped
	default:
		close(f.stopCh)
	}
	<-f.doneCh
}

func (f *APITokenLastUsedFlusher) flush(ctx context.Context) {
	f.mu.Lock()
	if len(f.pending) == 0 {
		f.mu.Unlock()
		return
	}
	batch := f.pending
	f.pending = make(map[uuid.UUID]time.Time, len(batch))
	f.mu.Unlock()

	for id, ts := range batch {
		if err := f.repo.UpdateLastUsedAt(ctx, id, ts); err != nil {
			// Lossy by design: a failed last_used_at update is an audit signal
			// degradation, not a correctness failure. Log at debug to avoid spam.
			logging.GetLogger().Debug("api_token last_used_at flush failed",
				"token_id", id.String(), "error", err.Error())
		}
	}
}
