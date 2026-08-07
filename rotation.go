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
// Returns the acquired Proxy, whether the caller is the FIRST holder (running went
// 0→1), and an error if none available. The first-holder flag decides who is allowed
// to rotate the IP: a later slot must not swap the address out from under a peer
// that is already browsing on it.
func (pm *ProxyManager) acquireAvailableProxy(maxUsed, slots, threadID int) (*Proxy, bool, error) {
	maxUsed = effectiveMaxUsed(maxUsed)
	slots = effectiveSlots(slots)
	nowUnix := time.Now().Unix()
	now := time.Now()

	tx, err := pm.db.Begin()
	if err != nil {
		return nil, false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// SELECT + UPDATE in single transaction — atomic acquire.
	// `running < slots` replaces the old `running=0`: with slots=1 the predicate
	// is identical, above 1 it lets N handles share one upstream. Ordering by
	// running spreads load across the pool instead of stacking on the first row.
	row := tx.QueryRow(`
		SELECT id, type, proxy_str, api_key, change_url, min_time, running, used, is_unique,
		       last_ip, last_changed, error, created_at, updated_at
		FROM proxies
		WHERE (
			(is_unique = 0)
			OR (type = 'static' AND running < ? AND used < ?)
			OR (type = 'mobilehop' AND running < ?)
			OR (type = 'auto' AND running < ?)
			OR (type NOT IN ('static','mobilehop','auto') AND is_unique=1 AND running < ? AND (
				used < ?
				OR (min_time = 0 OR (last_changed IS NULL OR (?-last_changed >= min_time)))
			))
		)
		ORDER BY CASE WHEN is_unique=0 THEN 0 ELSE 1 END, running ASC, used ASC, id ASC
		LIMIT 1
	`, slots, maxUsed, slots, slots, slots, maxUsed, nowUnix)

	var p Proxy
	var apiKey, changeUrl, lastIP, errStr sql.NullString
	var lastChangedUnix sql.NullInt64
	if err := row.Scan(
		&p.ID, &p.Type, &p.ProxyStr, &apiKey, &changeUrl, &p.MinTime,
		&p.Running, &p.Used, &p.Unique, &lastIP, &lastChangedUnix, &errStr,
		&p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, false, fmt.Errorf("no available proxy")
		}
		return nil, false, fmt.Errorf("scan proxy: %w", err)
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

	// For unique proxies, atomically claim a slot within the same transaction.
	// p.Running still holds the pre-increment value read above, so 0 identifies
	// the first holder.
	first := p.Running == 0
	if p.Unique {
		if _, err := tx.Exec(
			`UPDATE proxies SET running=running+1, thread_id=?, updated_at=? WHERE id=?`,
			threadID, now, p.ID,
		); err != nil {
			return nil, false, fmt.Errorf("acquire proxy in tx: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit tx: %w", err)
	}

	// Sync cache after successful commit
	if p.Unique {
		pm.mu.Lock()
		if cached, ok := pm.cache[p.ID]; ok {
			cached.Running++
			cached.UpdatedAt = now
		}
		pm.mu.Unlock()
	}

	return &p, first, nil
}

// queryAvailableProxy selects a single candidate proxy from DB without holding any mutex.
// Used internally by acquireAvailableProxy and useful for testing the selection query.
func (pm *ProxyManager) queryAvailableProxy(maxUsed, slots int) (*Proxy, error) {
	maxUsed = effectiveMaxUsed(maxUsed)
	slots = effectiveSlots(slots)
	nowUnix := time.Now().Unix()
	row := pm.db.QueryRow(`
		SELECT id, type, proxy_str, api_key, change_url, min_time, running, used, is_unique,
		       last_ip, last_changed, error, created_at, updated_at
		FROM proxies
		WHERE (
			(is_unique = 0)
			OR (type = 'static' AND running < ? AND used < ?)
			OR (type = 'mobilehop' AND running < ?)
			OR (type = 'auto' AND running < ?)
			OR (type NOT IN ('static','mobilehop','auto') AND is_unique=1 AND running < ? AND (
				used < ?
				OR (min_time = 0 OR (last_changed IS NULL OR (?-last_changed >= min_time)))
			))
		)
		ORDER BY CASE WHEN is_unique=0 THEN 0 ELSE 1 END, running ASC, used ASC, id ASC
		LIMIT 1
	`, slots, maxUsed, slots, slots, slots, maxUsed, nowUnix)

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

// joinRotatedProxy resolves the upstream for a slot that is NOT the first holder
// of a proxy (Limit > 1). It never contacts the provider: the address is whatever
// the first holder settled on. `used` still advances so N parallel slots spend the
// per-IP budget N times, keeping max_used an honest count of hits on one IP.
//
// The proxy is re-read from cache rather than trusting the transaction snapshot,
// because the first holder may have written a fresh proxy_str between the two.
func (pm *ProxyManager) joinRotatedProxy(p *Proxy) (string, error) {
	proxyStr := p.ProxyStr
	lastErr := p.Error
	if cached, err := pm.getProxyByID(p.ID); err == nil {
		proxyStr = cached.ProxyStr
		lastErr = cached.Error
	}

	if proxyStr == "" {
		if lastErr != "" {
			return "", fmt.Errorf("no proxy_str available, last rotation error: %s", lastErr)
		}
		return "", fmt.Errorf("no proxy_str available for proxy %d", p.ID)
	}

	// Sticky strings are templates carrying a {random} session tag — leaving it
	// unsubstituted would hand the caller a literal "{random}" as an upstream.
	if p.Type == ProxyTypeSticky {
		proxyStr = processStickyProxyStr(proxyStr)
	}

	pm.incrementUsed(p.ID, time.Now())
	return proxyStr, nil
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
