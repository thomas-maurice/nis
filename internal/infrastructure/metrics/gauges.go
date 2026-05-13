package metrics

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/thomas-maurice/nis/internal/infrastructure/logging"
	"github.com/thomas-maurice/nis/internal/infrastructure/persistence"
)

// InventoryFetcher is the small surface area the gauge cache needs. The
// RepositoryFactory implements it, but tests can supply a fake.
type InventoryFetcher interface {
	Inventory(ctx context.Context) (persistence.Inventory, error)
}

// domainCache holds the most recently fetched inventory. Reads happen at Prom
// scrape time inside an OTel callback and must be lock-free.
type domainCache struct {
	v atomic.Pointer[persistence.Inventory]
}

func (c *domainCache) store(inv persistence.Inventory) {
	c.v.Store(&inv)
}

func (c *domainCache) load() persistence.Inventory {
	if p := c.v.Load(); p != nil {
		return *p
	}
	return persistence.Inventory{}
}

// DomainGauges registers ObservableGauges that publish entity counts. The
// caller must keep RefreshLoop running (or call Refresh directly) — otherwise
// every gauge will read zero.
type DomainGauges struct {
	cache   *domainCache
	fetcher InventoryFetcher
}

// RegisterDomainGauges builds the ObservableGauges and wires them to the cache.
// Returns the DomainGauges handle so callers can drive Refresh manually.
func RegisterDomainGauges(fetcher InventoryFetcher) (*DomainGauges, error) {
	dg := &DomainGauges{
		cache:   &domainCache{},
		fetcher: fetcher,
	}

	m := otel.Meter(scope)
	gauges := []struct {
		name string
		desc string
		read func(persistence.Inventory) int64
	}{
		{"nis_operators_total", "Number of operators in the database.", func(i persistence.Inventory) int64 { return i.Operators }},
		{"nis_accounts_total", "Number of accounts in the database.", func(i persistence.Inventory) int64 { return i.Accounts }},
		{"nis_users_total", "Number of users in the database.", func(i persistence.Inventory) int64 { return i.Users }},
		{"nis_scoped_keys_total", "Number of scoped signing keys in the database.", func(i persistence.Inventory) int64 { return i.ScopedKeys }},
		{"nis_clusters_total", "Number of clusters in the database.", func(i persistence.Inventory) int64 { return i.Clusters }},
		{"nis_clusters_healthy", "Number of clusters last reported healthy by the 60s health-check loop.", func(i persistence.Inventory) int64 { return i.ClustersHealthy }},
	}

	instruments := make([]metric.Int64ObservableGauge, 0, len(gauges))
	for _, g := range gauges {
		inst, err := m.Int64ObservableGauge(g.name, metric.WithDescription(g.desc))
		if err != nil {
			return nil, fmt.Errorf("register gauge %s: %w", g.name, err)
		}
		instruments = append(instruments, inst)
	}

	// Single batch callback so all six gauges read from the same cache snapshot.
	_, err := m.RegisterCallback(func(_ context.Context, obs metric.Observer) error {
		inv := dg.cache.load()
		for i, g := range gauges {
			obs.ObserveInt64(instruments[i], g.read(inv))
		}
		return nil
	}, asObservables(instruments)...)
	if err != nil {
		return nil, fmt.Errorf("register gauge callback: %w", err)
	}

	return dg, nil
}

func asObservables(gs []metric.Int64ObservableGauge) []metric.Observable {
	out := make([]metric.Observable, len(gs))
	for i, g := range gs {
		out[i] = g
	}
	return out
}

// Refresh fetches the latest inventory and stores it in the cache. Failures are
// logged and the previous value is retained.
func (dg *DomainGauges) Refresh(ctx context.Context) {
	inv, err := dg.fetcher.Inventory(ctx)
	if err != nil {
		logging.LogFromContext(ctx).Warn("metrics: inventory refresh failed", "error", err)
		return
	}
	dg.cache.store(inv)
}

// RefreshLoop refreshes the cache every interval until ctx is done. Designed
// for a single background goroutine — do not start more than one per Provider.
func (dg *DomainGauges) RefreshLoop(ctx context.Context, interval time.Duration) {
	// Prime once before the first tick so /metrics returns real data within
	// seconds of startup.
	dg.Refresh(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			dg.Refresh(ctx)
		}
	}
}
