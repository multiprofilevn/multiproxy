package multiproxy

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// initDB opens the SQLite database with WAL mode and busy_timeout pragma.
//
// _txlock=immediate makes db.Begin() emit BEGIN IMMEDIATE, so the read-then-write
// transaction in acquireAvailableProxy takes the write lock up front. Without it, a
// deferred transaction that SELECTs then UPDATEs must upgrade to a writer mid-flight;
// if another connection wrote to the WAL after the read snapshot, SQLite returns
// SQLITE_BUSY immediately (deadlock avoidance) and busy_timeout is NOT honored.
//
// SetMaxOpenConns(1) serializes all access through a single connection, eliminating
// writer-vs-writer contention entirely — safe here because writes are sub-millisecond
// and throughput is bounded by the small proxy pool.
func initDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return db, nil
}

// initSchema creates the proxies table and applies column migrations.
func (pm *ProxyManager) initSchema() error {
	_, err := pm.db.Exec(`
	CREATE TABLE IF NOT EXISTS proxies (
		id INTEGER PRIMARY KEY,
		type TEXT NOT NULL,
		proxy_str TEXT,
		api_key TEXT,
		unique_key TEXT UNIQUE,
		min_time INTEGER,
		change_url TEXT,
		running INTEGER DEFAULT 0,
		used INTEGER DEFAULT 0,
		is_unique INTEGER DEFAULT 0,
		last_changed INTEGER,
		last_ip TEXT,
		error TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_type ON proxies(type);
	CREATE INDEX IF NOT EXISTS idx_unique_key ON proxies(unique_key);
	`)
	if err != nil {
		return fmt.Errorf("create table: %w", err)
	}

	// Migrations — errors are intentionally ignored (columns may already exist)
	pm.db.Exec(`ALTER TABLE proxies ADD COLUMN is_unique INTEGER DEFAULT 0`)
	pm.db.Exec(`UPDATE proxies SET is_unique=1 WHERE type IN ('tmproxy','mobilehop','static','kiotproxy','auto','ipv4xoay')`)
	pm.db.Exec(`ALTER TABLE proxies ADD COLUMN thread_id INTEGER`)

	return nil
}

// releaseProxy sets running=false and clears thread_id for the given proxy ID.
// Implements the stub declared in manager.go.
func (pm *ProxyManager) releaseProxy(id int64) error {
	now := time.Now()
	if _, err := pm.db.Exec(`UPDATE proxies SET running=0, thread_id=NULL, updated_at=? WHERE id=?`, now, id); err != nil {
		return fmt.Errorf("releaseProxy id=%d: %w", id, err)
	}

	pm.mu.Lock()
	if p, ok := pm.cache[id]; ok {
		p.Running = false
		p.UpdatedAt = now
	}
	pm.mu.Unlock()
	return nil
}

// resetAllProxies clears used/running/error fields for every proxy.
func (pm *ProxyManager) resetAllProxies() {
	now := time.Now()
	pm.db.Exec(`UPDATE proxies SET used=0, running=0, error='', updated_at=?`, now)

	pm.mu.Lock()
	for _, p := range pm.cache {
		p.Used = 0
		p.Running = false
		p.Error = ""
		p.UpdatedAt = now
	}
	pm.mu.Unlock()
}

// deleteAllProxies removes every proxy from the table and cache.
func (pm *ProxyManager) deleteAllProxies() {
	pm.db.Exec(`DELETE FROM proxies`)

	pm.mu.Lock()
	pm.cache = make(map[int64]*Proxy)
	pm.mu.Unlock()
}

// getProxyByID returns a proxy from cache if present, otherwise reads from DB.
func (pm *ProxyManager) getProxyByID(id int64) (*Proxy, error) {
	pm.mu.RLock()
	if p, ok := pm.cache[id]; ok {
		pm.mu.RUnlock()
		return p, nil
	}
	pm.mu.RUnlock()

	// Fallback: query DB (no lock held during I/O)
	row := pm.db.QueryRow(`
		SELECT id, type, proxy_str, api_key, change_url, min_time,
		       running, used, is_unique, last_ip, last_changed, error, thread_id,
		       created_at, updated_at
		FROM proxies WHERE id=?`, id)

	p, err := scanProxy(row)
	if err != nil {
		return nil, err
	}

	pm.mu.Lock()
	pm.cache[p.ID] = p
	pm.mu.Unlock()
	return p, nil
}

// updateProxyFields performs a generic column update on a single proxy row
// and syncs the cache entry. Only string, int, int64, bool, time.Time values supported.
func (pm *ProxyManager) updateProxyFields(id int64, fields map[string]interface{}) error {
	if len(fields) == 0 {
		return nil
	}
	cols := make([]string, 0, len(fields)+1)
	vals := make([]interface{}, 0, len(fields)+1)
	for col, val := range fields {
		cols = append(cols, col+"=?")
		vals = append(vals, val)
	}
	// Always bump updated_at
	cols = append(cols, "updated_at=?")
	vals = append(vals, time.Now())
	vals = append(vals, id)

	query := "UPDATE proxies SET " + strings.Join(cols, ", ") + " WHERE id=?"
	if _, err := pm.db.Exec(query, vals...); err != nil {
		return fmt.Errorf("updateProxyFields id=%d: %w", id, err)
	}

	pm.mu.Lock()
	if p, ok := pm.cache[id]; ok {
		for col, val := range fields {
			applyFieldToProxy(p, col, val)
		}
		p.UpdatedAt = time.Now()
	}
	pm.mu.Unlock()
	return nil
}

