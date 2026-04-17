package multiproxy

import (
	"context"
	"fmt"
	"time"
)

// loadProxiesFromList parses proxy definition strings, calls provider APIs for
// API-based types to fetch the initial proxy address, then upserts each entry
// into the database. Returns the slice of inserted/updated IDs.
func (pm *ProxyManager) loadProxiesFromList(proxyStrings []string) ([]int64, error) {
	ids := make([]int64, 0, len(proxyStrings))

	for _, raw := range proxyStrings {
		res, err := parseProxyString(raw)
		if err != nil {
			return nil, err
		}

		lastChanged := time.Time{}
		var proxyError string

		// --- TMProxy ---
		if res.pType == ProxyTypeTMProxy && res.apiKey != "" {
			prov, ok := pm.providers[ProxyTypeTMProxy]
			if ok {
				cur, err := prov.GetCurrentProxy(context.Background(), res.apiKey)
				needNew := err != nil || cur == nil || cur.Blocked || !cur.Success
				if !needNew && cur != nil {
					res.proxyStr = cur.ProxyStr
					// lastChanged is approximate; set to zero so caller computes from minTime
				}
				if needNew {
					nr, err2 := prov.GetNewProxy(context.Background(), res.apiKey, nil)
					if err2 != nil {
						proxyError = fmt.Sprintf("GetNewProxy failed: %v", err2)
					} else if nr == nil || !nr.Success {
						if nr != nil {
							proxyError = nr.Error
						} else {
							proxyError = "GetNewProxy returned nil"
						}
					} else {
						res.proxyStr = nr.ProxyStr
					}
					lastChanged = time.Now()
				}
			}
		}

		// --- KiotProxy ---
		if res.pType == ProxyTypeKiotProxy && res.apiKey != "" {
			prov, ok := pm.providers[ProxyTypeKiotProxy]
			if ok {
				opts := map[string]string{"region": res.changeUrl}
				cur, err := prov.GetCurrentProxy(context.Background(), res.apiKey)
				needNew := err != nil || cur == nil || !cur.Success
				if !needNew && cur != nil {
					res.proxyStr = cur.ProxyStr
				}
				if needNew {
					nr, err2 := prov.GetNewProxy(context.Background(), res.apiKey, opts)
					if err2 != nil {
						proxyError = fmt.Sprintf("GetNewProxy failed: %v", err2)
					} else if nr == nil || !nr.Success {
						if nr != nil {
							proxyError = nr.Error
						} else {
							proxyError = "GetNewProxy returned nil"
						}
					} else {
						res.proxyStr = nr.ProxyStr
					}
					lastChanged = time.Now()
				}
			}
		}

		// --- IPv4Xoay ---
		if res.pType == ProxyTypeIPv4Xoay && res.apiKey != "" {
			prov, ok := pm.providers[ProxyTypeIPv4Xoay]
			if ok {
				cur, err := prov.GetCurrentProxy(context.Background(), res.apiKey)
				if err != nil {
					proxyError = fmt.Sprintf("GetCurrentProxy failed: %v", err)
				} else if cur == nil || cur.Blocked {
					// Blocked (status 101) — leave proxyStr empty, retry later
				} else if cur.Success {
					res.proxyStr = cur.ProxyStr
				}
				lastChanged = time.Now()
			}
		}

		// All non-API types and fallback: set lastChanged to now if still zero
		if lastChanged.IsZero() {
			lastChanged = time.Now()
		}

		uniqueKey := uniqueKeyFor(res)

		id, err := pm.upsertProxy(
			res.pType, res.proxyStr, res.apiKey, res.changeUrl,
			res.minTime, uniqueKey, res.unique, lastChanged, proxyError,
		)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}

	return ids, nil
}

// upsertProxy inserts a new proxy row or updates an existing one matched by
// unique_key. Returns the row ID. Cache is updated under lock after the DB write.
func (pm *ProxyManager) upsertProxy(
	pType ProxyType,
	proxyStr, apiKey, changeUrl string,
	minTime int,
	uniqueKey string,
	unique bool,
	lastChanged time.Time,
	proxyError string,
) (int64, error) {
	now := time.Now()

	result, err := pm.db.Exec(
		`INSERT OR IGNORE INTO proxies
		 (type, proxy_str, api_key, unique_key, min_time, change_url, is_unique, last_changed, error, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		pType, proxyStr, apiKey, uniqueKey, minTime, changeUrl,
		unique, lastChanged.Unix(), proxyError, now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("upsertProxy insert: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected > 0 {
		id, _ := result.LastInsertId()
		entry := &Proxy{
			ID:          id,
			Type:        pType,
			ProxyStr:    proxyStr,
			ApiKey:      apiKey,
			ChangeUrl:   changeUrl,
			MinTime:     minTime,
			Unique:      unique,
			LastChanged: lastChanged,
			Error:       proxyError,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		pm.mu.Lock()
		pm.cache[id] = entry
		pm.mu.Unlock()
		return id, nil
	}

	// Conflict on unique_key — update mutable fields
	pm.db.Exec(
		`UPDATE proxies
		 SET proxy_str=?, min_time=?, change_url=?, is_unique=?, last_changed=?, error=?, updated_at=?
		 WHERE unique_key=?`,
		proxyStr, minTime, changeUrl, unique, lastChanged.Unix(), proxyError, now, uniqueKey,
	)

	var id int64
	if err2 := pm.db.QueryRow(`SELECT id FROM proxies WHERE unique_key=?`, uniqueKey).Scan(&id); err2 != nil {
		return 0, fmt.Errorf("upsertProxy select id: %w", err2)
	}

	pm.mu.Lock()
	if cached, ok := pm.cache[id]; ok {
		cached.ProxyStr = proxyStr
		cached.MinTime = minTime
		cached.ChangeUrl = changeUrl
		cached.Unique = unique
		cached.LastChanged = lastChanged
		cached.Error = proxyError
		cached.UpdatedAt = now
	} else {
		pm.cache[id] = &Proxy{
			ID:          id,
			Type:        pType,
			ProxyStr:    proxyStr,
			ApiKey:      apiKey,
			ChangeUrl:   changeUrl,
			MinTime:     minTime,
			Unique:      unique,
			LastChanged: lastChanged,
			Error:       proxyError,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
	}
	pm.mu.Unlock()

	return id, nil
}
