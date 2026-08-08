package multiproxy

import (
	"testing"
	"time"
)

// TestParseProxyString_TMProxy tests parsing of tmproxy format.
func TestParseProxyString_TMProxy(t *testing.T) {
	tests := []struct {
		input    string
		wantType ProxyType
		wantKey  string
		wantErr  bool
	}{
		{
			input:    "tmproxy|mykey",
			wantType: ProxyTypeTMProxy,
			wantKey:  "mykey",
		},
		{
			input:    "tmproxy|mykey|300",
			wantType: ProxyTypeTMProxy,
			wantKey:  "mykey",
		},
		{
			input:    "tmproxy|mykey|300|https://example.com/change",
			wantType: ProxyTypeTMProxy,
			wantKey:  "mykey",
		},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			res, err := parseProxyString(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseProxyString error: %v", err)
			}
			if res.pType != tt.wantType {
				t.Errorf("got type %s, want %s", res.pType, tt.wantType)
			}
			if res.apiKey != tt.wantKey {
				t.Errorf("got apiKey %s, want %s", res.apiKey, tt.wantKey)
			}
		})
	}
}

// TestParseProxyString_Static tests parsing of static format (contains colon).
func TestParseProxyString_Static(t *testing.T) {
	input := "static|192.168.1.1:8080:user:pass"
	res, err := parseProxyString(input)
	if err != nil {
		t.Fatalf("parseProxyString error: %v", err)
	}
	if res.pType != ProxyTypeStatic {
		t.Errorf("got type %s, want static", res.pType)
	}
	if res.proxyStr != "192.168.1.1:8080:user:pass" {
		t.Errorf("got proxyStr %s", res.proxyStr)
	}
	if res.unique != true {
		t.Errorf("static should default to unique=true")
	}
}

// TestParseProxyString_MobileHop tests parsing of mobilehop with changeUrl.
func TestParseProxyString_MobileHop(t *testing.T) {
	input := "mobilehop|192.168.1.1:8080|https://webhook.example.com/change"
	res, err := parseProxyString(input)
	if err != nil {
		t.Fatalf("parseProxyString error: %v", err)
	}
	if res.pType != ProxyTypeMobileHop {
		t.Errorf("got type %s, want mobilehop", res.pType)
	}
	if res.proxyStr != "192.168.1.1:8080" {
		t.Errorf("got proxyStr %s", res.proxyStr)
	}
	if res.changeUrl != "https://webhook.example.com/change" {
		t.Errorf("got changeUrl %s", res.changeUrl)
	}
}

// TestParseProxyString_Sticky_Unique tests sticky with explicit unique flag.
func TestParseProxyString_Sticky_Unique(t *testing.T) {
	input := "sticky|192.168.1.1:8080:user:pass|true|300|https://webhook.com"
	res, err := parseProxyString(input)
	if err != nil {
		t.Fatalf("parseProxyString error: %v", err)
	}
	if res.pType != ProxyTypeSticky {
		t.Errorf("got type %s, want sticky", res.pType)
	}
	if !res.unique {
		t.Errorf("sticky unique flag should be true")
	}
	if res.minTime != 300 {
		t.Errorf("got minTime %d, want 300", res.minTime)
	}
	if res.changeUrl != "https://webhook.com" {
		t.Errorf("got changeUrl %s", res.changeUrl)
	}
}

// TestParseProxyString_Sticky_NonUnique tests sticky with unique=false.
func TestParseProxyString_Sticky_NonUnique(t *testing.T) {
	input := "sticky|192.168.1.1:8080:user:pass|false"
	res, err := parseProxyString(input)
	if err != nil {
		t.Fatalf("parseProxyString error: %v", err)
	}
	if res.pType != ProxyTypeSticky {
		t.Errorf("got type %s, want sticky", res.pType)
	}
	if res.unique {
		t.Errorf("sticky unique flag should be false")
	}
}

