package multiproxy

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/tuwibu/multiproxy/provider"
)

// ProxyManager is the main entry point for the multiproxy library.
// Create with New(config), use Acquire/Release pattern for proxy access.
type ProxyManager struct {
	db                  *sql.DB
	mu                  sync.RWMutex
	cache               map[int64]*Proxy
	instances           *InstanceManager
	providers           map[ProxyType]provider.Provider
	changeProxyWaitTime time.Duration
	maxUsed             int
	slots               int // concurrent handles allowed per unique proxy (Config.Limit)
	isBlockAssets       bool
	ctx                 context.Context    // cancelled on Close()
	cancel              context.CancelFunc // triggers ctx cancellation
}

// New creates a ProxyManager, initializes the database, loads proxies,
// and starts local proxy instances if IsBlockAssets is enabled.
func New(config Config) (*ProxyManager, error) {
	dbPath := config.DBPath
	if dbPath == "" {
		dbPath = "proxy.db"
	}

	db, err := initDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("init db: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	pm := &ProxyManager{
		db:                  db,
		cache:               make(map[int64]*Proxy),
		instances:           NewInstanceManager(),
		providers:           initProviders(),
		changeProxyWaitTime: config.ChangeProxyWaitTime,
		maxUsed:             config.MaxUsed,
		slots:               config.Limit,
		isBlockAssets:       config.IsBlockAssets,
		ctx:                 ctx,
		cancel:              cancel,
	}

	if err := pm.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	if err := pm.Reconfigure(config); err != nil {
		db.Close()
		return nil, fmt.Errorf("reconfigure: %w", err)
	}

	return pm, nil
}

// initProviders creates one instance of each known proxy provider.
func initProviders() map[ProxyType]provider.Provider {
	return map[ProxyType]provider.Provider{
		ProxyTypeTMProxy:   provider.NewTMProxy(),
		ProxyTypeKiotProxy: provider.NewKiotProxy(),
		ProxyTypeIPv4Xoay:  provider.NewIPv4Xoay(),
	}
}

// Acquire selects an available proxy, rotates it if needed, and returns
// a ProxyHandle. The caller MUST call handle.Release() when done.
func (pm *ProxyManager) Acquire(ctx context.Context, threadID int) (*ProxyHandle, error) {
	// Merge caller context with manager context so Close() cancels in-flight Acquires
	ctx, cancel := mergeContexts(ctx, pm.ctx)
	defer cancel()

	// 1. Atomically select + acquire proxy (no TOCTOU race)
	p, first, err := pm.acquireAvailableProxy(pm.maxUsed, pm.slots, threadID)
	if err != nil {
		return nil, err
	}

	// 2. Non-unique: process and return immediately (no running flag)
	if !p.Unique {
		proxyStr := p.ProxyStr
		if p.Type == ProxyTypeSticky {
			proxyStr = processStickyProxyStr(proxyStr)
		}
		if err := pm.ensureInstanceAlive(p.ID, proxyStr); err != nil {
			return nil, err
		}
		return &ProxyHandle{
			ID:       p.ID,
			Type:     p.Type,
			Addr:     pm.getConnectionString(p.ID, proxyStr),
			Upstream: proxyStr,
			manager:  pm,
		}, nil
	}

	// 3. Rotate if needed — release proxy on failure to prevent permanent lock.
	// Only the first holder rotates. Under Limit > 1 the later slots join an IP
	// a peer is already browsing on; rotating it here would yank the address out
	// from under that peer mid-session. They share the address and still count
	// against `used`, so the per-IP usage budget is spent by all slots together.
	var proxyStr string
	if first {
		proxyStr, err = pm.rotateProxy(ctx, p)
	} else {
		proxyStr, err = pm.joinRotatedProxy(p)
	}
	if err != nil {
		pm.releaseProxy(p.ID) // ensure cleanup on rotation failure
		return nil, err
	}

	if err := pm.ensureInstanceAlive(p.ID, proxyStr); err != nil {
		pm.releaseProxy(p.ID)
		return nil, err
	}

	return &ProxyHandle{
		ID:       p.ID,
		Type:     p.Type,
		Addr:     pm.getConnectionString(p.ID, proxyStr),
		Upstream: proxyStr,
		manager:  pm,
	}, nil
}

