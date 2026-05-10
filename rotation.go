package multiproxy

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// effectiveMaxUsed returns a large value when maxUsed=0 (unlimited),
// preventing `used < 0` from disabling all unique proxies.
func effectiveMaxUsed(maxUsed int) int {
	if maxUsed <= 0 {
		return 1<<31 - 1 // ~2 billion — effectively unlimited
	}
	return maxUsed
}

// acquireAvailableProxy atomically selects and acquires a proxy using a DB transaction.
// This prevents TOCTOU race conditions where two goroutines select the same proxy.
// Returns the acquired Proxy or an error if none available.
func (pm *ProxyManager) acquireAvailableProxy(maxUsed int, threadID int) (*Proxy, error) {
	maxUsed = effectiveMaxUsed(maxUsed)
	nowUnix := time.Now().Unix()
	now := time.Now()

	tx, err := pm.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// SELECT + UPDATE in single transaction — atomic acquire
	row := tx.QueryRow(`
		SELECT id, type, proxy_str, api_key, change_url, min_time, running, used, is_unique,
		       last_ip, last_changed, error, created_at, updated_at
		FROM proxies
		WHERE (
			(is_unique = 0)
			OR (type = 'static' AND running=0 AND used < ?)
			OR (type = 'mobilehop' AND running=0)
			OR (type = 'auto' AND running=0)
			OR (type NOT IN ('static','mobilehop','auto') AND is_unique=1 AND running=0 AND (
				used < ?
				OR (min_time = 0 OR (last_changed IS NULL OR (?-last_changed >= min_time)))
			))
		)
		ORDER BY CASE WHEN is_unique=0 THEN 0 ELSE 1 END, used ASC, id ASC
		LIMIT 1
	`, maxUsed, maxUsed, nowUnix)

	var p Proxy
	var apiKey, changeUrl, lastIP, errStr sql.NullString
	var lastChangedUnix sql.NullInt64
	if err := row.Scan(
		&p.ID, &p.Type, &p.ProxyStr, &apiKey, &changeUrl, &p.MinTime,
		&p.Running, &p.Used, &p.Unique, &lastIP, &lastChangedUnix, &errStr,
		&p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no available proxy")
		}
		return nil, fmt.Errorf("scan proxy: %w", err)
	}
	if apiKey.Valid {
		p.ApiKey = apiKey.String
	}
	if changeUrl.Valid {
		p.ChangeUrl = changeUrl.String
	}
	if lastIP.Valid {
		p.LastIP = lastIP.String
	}
	if lastChangedUnix.Valid {
		p.LastChanged = time.Unix(lastChangedUnix.Int64, 0)
	}
	if errStr.Valid {
		p.Error = errStr.String
	}

	// For unique proxies, atomically set running=1 within same transaction
	if p.Unique {
		if _, err := tx.Exec(
			`UPDATE proxies SET running=1, thread_id=?, updated_at=? WHERE id=?`,
			threadID, now, p.ID,
		); err != nil {
			return nil, fmt.Errorf("acquire proxy in tx: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	// Sync cache after successful commit
	if p.Unique {
		pm.mu.Lock()
		if cached, ok := pm.cache[p.ID]; ok {
			cached.Running = true
			cached.UpdatedAt = now
		}
		pm.mu.Unlock()
	}

	return &p, nil
}

// queryAvailableProxy selects a single candidate proxy from DB without holding any mutex.
// Used internally by acquireAvailableProxy and useful for testing the selection query.
func (pm *ProxyManager) queryAvailableProxy(maxUsed int) (*Proxy, error) {
	maxUsed = effectiveMaxUsed(maxUsed)
	nowUnix := time.Now().Unix()
	row := pm.db.QueryRow(`
		SELECT id, type, proxy_str, api_key, change_url, min_time, running, used, is_unique,
		       last_ip, last_changed, error, created_at, updated_at
		FROM proxies
		WHERE (
			(is_unique = 0)
			OR (type = 'static' AND running=0 AND used < ?)
			OR (type = 'mobilehop' AND running=0)
			OR (type = 'auto' AND running=0)
			OR (type NOT IN ('static','mobilehop','auto') AND is_unique=1 AND running=0 AND (
				used < ?
				OR (min_time = 0 OR (last_changed IS NULL OR (?-last_changed >= min_time)))
			))
		)
		ORDER BY CASE WHEN is_unique=0 THEN 0 ELSE 1 END, used ASC, id ASC
		LIMIT 1
	`, maxUsed, maxUsed, nowUnix)

	var p Proxy
	var apiKey, changeUrl, lastIP, errStr sql.NullString
	var lastChangedUnix sql.NullInt64
	if err := row.Scan(
		&p.ID, &p.Type, &p.ProxyStr, &apiKey, &changeUrl, &p.MinTime,
		&p.Running, &p.Used, &p.Unique, &lastIP, &lastChangedUnix, &errStr,
		&p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no available proxy")
		}
		return nil, fmt.Errorf("scan proxy: %w", err)
	}
	if apiKey.Valid {
		p.ApiKey = apiKey.String
	}
	if changeUrl.Valid {
		p.ChangeUrl = changeUrl.String
	}
	if lastIP.Valid {
		p.LastIP = lastIP.String
	}
	if lastChangedUnix.Valid {
		p.LastChanged = time.Unix(lastChangedUnix.Int64, 0)
	}
	if errStr.Valid {
		p.Error = errStr.String
	}
	return &p, nil
}

// rotateProxy applies type-specific rotation logic and returns the resolved proxy string.
// The mutex MUST NOT be held when calling this function.
func (pm *ProxyManager) rotateProxy(ctx context.Context, p *Proxy) (string, error) {
	now := time.Now()
	canChangeIP := p.MinTime == 0 || time.Since(p.LastChanged).Seconds() >= float64(p.MinTime)

	// Sticky unique: change ${random} session tag
	if p.Type == ProxyTypeSticky && p.Unique {
		return pm.rotateStickyUnique(p, now, canChangeIP)
	}

	// TMProxy
	if p.Type == ProxyTypeTMProxy && canChangeIP && p.ApiKey != "" {
		return pm.rotateViaProvider(ctx, p, now, ProxyTypeTMProxy, nil)
	}

	// MobileHop: always rotate via change_url (ignores canChangeIP)
	if p.Type == ProxyTypeMobileHop && p.ChangeUrl != "" {
		return pm.rotateMobileHop(ctx, p, now)
	}

	// KiotProxy
	if p.Type == ProxyTypeKiotProxy && canChangeIP && p.ApiKey != "" {
		opts := map[string]string{}
		if p.ChangeUrl != "" {
			opts["region"] = p.ChangeUrl
		}
		return pm.rotateViaProvider(ctx, p, now, ProxyTypeKiotProxy, opts)
	}

	// IPv4Xoay
	if p.Type == ProxyTypeIPv4Xoay && canChangeIP && p.ApiKey != "" {
		return pm.rotateIPv4Xoay(ctx, p, now)
	}

	// Default: not eligible for rotation — just increment used (except Auto).
	// If proxy_str is empty AND a previous rotation left an error in DB,
	// surface that error: returning "" + nil here would cascade into a
	// downstream "connection refused" because there's no upstream to forward
	// to. The stale error is the most informative diagnostic available.
	if p.ProxyStr == "" && p.Error != "" {
		return "", fmt.Errorf("no proxy_str available, last rotation error: %s", p.Error)
	}
	if p.Type != ProxyTypeAuto {
		pm.incrementUsed(p.ID, now)
	}
	return p.ProxyStr, nil
}