// TestParseProxyString_KiotProxy tests kiotproxy with region.
func TestParseProxyString_KiotProxy(t *testing.T) {
	input := "kiotproxy|mykey|us-west"
	res, err := parseProxyString(input)
	if err != nil {
		t.Fatalf("parseProxyString error: %v", err)
	}
	if res.pType != ProxyTypeKiotProxy {
		t.Errorf("got type %s, want kiotproxy", res.pType)
	}
	if res.apiKey != "mykey" {
		t.Errorf("got apiKey %s", res.apiKey)
	}
	if res.changeUrl != "us-west" {
		t.Errorf("got changeUrl (region) %s", res.changeUrl)
	}
}

// TestParseProxyString_Auto tests auto format.
func TestParseProxyString_Auto(t *testing.T) {
	input := "auto|192.168.1.1:8080"
	res, err := parseProxyString(input)
	if err != nil {
		t.Fatalf("parseProxyString error: %v", err)
	}
	if res.pType != ProxyTypeAuto {
		t.Errorf("got type %s, want auto", res.pType)
	}
	if res.unique != true {
		t.Errorf("auto should default to unique=true")
	}
}

// TestParseProxyString_IPv4Xoay tests ipv4xoay format.
func TestParseProxyString_IPv4Xoay(t *testing.T) {
	input := "ipv4xoay|mykey|600"
	res, err := parseProxyString(input)
	if err != nil {
		t.Fatalf("parseProxyString error: %v", err)
	}
	if res.pType != ProxyTypeIPv4Xoay {
		t.Errorf("got type %s, want ipv4xoay", res.pType)
	}
	if res.apiKey != "mykey" {
		t.Errorf("got apiKey %s", res.apiKey)
	}
	if res.minTime != 600 {
		t.Errorf("got minTime %d", res.minTime)
	}
}

// TestParseProxyString_InvalidFormat tests error on invalid format.
func TestParseProxyString_InvalidFormat(t *testing.T) {
	tests := []string{
		"invalid",                    // no pipe
		"invalid|",                   // no type
		"nosuchtype|value",           // invalid type
		"|value",                     // empty type
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			_, err := parseProxyString(input)
			if err == nil {
				t.Fatalf("expected error for %q", input)
			}
		})
	}
}

// TestProcessStickyProxyStr_ReplaceRandom tests {random} and ${random} replacement.
func TestProcessStickyProxyStr_ReplaceRandom(t *testing.T) {
	tests := []struct {
		input       string
		placeholder string
	}{
		{
			input:       "192.168.1.1:8080:{random}:pass",
			placeholder: "{random}",
		},
		{
			input:       "192.168.1.1:8080:${random}:pass",
			placeholder: "${random}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.placeholder, func(t *testing.T) {
			result := processStickyProxyStr(tt.input)
			if result == tt.input {
				t.Errorf("processStickyProxyStr did not replace %s", tt.placeholder)
			}
			if len(result) != len(tt.input) {
				// Allow some variance for random generation
			}
		})
	}
}

// TestProcessStickyProxyStr_NoRandom tests proxy string without placeholders.
func TestProcessStickyProxyStr_NoRandom(t *testing.T) {
	input := "192.168.1.1:8080:myuser:mypass"
	result := processStickyProxyStr(input)
	if result != input {
		t.Errorf("processStickyProxyStr should not change %s", input)
	}
}

// TestGenerateRandomString tests random string generation.
func TestGenerateRandomString(t *testing.T) {
	tests := []int{8, 16, 32}
	for _, length := range tests {
		t.Run(("length_" + string(rune(length))), func(t *testing.T) {
			result := generateRandomString(length)
			if len(result) != length {
				t.Errorf("got length %d, want %d", len(result), length)
			}
			// Check it's hex-like
			for _, c := range result {
				if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
					t.Errorf("non-hex character in random string: %c", c)
				}
			}
		})
	}
}

// TestGenerateRandomString_Uniqueness tests that generated strings differ.
func TestGenerateRandomString_Uniqueness(t *testing.T) {
	s1 := generateRandomString(16)
	s2 := generateRandomString(16)
	if s1 == s2 {
		t.Errorf("consecutive random strings should differ: %s == %s", s1, s2)
	}
}