// ensureInstanceAlive guarantees a local listener exists for proxyID when
// isBlockAssets is on. Without this, Acquire could return Addr=127.0.0.1:port
// for a port nobody is listening on (e.g. cooldown path returned a stale
// proxy_str and rotation never restarted the instance), causing the caller
// to see a misleading "connection refused" instead of the real upstream
// error. No-op when isBlockAssets is off.
func (pm *ProxyManager) ensureInstanceAlive(proxyID int64, proxyStr string) error {
	if !pm.isBlockAssets || pm.instances == nil {
		return nil
	}
	if pm.instances.Has(proxyID) {
		return nil
	}
	if proxyStr == "" {
		// Read DB error column to surface the most informative diagnostic.
		pm.mu.RLock()
		errMsg := ""
		if cached, ok := pm.cache[proxyID]; ok {
			errMsg = cached.Error
		}
		pm.mu.RUnlock()
		if errMsg != "" {
			return fmt.Errorf("local instance for proxy %d not running and no proxy_str (last error: %s)", proxyID, errMsg)
		}
		return fmt.Errorf("local instance for proxy %d not running and no proxy_str", proxyID)
	}
	return pm.restartDumbProxyInstance(proxyID, proxyStr)
}

// Reconfigure updates config at runtime: reloads proxy strings and restarts instances.
func (pm *ProxyManager) Reconfigure(config Config) error {
	pm.mu.Lock()
	pm.changeProxyWaitTime = config.ChangeProxyWaitTime
	pm.maxUsed = config.MaxUsed
	pm.slots = config.Limit
	blockAssetsChanged := pm.isBlockAssets != config.IsBlockAssets
	pm.isBlockAssets = config.IsBlockAssets
	pm.mu.Unlock()

	// Stop all instances if clearing or block-assets mode changed
	if config.ClearAllProxy || blockAssetsChanged {
		pm.instances.StopAll()
	}

	if config.ClearAllProxy {
		pm.deleteAllProxies()
	} else {
		pm.resetAllProxies()
	}

	ids, err := pm.loadProxiesFromList(config.ProxyStrings)
	if err != nil {
		return fmt.Errorf("load proxies: %w", err)
	}

	// Start local proxy instances if block-assets mode is enabled
	if config.IsBlockAssets {
		for _, id := range ids {
			pm.mu.RLock()
			p, ok := pm.cache[id]
			pm.mu.RUnlock()
			if ok && p.ProxyStr != "" {
				if _, err := pm.instances.StartInstance(id, p.ProxyStr); err != nil {
					fmt.Printf("[multiproxy] failed to start instance for proxy %d: %v\n", id, err)
				}
			}
		}
	}

	return nil
}

// Close cancels in-flight operations, stops all instances, and closes the database.
func (pm *ProxyManager) Close() error {
	pm.cancel() // cancel in-flight Acquire calls
	pm.instances.StopAll()
	return pm.db.Close()
}

// mergeContexts returns a context that is cancelled when either parent is cancelled.
func mergeContexts(a, b context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(a)
	go func() {
		select {
		case <-b.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// ProxyStatus holds the current status of a proxy for health checking.
type ProxyStatus struct {
	ID           int64
	Type         ProxyType
	Running      bool // any handle outstanding
	RunningCount int  // how many, under Limit > 1
	Used         int
	Error        string
}

// HealthCheck returns the status of all proxies.
func (pm *ProxyManager) HealthCheck() []ProxyStatus {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	statuses := make([]ProxyStatus, 0, len(pm.cache))
	for _, p := range pm.cache {
		statuses = append(statuses, ProxyStatus{
			ID:           p.ID,
			Type:         p.Type,
			Running:      p.Running > 0,
			RunningCount: p.Running,
			Used:         p.Used,
			Error:        p.Error,
		})
	}
	return statuses
}

// ProxyInspect is the full row snapshot returned by Inspect() — superset of
// ProxyStatus, intended for debug/admin UIs. Contains raw proxy_str which may
// embed credentials; do not log or expose externally.
type ProxyInspect struct {
	ID          int64
	Type        ProxyType
	ProxyStr     string
	Running      bool // any handle outstanding
	RunningCount int  // how many, under Limit > 1
	Used         int
	Unique       bool
	ThreadID    int
	LastIP      string
	Error       string
	LastChanged time.Time
	UpdatedAt   time.Time
	CreatedAt   time.Time
}

// Inspect returns a snapshot of every proxy row in cache, sorted by ID.
// Cache stays in lock-step with the DB via updateProxyFields/releaseProxy,
// so this is the canonical view without paying for a SQL round-trip.
func (pm *ProxyManager) Inspect() []ProxyInspect {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	out := make([]ProxyInspect, 0, len(pm.cache))
	for _, p := range pm.cache {
		out = append(out, ProxyInspect{
			ID:          p.ID,
			Type:        p.Type,
			ProxyStr:     p.ProxyStr,
			Running:      p.Running > 0,
			RunningCount: p.Running,
			Used:         p.Used,
			Unique:       p.Unique,
			ThreadID:    p.ThreadID,
			LastIP:      p.LastIP,
			Error:       p.Error,
			LastChanged: p.LastChanged,
			UpdatedAt:   p.UpdatedAt,
			CreatedAt:   p.CreatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
