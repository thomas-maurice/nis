package middleware

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/thomas-maurice/nis/internal/application/authz"
	"github.com/thomas-maurice/nis/internal/domain/entities"
	"github.com/thomas-maurice/nis/internal/domain/repositories"
)

// fakeAPITokenRepo records UpdateLastUsedAt calls so the test can assert that
// the flusher coalesces — N touches between flushes turn into ONE write.
type fakeAPITokenRepo struct {
	mu      sync.Mutex
	updates map[uuid.UUID][]time.Time
}

func (f *fakeAPITokenRepo) UpdateLastUsedAt(ctx context.Context, id uuid.UUID, ts time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updates == nil {
		f.updates = make(map[uuid.UUID][]time.Time)
	}
	f.updates[id] = append(f.updates[id], ts)
	return nil
}

func (f *fakeAPITokenRepo) Create(context.Context, *entities.APIToken) error { return nil }
func (f *fakeAPITokenRepo) GetByID(context.Context, uuid.UUID) (*entities.APIToken, error) {
	return nil, nil
}
func (f *fakeAPITokenRepo) GetByHash(context.Context, string) (*entities.APIToken, error) {
	return nil, nil
}
func (f *fakeAPITokenRepo) List(context.Context, repositories.APITokenFilter) ([]*entities.APIToken, error) {
	return nil, nil
}
func (f *fakeAPITokenRepo) ListPage(context.Context, authz.Scope, repositories.APITokenListFilter) ([]*entities.APIToken, string, error) {
	return nil, "", nil
}
func (f *fakeAPITokenRepo) Revoke(context.Context, uuid.UUID, time.Time) error { return nil }
func (f *fakeAPITokenRepo) Delete(context.Context, uuid.UUID) error            { return nil }

func TestFlusher_CoalescesMultipleTouches(t *testing.T) {
	repo := &fakeAPITokenRepo{}
	// 50ms interval so the test finishes quickly while still asserting that
	// many touches inside one window collapse to one write.
	f := NewAPITokenLastUsedFlusher(repo, 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.Start(ctx)
	defer f.Stop()

	id := uuid.New()
	base := time.Now()
	for i := 0; i < 10; i++ {
		f.Touch(id, base.Add(time.Duration(i)*time.Millisecond))
	}

	// Wait long enough for at least one flush tick to fire.
	time.Sleep(120 * time.Millisecond)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	updates := repo.updates[id]
	require.NotEmpty(t, updates,
		"flusher should have written at least one update after the interval")
	require.LessOrEqual(t, len(updates), 2,
		"coalescing should keep the write count bounded; 10 touches must not produce 10 writes")
	require.Equal(t, base.Add(9*time.Millisecond), updates[0],
		"the latest touch must win — earlier stamps are dropped")
}

func TestFlusher_StopDrainsPending(t *testing.T) {
	repo := &fakeAPITokenRepo{}
	// Long interval so the natural ticker doesn't fire — only Stop's final
	// drain should write the pending row.
	f := NewAPITokenLastUsedFlusher(repo, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.Start(ctx)

	id := uuid.New()
	f.Touch(id, time.Now())
	f.Stop()

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.updates[id], 1,
		"Stop must drain pending touches before returning")
}