// TestFormatProxyURL_HostPort tests conversion of host:port format.
func TestFormatProxyURL_HostPort(t *testing.T) {
	input := "192.168.1.1:8080"
	expected := "http://192.168.1.1:8080"
	result := formatProxyURL(input)
	if result != expected {
		t.Errorf("got %s, want %s", result, expected)
	}
}

// TestFormatProxyURL_HostPortUserPass tests conversion of host:port:user:pass format.
func TestFormatProxyURL_HostPortUserPass(t *testing.T) {
	input := "192.168.1.1:8080:myuser:mypass"
	expected := "http://myuser:mypass@192.168.1.1:8080"
	result := formatProxyURL(input)
	if result != expected {
		t.Errorf("got %s, want %s", result, expected)
	}
}

// TestFormatProxyURL_TrailingEmptyCreds tests host:port:: (IPv4Xoay padding).
func TestFormatProxyURL_TrailingEmptyCreds(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// IPv4Xoay pads no-auth proxies with "::" — must not emit empty creds.
		{"160.250.166.24:10262::", "http://160.250.166.24:10262"},
		// A real username with empty password stays in the userinfo form.
		{"160.250.166.24:10262:user:", "http://user:@160.250.166.24:10262"},
	}
	for _, tt := range tests {
		if result := formatProxyURL(tt.input); result != tt.expected {
			t.Errorf("formatProxyURL(%s) = %s, want %s", tt.input, result, tt.expected)
		}
	}
}

// TestFormatProxyURL_MalformedFallback tests fallback for malformed input.
func TestFormatProxyURL_MalformedFallback(t *testing.T) {
	input := "malformed:input:with:too:many:parts"
	result := formatProxyURL(input)
	if result == "" {
		t.Errorf("formatProxyURL should not return empty string")
	}
}

// ============================================================================
// DATABASE TESTS
// ============================================================================

// newTestManager creates a ProxyManager with in-memory SQLite for testing.
func newTestManager(t *testing.T) *ProxyManager {
	t.Helper()
	db, err := initDB(":memory:")
	if err != nil {
		t.Fatalf("initDB error: %v", err)
	}
	pm := &ProxyManager{
		db:        db,
		cache:     make(map[int64]*Proxy),
		instances: NewInstanceManager(),
		providers: initProviders(),
		maxUsed:   10,
	}
	if err := pm.initSchema(); err != nil {
		t.Fatalf("initSchema error: %v", err)
	}
	return pm
}

// TestInitSchema creates schema and verifies tables exist.
func TestInitSchema(t *testing.T) {
	pm := newTestManager(t)
	defer pm.db.Close()

	// Verify proxies table exists
	var exists int
	err := pm.db.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name='proxies'
	`).Scan(&exists)
	if err != nil || exists != 1 {
		t.Errorf("proxies table not created")
	}

	// Verify indexes exist
	var indexCount int
	err = pm.db.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type='index' AND tbl_name='proxies'
	`).Scan(&indexCount)
	if err != nil || indexCount < 2 {
		t.Errorf("expected at least 2 indexes, got %d", indexCount)
	}
}

// TestUpsertProxy_Insert tests inserting a new proxy.
func TestUpsertProxy_Insert(t *testing.T) {
	pm := newTestManager(t)
	defer pm.db.Close()

	uniqueKey := "test_key_1"
	id, err := pm.upsertProxy(
		ProxyTypeTMProxy,
		"192.168.1.1:8080",
		"mykey",
		"",
		300,
		uniqueKey,
		true,
		time.Now(),
		"",
	)
	if err != nil {
		t.Fatalf("upsertProxy error: %v", err)
	}
	if id == 0 {
		t.Errorf("expected non-zero ID")
	}

	// Verify proxy in cache
	pm.mu.RLock()
	cached := pm.cache[id]
	pm.mu.RUnlock()
	if cached == nil {
		t.Errorf("proxy not in cache")
	}
	if cached.Type != ProxyTypeTMProxy {
		t.Errorf("cached type mismatch")
	}
}