// getConnectionString returns localhost:port when IsBlockAssets is true,
// otherwise returns the raw proxy string.
func (pm *ProxyManager) getConnectionString(proxyID int64, proxyStr string) string {
	if pm.isBlockAssets {
		return fmt.Sprintf("127.0.0.1:%d", BasePort+int(proxyID))
	}
	return proxyStr
}

// restartDumbProxyInstance restarts the local dumbproxy instance with a new upstream.
// No-op when isBlockAssets is false. Returns the StartInstance error so callers
// can decide whether to surface it; previously this was silently swallowed which
// made dead-listener bugs invisible to the calling app.
func (pm *ProxyManager) restartDumbProxyInstance(proxyID int64, newProxyStr string) error {
	if !pm.isBlockAssets || pm.instances == nil {
		return nil
	}
	if _, err := pm.instances.StartInstance(proxyID, newProxyStr); err != nil {
		fmt.Printf("[multiproxy] restart instance proxyID=%d failed: %v\n", proxyID, err)
		return fmt.Errorf("start instance proxyID=%d: %w", proxyID, err)
	}
	return nil
}

// callChangeURL performs a GET request to the mobilehop webhook URL.
func (pm *ProxyManager) callChangeURL(ctx context.Context, changeURL string) error {
	if changeURL == "" {
		return fmt.Errorf("changeURL is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, changeURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("call change_url: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("change_url returned status %d", resp.StatusCode)
	}
	return nil
}

// generateRandomString returns a hex-encoded random string of the given length.
func generateRandomString(length int) string {
	b := make([]byte, length/2+1)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))[:length]
	}
	return hex.EncodeToString(b)[:length]
}

// processStickyProxyStr replaces {random} / ${random} placeholders in the username
// field of an ip:port:username:password proxy string.
func processStickyProxyStr(proxyStr string) string {
	parts := strings.Split(proxyStr, ":")
	if len(parts) >= 3 {
		username := parts[2]
		if strings.Contains(username, "${random}") || strings.Contains(username, "{random}") {
			r := generateRandomString(8)
			username = strings.ReplaceAll(username, "${random}", r)
			username = strings.ReplaceAll(username, "{random}", r)
			parts[2] = username
			return strings.Join(parts, ":")
		}
	}
	return proxyStr
}

// uniqueKeyFor computes the MD5 unique key for a parsed proxy entry.
func uniqueKeyFor(res *proxyParseResult) string {
	switch res.pType {
	case ProxyTypeTMProxy, ProxyTypeKiotProxy, ProxyTypeIPv4Xoay:
		return fmt.Sprintf("%x", md5.Sum([]byte(res.apiKey)))
	case ProxyTypeSticky:
		// Use the original (un-randomised) proxy string as the key
		return fmt.Sprintf("%x", md5.Sum([]byte(res.proxyStr)))
	default:
		return fmt.Sprintf("%x", md5.Sum([]byte(res.proxyStr)))
	}
}

// scanProxy scans a single proxies row into a Proxy struct.
func scanProxy(row *sql.Row) (*Proxy, error) {
	var p Proxy
	var apiKey, changeUrl, lastIP, errStr sql.NullString
	var lastChangedUnix sql.NullInt64
	var threadID sql.NullInt64
	err := row.Scan(
		&p.ID, &p.Type, &p.ProxyStr, &apiKey, &changeUrl,
		&p.MinTime, &p.Running, &p.Used, &p.Unique,
		&lastIP, &lastChangedUnix, &errStr, &threadID,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
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
	if threadID.Valid {
		p.ThreadID = int(threadID.Int64)
	}
	return &p, nil
}

// applyFieldToProxy maps a column name to the matching Proxy field.
func applyFieldToProxy(p *Proxy, col string, val interface{}) {
	switch col {
	case "proxy_str":
		if s, ok := val.(string); ok {
			p.ProxyStr = s
		}
	case "error":
		if s, ok := val.(string); ok {
			p.Error = s
		}
	case "running":
		switch v := val.(type) {
		case bool:
			p.Running = v
		case int:
			p.Running = v != 0
		}
	case "used":
		if v, ok := val.(int); ok {
			p.Used = v
		}
	case "last_changed":
		switch v := val.(type) {
		case int64:
			p.LastChanged = time.Unix(v, 0)
		case time.Time:
			p.LastChanged = v
		}
	case "last_ip":
		if s, ok := val.(string); ok {
			p.LastIP = s
		}
	case "thread_id":
		switch v := val.(type) {
		case int:
			p.ThreadID = v
		case int64:
			p.ThreadID = int(v)
		}
	}
}
