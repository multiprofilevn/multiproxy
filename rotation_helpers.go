package multiproxy

import (
	"context"
	"fmt"
	"time"
)

// rotateStickyUnique handles rotation for sticky proxies with is_unique=1.
// If canChangeIP: reset used=1 and update last_changed.
// Otherwise: increment used.
func (pm *ProxyManager) rotateStickyUnique(p *Proxy, now time.Time, canChangeIP bool) (string, error) {
	if canChangeIP {
		// DB write outside lock
		pm.db.Exec(`UPDATE proxies SET last_changed=?, used=1, error='', updated_at=? WHERE id=?`,
			now.Unix(), now, p.ID)
		pm.mu.Lock()
		if cached, ok := pm.cache[p.ID]; ok {
			cached.LastChanged = now
			cached.Used = 1
			cached.Error = ""
			cached.UpdatedAt = now
		}
		pm.mu.Unlock()
	} else {
		pm.incrementUsed(p.ID, now)
	}
	return processStickyProxyStr(p.ProxyStr), nil
}

// rotateViaProvider calls the provider's GetNewProxy, handles errors, and updates DB/cache on success.
// Used by TMProxy and KiotProxy.
func (pm *ProxyManager) rotateViaProvider(ctx context.Context, p *Proxy, now time.Time, pType ProxyType, opts map[string]string) (string, error) {
	prov, ok := pm.providers[pType]
	if !ok {
		return "", fmt.Errorf("no provider registered for %s", pType)
	}

	result, err := prov.GetNewProxy(ctx, p.ApiKey, opts)
	if err != nil {
		errMsg := fmt.Sprintf("GetNewProxy failed: %v", err)
		pm.markError(p.ID, errMsg, now)
		return "", fmt.Errorf("%s", errMsg)
	}
	if !result.Success {
		errMsg := fmt.Sprintf("%s api error: %s", pType, result.Error)
		pm.markError(p.ID, errMsg, now)
		return "", fmt.Errorf("%s", errMsg)
	}

	newProxyStr := result.ProxyStr
	pm.updateAfterRotation(p.ID, newProxyStr, now)
	pm.restartDumbProxyInstance(p.ID, newProxyStr)
	pm.sleepChangeWait()
	return newProxyStr, nil
}

// rotateMobileHop calls the change_url webhook for MobileHop proxies.
func (pm *ProxyManager) rotateMobileHop(ctx context.Context, p *Proxy, now time.Time) (string, error) {
	if err := pm.callChangeURL(ctx, p.ChangeUrl); err != nil {
		errMsg := fmt.Sprintf("callChangeURL failed: %v", err)
		// Release proxy (running=0, no error field set) — DB write outside lock
		pm.db.Exec(`UPDATE proxies SET running=0, thread_id=NULL, updated_at=? WHERE id=?`, now, p.ID)
		pm.mu.Lock()
		if cached, ok := pm.cache[p.ID]; ok {
			cached.Running = false
			cached.UpdatedAt = now
		}
		pm.mu.Unlock()
		return "", fmt.Errorf("%s", errMsg)
	}

	// DB write outside lock
	pm.db.Exec(`UPDATE proxies SET last_changed=?, used=1, error='', updated_at=? WHERE id=?`,
		now.Unix(), now, p.ID)
	pm.mu.Lock()
	if cached, ok := pm.cache[p.ID]; ok {
		cached.LastChanged = now
		cached.Used = 1
		cached.Error = ""
		cached.UpdatedAt = now
	}
	pm.mu.Unlock()

	pm.sleepChangeWait()
	return p.ProxyStr, nil
}

// rotateIPv4Xoay handles IPv4Xoay-specific rotation including blocked (status 101) case.
func (pm *ProxyManager) rotateIPv4Xoay(ctx context.Context, p *Proxy, now time.Time) (string, error) {
	prov, ok := pm.providers[ProxyTypeIPv4Xoay]
	if !ok {
		return "", fmt.Errorf("no provider registered for ipv4xoay")
	}

	result, err := prov.GetNewProxy(ctx, p.ApiKey, nil)
	if err != nil {
		errMsg := fmt.Sprintf("GetNewProxy failed: %v", err)
		pm.markError(p.ID, errMsg, now)
		return "", fmt.Errorf("%s", errMsg)
	}

	// Blocked (status 101): release without setting error, caller should retry
	if result.Blocked {
		// DB write outside lock
		pm.db.Exec(`UPDATE proxies SET running=0, thread_id=NULL, updated_at=? WHERE id=?`, now, p.ID)
		pm.mu.Lock()
		if cached, ok := pm.cache[p.ID]; ok {
			cached.Running = false
			cached.UpdatedAt = now
		}
		pm.mu.Unlock()
		return "", fmt.Errorf("blocking status 101, retry later")
	}

	newProxyStr := result.ProxyStr
	pm.updateAfterRotation(p.ID, newProxyStr, now)
	pm.restartDumbProxyInstance(p.ID, newProxyStr)
	pm.sleepChangeWait()
	return newProxyStr, nil
}

// markError sets error, running=0, thread_id=NULL in DB then syncs cache.
func (pm *ProxyManager) markError(id int64, errMsg string, now time.Time) {
	// DB write outside lock
	pm.db.Exec(`UPDATE proxies SET error=?, running=0, thread_id=NULL, updated_at=? WHERE id=?`,
		errMsg, now, id)
	pm.mu.Lock()
	if cached, ok := pm.cache[id]; ok {
		cached.Error = errMsg
		cached.Running = false
		cached.ThreadID = 0
		cached.UpdatedAt = now
	}
	pm.mu.Unlock()
}

// incrementUsed increments used counter in DB then syncs cache.
func (pm *ProxyManager) incrementUsed(id int64, now time.Time) {
	// DB write outside lock
	pm.db.Exec(`UPDATE proxies SET used=used+1, updated_at=? WHERE id=?`, now, id)
	pm.mu.Lock()
	if cached, ok := pm.cache[id]; ok {
		cached.Used++
		cached.UpdatedAt = now
	}
	pm.mu.Unlock()
}

// updateAfterRotation updates proxy_str, last_changed, resets used=1, clears error.
func (pm *ProxyManager) updateAfterRotation(id int64, newProxyStr string, now time.Time) {
	// DB write outside lock
	pm.db.Exec(`UPDATE proxies SET proxy_str=?, last_changed=?, used=1, error='', updated_at=? WHERE id=?`,
		newProxyStr, now.Unix(), now, id)
	pm.mu.Lock()
	if cached, ok := pm.cache[id]; ok {
		cached.ProxyStr = newProxyStr
		cached.LastChanged = now
		cached.Used = 1
		cached.Error = ""
		cached.UpdatedAt = now
	}
	pm.mu.Unlock()
}

// sleepChangeWait pauses for the configured changeProxyWaitTime if set.
func (pm *ProxyManager) sleepChangeWait() {
	if pm.changeProxyWaitTime > 0 {
		time.Sleep(pm.changeProxyWaitTime)
	}
}