// TestUpsertProxy_Update tests updating existing proxy via unique_key.
func TestUpsertProxy_Update(t *testing.T) {
	pm := newTestManager(t)
	defer pm.db.Close()

	uniqueKey := "test_key_2"
	id1, err := pm.upsertProxy(
		ProxyTypeTMProxy,
		"192.168.1.1:8080",
		"mykey",
		"",
		300,
		uniqueKey,
		true,
		time.Now(),
		"",
	)
	if err != nil {
		t.Fatalf("first upsertProxy error: %v", err)
	}

	// Upsert again with same unique_key, different proxyStr
	id2, err := pm.upsertProxy(
		ProxyTypeTMProxy,
		"10.0.0.1:9090",
		"mykey",
		"",
		300,
		uniqueKey,
		true,
		time.Now(),
		"",
	)
	if err != nil {
		t.Fatalf("second upsertProxy error: %v", err)
	}

	if id1 != id2 {
		t.Errorf("expected same ID after upsert, got %d and %d", id1, id2)
	}

	// Verify updated proxyStr
	p, err := pm.getProxyByID(id1)
	if err != nil {
		t.Fatalf("getProxyByID error: %v", err)
	}
	if p.ProxyStr != "10.0.0.1:9090" {
		t.Errorf("proxy string not updated: %s", p.ProxyStr)
	}
}

// TestReleaseProxy sets running=false.
func TestReleaseProxy(t *testing.T) {
	pm := newTestManager(t)
	defer pm.db.Close()

	id, err := pm.upsertProxy(
		ProxyTypeStatic,
		"192.168.1.1:8080",
		"",
		"",
		0,
		"test_key_3",
		false,
		time.Now(),
		"",
	)
	if err != nil {
		t.Fatalf("upsertProxy error: %v", err)
	}

	// Acquire
	pm.db.Exec(`UPDATE proxies SET running=1 WHERE id=?`, id)
	pm.mu.Lock()
	pm.cache[id].Running = 1
	pm.mu.Unlock()

	// Release
	err = pm.releaseProxy(id)
	if err != nil {
		t.Fatalf("releaseProxy error: %v", err)
	}

	// Verify released
	p, err := pm.getProxyByID(id)
	if err != nil {
		t.Fatalf("getProxyByID error: %v", err)
	}
	if p.Running != 0 {
		t.Errorf("proxy should not be running after release, got %d", p.Running)
	}
}

// TestReleaseProxy_FloorsAtZero guards the `running < slots` predicate: a double
// Release() must not drive the counter negative, which would leave the proxy
// permanently below the threshold and hand out unlimited slots.
func TestReleaseProxy_FloorsAtZero(t *testing.T) {
	pm := newTestManager(t)
	defer pm.db.Close()

	id, err := pm.upsertProxy(ProxyTypeStatic, "192.168.1.1:8080", "", "", 0, "test_key_floor", true, time.Now(), "")
	if err != nil {
		t.Fatalf("upsertProxy error: %v", err)
	}

	pm.releaseProxy(id) //nolint:errcheck
	pm.releaseProxy(id) //nolint:errcheck

	var running int
	pm.db.QueryRow(`SELECT running FROM proxies WHERE id=?`, id).Scan(&running)
	if running != 0 {
		t.Errorf("running should floor at 0, got %d", running)
	}
}

// TestAcquireAvailableProxy_SlotsAllowConcurrentHolders covers the Limit > 1
// path: one proxy must serve N holders, and only the first one may rotate.
func TestAcquireAvailableProxy_SlotsAllowConcurrentHolders(t *testing.T) {
	pm := newTestManager(t)
	defer pm.db.Close()

	id, err := pm.upsertProxy(ProxyTypeStatic, "192.168.1.1:8080", "", "", 0, "test_key_slots", true, time.Now(), "")
	if err != nil {
		t.Fatalf("upsertProxy error: %v", err)
	}

	for i := 1; i <= 3; i++ {
		p, first, err := pm.acquireAvailableProxy(0, 3, i)
		if err != nil {
			t.Fatalf("acquire #%d on a 3-slot proxy failed: %v", i, err)
		}
		if p.ID != id {
			t.Errorf("acquire #%d: expected proxy %d, got %d", i, id, p.ID)
		}
		if want := i == 1; first != want {
			t.Errorf("acquire #%d: first=%v, want %v", i, first, want)
		}
	}

	// Fourth holder exceeds the slot count — the pool is now exhausted.
	if _, _, err := pm.acquireAvailableProxy(0, 3, 4); err == nil {
		t.Error("acquire #4 should fail once all 3 slots are taken")
	}

	// Releasing one slot makes exactly one more acquire possible.
	pm.releaseProxy(id) //nolint:errcheck
	if _, first, err := pm.acquireAvailableProxy(0, 3, 4); err != nil {
		t.Errorf("acquire after release failed: %v", err)
	} else if first {
		t.Error("acquire after release should not be flagged as first holder")
	}
}

// TestAcquireAvailableProxy_DefaultSlotsIsOne pins the pre-slots behaviour:
// Limit unset (0) still means one holder at a time.
func TestAcquireAvailableProxy_DefaultSlotsIsOne(t *testing.T) {
	pm := newTestManager(t)
	defer pm.db.Close()

	if _, err := pm.upsertProxy(ProxyTypeStatic, "192.168.1.1:8080", "", "", 0, "test_key_slot1", true, time.Now(), ""); err != nil {
		t.Fatalf("upsertProxy error: %v", err)
	}

	if _, first, err := pm.acquireAvailableProxy(0, 0, 1); err != nil || !first {
		t.Fatalf("first acquire: err=%v first=%v", err, first)
	}
	if _, _, err := pm.acquireAvailableProxy(0, 0, 2); err == nil {
		t.Error("second acquire should fail when Limit is unset")
	}
}

// TestResetAllProxies resets used/running/error.
func TestResetAllProxies(t *testing.T) {
	pm := newTestManager(t)
	defer pm.db.Close()

	// Insert two proxies
	pm.upsertProxy(ProxyTypeStatic, "192.168.1.1:8080", "", "", 0, "test_key_4a", false, time.Now(), "")
	pm.upsertProxy(ProxyTypeStatic, "10.0.0.1:9090", "", "", 0, "test_key_4b", false, time.Now(), "")

	// Mark them with used/error
	pm.db.Exec(`UPDATE proxies SET used=5, running=1, error='test error'`)

	// Reset
	pm.resetAllProxies()

	// Verify all reset
	var totalUsed, totalRunning, errorCount int
	pm.db.QueryRow(`SELECT SUM(used), SUM(running), COUNT(error) FROM proxies WHERE error != ''`).
		Scan(&totalUsed, &totalRunning, &errorCount)
	if totalUsed != 0 || totalRunning != 0 || errorCount != 0 {
		t.Errorf("reset failed: used=%d, running=%d, errors=%d", totalUsed, totalRunning, errorCount)
	}
}

// TestDeleteAllProxies removes all proxies.
func TestDeleteAllProxies(t *testing.T) {
	pm := newTestManager(t)
	defer pm.db.Close()

	// Insert two proxies
	pm.upsertProxy(ProxyTypeStatic, "192.168.1.1:8080", "", "", 0, "test_key_5a", false, time.Now(), "")
	pm.upsertProxy(ProxyTypeStatic, "10.0.0.1:9090", "", "", 0, "test_key_5b", false, time.Now(), "")

	// Delete all
	pm.deleteAllProxies()

	// Verify cache cleared
	pm.mu.RLock()
	cacheSize := len(pm.cache)
	pm.mu.RUnlock()
	if cacheSize != 0 {
		t.Errorf("cache not cleared, size=%d", cacheSize)
	}

	// Verify DB cleared
	var count int
	pm.db.QueryRow(`SELECT COUNT(*) FROM proxies`).Scan(&count)
	if count != 0 {
		t.Errorf("proxies not deleted from DB, count=%d", count)
	}
}

// ============================================================================
// SELECTION QUERY TESTS
// ============================================================================

// TestQueryAvailableProxy_SelectsLowestUsed selects proxy with lowest used.
func TestQueryAvailableProxy_SelectsLowestUsed(t *testing.T) {
	pm := newTestManager(t)
	defer pm.db.Close()

	// Insert two proxies, mark one with higher used count
	pm.upsertProxy(ProxyTypeStatic, "192.168.1.1:8080", "", "", 0, "test_key_6a", false, time.Now(), "")
	pm.upsertProxy(ProxyTypeStatic, "10.0.0.1:9090", "", "", 0, "test_key_6b", false, time.Now(), "")

	// Mark first with used=5, second with used=1
	pm.db.Exec(`UPDATE proxies SET used=5 WHERE proxy_str='192.168.1.1:8080'`)
	pm.db.Exec(`UPDATE proxies SET used=1 WHERE proxy_str='10.0.0.1:9090'`)

	// Query should return second one (lowest used)
	p, err := pm.queryAvailableProxy(10, 1)
	if err != nil {
		t.Fatalf("queryAvailableProxy error: %v", err)
	}
	if p.ProxyStr != "10.0.0.1:9090" {
		t.Errorf("expected 10.0.0.1:9090, got %s", p.ProxyStr)
	}
}

// TestQueryAvailableProxy_IgnoresRunning ignores running=true proxies for unique types.
// Note: sticky is non-unique by default, so it bypasses running check. Use static which is unique.
func TestQueryAvailableProxy_IgnoresRunning(t *testing.T) {
	pm := newTestManager(t)
	defer pm.db.Close()

	// Insert two static proxies (is_unique=true)
	id1, _ := pm.upsertProxy(ProxyTypeStatic, "192.168.1.1:8080", "", "", 0, "test_key_7a", true, time.Now(), "")
	id2, _ := pm.upsertProxy(ProxyTypeStatic, "10.0.0.1:9090", "", "", 0, "test_key_7b", true, time.Now(), "")

	// Mark first as running
	pm.db.Exec(`UPDATE proxies SET running=1 WHERE id=?`, id1)

	// Query should return second one (first is running and unique)
	p, err := pm.queryAvailableProxy(10, 1)
	if err != nil {
		t.Fatalf("queryAvailableProxy error: %v", err)
	}
	if p.ID != id2 {
		t.Errorf("expected id %d, got %d", id2, p.ID)
	}
}

// TestQueryAvailableProxy_RespectMaxUsed enforces maxUsed limit for unique proxies.
// Non-unique proxies bypass the used check, so we test with unique=true.
func TestQueryAvailableProxy_RespectMaxUsed(t *testing.T) {
	pm := newTestManager(t)
	defer pm.db.Close()

	// Insert one unique static proxy with used > maxUsed
	pm.upsertProxy(ProxyTypeStatic, "192.168.1.1:8080", "", "", 0, "test_key_8", true, time.Now(), "")
	pm.db.Exec(`UPDATE proxies SET used=20, is_unique=1`)

	// Query with maxUsed=10 should fail (used > maxUsed, running=false, unique=1)
	_, err := pm.queryAvailableProxy(10, 1)
	if err == nil {
		t.Errorf("queryAvailableProxy should fail when used > maxUsed")
	}
}

// TestQueryAvailableProxy_NonUniqueBypass bypasses running check.
func TestQueryAvailableProxy_NonUniqueBypass(t *testing.T) {
	pm := newTestManager(t)
	defer pm.db.Close()

	// Insert non-unique (is_unique=0) proxy
	pm.upsertProxy(ProxyTypeSticky, "192.168.1.1:8080", "", "", 0, "test_key_9", false, time.Now(), "")

	// Mark it as running
	pm.db.Exec(`UPDATE proxies SET running=1`)

	// Query should still return it (non-unique bypasses running check)
	p, err := pm.queryAvailableProxy(10, 1)
	if err != nil {
		t.Fatalf("queryAvailableProxy error: %v", err)
	}
	if p.ProxyStr != "192.168.1.1:8080" {
		t.Errorf("non-unique proxy should be returned despite running flag")
	}
}

// TestQueryAvailableProxy_EmptyResultError returns error when no proxies available.
func TestQueryAvailableProxy_EmptyResultError(t *testing.T) {
	pm := newTestManager(t)
	defer pm.db.Close()

	// No proxies inserted
	_, err := pm.queryAvailableProxy(10, 1)
	if err == nil {
		t.Errorf("queryAvailableProxy should error with no available proxies")
	}
}
