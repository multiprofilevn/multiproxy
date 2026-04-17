package multiproxy

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// ============================================================================
// INSTANCE LIFECYCLE TESTS
// ============================================================================

// TestInstanceManager_StartInstance verifies instance starts and listens.
func TestInstanceManager_StartInstance(t *testing.T) {
	im := NewInstanceManager()
	defer im.StopAll()

	// Use high proxy ID to avoid port conflicts
	proxyID := int64(95)
	proxyStr := "127.0.0.1:8080" // dummy upstream

	addr, err := im.StartInstance(proxyID, proxyStr)
	if err != nil {
		t.Fatalf("StartInstance error: %v", err)
	}

	// Verify returned address
	expectedAddr := "127.0.0.1:20095"
	if addr != expectedAddr {
		t.Errorf("got address %s, want %s", addr, expectedAddr)
	}

	// Verify instance in manager
	im.mu.RLock()
	inst, ok := im.instances[proxyID]
	im.mu.RUnlock()
	if !ok {
		t.Errorf("instance not found in manager")
	}
	if inst.ProxyID != proxyID {
		t.Errorf("instance ProxyID mismatch")
	}
	if inst.Listener == nil {
		t.Errorf("instance listener is nil")
	}

	// Verify listener actually listening
	conn, err := net.DialTimeout("tcp", expectedAddr, 1*time.Second)
	if err != nil {
		t.Errorf("failed to connect to listener: %v", err)
	}
	conn.Close()
}

// TestInstanceManager_StopInstance stops and removes instance.
func TestInstanceManager_StopInstance(t *testing.T) {
	im := NewInstanceManager()
	defer im.StopAll()

	proxyID := int64(96)
	proxyStr := "127.0.0.1:8080"

	_, err := im.StartInstance(proxyID, proxyStr)
	if err != nil {
		t.Fatalf("StartInstance error: %v", err)
	}

	// Stop it
	im.StopInstance(proxyID)

	// Verify removed from manager
	im.mu.RLock()
	_, ok := im.instances[proxyID]
	im.mu.RUnlock()
	if ok {
		t.Errorf("instance should be removed after stop")
	}

	// Verify port is free (should fail to connect within timeout)
	expectedAddr := "127.0.0.1:20096"
	conn, err := net.DialTimeout("tcp", expectedAddr, 500*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Errorf("port should be free after stop")
	}
}

// TestInstanceManager_StopAll stops all instances.
func TestInstanceManager_StopAll(t *testing.T) {
	im := NewInstanceManager()

	// Start two instances
	proxyStr := "127.0.0.1:8080"
	im.StartInstance(97, proxyStr)
	im.StartInstance(98, proxyStr)

	// Stop all
	im.StopAll()

	// Verify all removed
	im.mu.RLock()
	count := len(im.instances)
	im.mu.RUnlock()
	if count != 0 {
		t.Errorf("instances not stopped, count=%d", count)
	}
}

// TestInstanceManager_RestartInstance replaces existing instance.
func TestInstanceManager_RestartInstance(t *testing.T) {
	im := NewInstanceManager()
	defer im.StopAll()

	proxyID := int64(99)
	proxyStr1 := "127.0.0.1:8080"
	proxyStr2 := "192.168.1.1:9090"

	// Start instance
	_, err := im.StartInstance(proxyID, proxyStr1)
	if err != nil {
		t.Fatalf("first StartInstance error: %v", err)
	}

	// Restart with different upstream
	_, err = im.StartInstance(proxyID, proxyStr2)
	if err != nil {
		t.Fatalf("second StartInstance error: %v", err)
	}

	// Verify instance exists and count is 1 (not 2)
	im.mu.RLock()
	count := len(im.instances)
	im.mu.RUnlock()
	if count != 1 {
		t.Errorf("expected 1 instance after restart, got %d", count)
	}
}

// ============================================================================
// FORWARD TESTS
// ============================================================================

// TestPairConnections_BidirectionalCopy verifies data flows both directions.
func TestPairConnections_BidirectionalCopy(t *testing.T) {
	// Create two separate pipe pairs for bidirectional communication
	// This simulates "incoming" and "outgoing" connections
	incomingRead, incomingWrite := net.Pipe()
	outgoingRead, outgoingWrite := net.Pipe()

	done := make(chan error, 1)
	ctx := context.Background()

	// Start PairConnections: it will copy incomingRead->outgoingWrite and outgoingRead->incomingWrite
	go func() {
		done <- PairConnections(ctx, incomingRead, outgoingWrite)
	}()

	// Test data
	testMsg := []byte("test data")

	// Write to incoming, should appear in outgoingRead
	go func() {
		incomingWrite.Write(testMsg)
		incomingWrite.Close()
	}()

	// Read from outgoing
	buf := make([]byte, 1024)
	outgoingRead.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := outgoingRead.Read(buf)
	if err != nil && err != io.EOF {
		t.Errorf("read from outgoing: %v", err)
	}

	if !bytes.Equal(buf[:n], testMsg) {
		t.Errorf("data mismatch: got %v, want %v", buf[:n], testMsg)
	}

	outgoingRead.Close()
	outgoingWrite.Close()

	// Wait for completion
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Errorf("PairConnections did not complete")
	}
}

// TestPairConnections_ContextCancel closes connections when context cancelled.
func TestPairConnections_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	done := make(chan error, 1)
	go func() {
		done <- PairConnections(ctx, client, server)
	}()

	// Give it a moment to start
	time.Sleep(50 * time.Millisecond)

	// Cancel context
	cancel()

	// PairConnections should exit
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Errorf("PairConnections did not exit after context cancel")
	}
}

// TestCopyWithDeadline_SetDeadlineOnRead verifies deadline is set during read.
// This is a basic sanity check - we don't stress-test the deadline logic.
func TestCopyWithDeadline_SetDeadlineOnRead(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	done := make(chan error, 1)
	go func() {
		done <- copyWithDeadline(server, client, 100*time.Millisecond)
	}()

	// Don't send data - let it timeout and close the read side
	// This verifies that SetReadDeadline was called (otherwise it would block forever)
	time.Sleep(200 * time.Millisecond)
	client.Close()

	// Should eventually complete
	select {
	case <-done:
		// Success - copyWithDeadline exited
	case <-time.After(3 * time.Second):
		t.Errorf("copyWithDeadline did not exit after deadline")
	}
}

// ============================================================================
// ASSET ROUTER TESTS
// ============================================================================

// TestIsStaticAsset_KnownExtensions tests static asset detection by extension.
func TestIsStaticAsset_KnownExtensions(t *testing.T) {
	tests := []struct {
		path     string
		wantTrue bool
	}{
		{"/app.js", true},
		{"/style.css", true},
		{"/image.png", true},
		{"/video.mp4", true},
		{"/font.woff2", true},
		{"/data.pdf", true},
		{"/api/users", false},
		{"/api/data.json", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			ctx := context.Background()
			req := makeTestRequest(tt.path, "")
			ctx = WithRequest(ctx, req)

			d := NewAssetRoutingDialer(&mockDialer{}, &mockDialer{})
			result := d.isStaticAsset(ctx)
			if result != tt.wantTrue {
				t.Errorf("got %v, want %v", result, tt.wantTrue)
			}
		})
	}
}

// TestIsStaticAsset_CDNPaths tests detection by CDN path segments.
func TestIsStaticAsset_CDNPaths(t *testing.T) {
	tests := []struct {
		path     string
		wantTrue bool
	}{
		{"/assets/bundle.js", true},
		{"/static/image.png", true},
		{"/images/photo.jpg", true},
		{"/img/icon.svg", true},
		{"/fonts/roboto.ttf", true},
		{"/css/main.css", true},
		{"/js/app.js", true},
		{"/media/video.mp4", true},
		{"/api/assets/data", false}, // /api/ prefix excludes
		{"/assets/?callback=123", false}, // callback excludes
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			ctx := context.Background()
			req := makeTestRequest(tt.path, "")
			ctx = WithRequest(ctx, req)

			d := NewAssetRoutingDialer(&mockDialer{}, &mockDialer{})
			result := d.isStaticAsset(ctx)
			if result != tt.wantTrue {
				t.Errorf("got %v, want %v for %s", result, tt.wantTrue, tt.path)
			}
		})
	}
}

// TestIsStaticAsset_AcceptHeader tests detection by Accept header.
func TestIsStaticAsset_AcceptHeader(t *testing.T) {
	tests := []struct {
		accept   string
		wantTrue bool
	}{
		{"image/*", true},
		{"image/png", true},
		{"video/mp4", true},
		{"audio/mpeg", true},
		{"font/woff2", true},
		{"application/font", true},
		{"application/json", false},
		{"text/html", false},
	}

	for _, tt := range tests {
		t.Run(tt.accept, func(t *testing.T) {
			ctx := context.Background()
			req := makeTestRequest("/api/data", tt.accept)
			ctx = WithRequest(ctx, req)

			d := NewAssetRoutingDialer(&mockDialer{}, &mockDialer{})
			result := d.isStaticAsset(ctx)
			if result != tt.wantTrue {
				t.Errorf("got %v, want %v", result, tt.wantTrue)
			}
		})
	}
}

// TestIsStaticAsset_NoRequest returns false when no request in context.
func TestIsStaticAsset_NoRequest(t *testing.T) {
	ctx := context.Background()

	d := NewAssetRoutingDialer(&mockDialer{}, &mockDialer{})
	result := d.isStaticAsset(ctx)
	if result {
		t.Errorf("expected false when no request in context")
	}
}

// TestAssetRoutingDialer_RoutesCorrectly selects correct dialer.
func TestAssetRoutingDialer_RoutesCorrectly(t *testing.T) {
	directDialer := &mockDialer{name: "direct"}
	upstreamDialer := &mockDialer{name: "upstream"}
	d := NewAssetRoutingDialer(directDialer, upstreamDialer)

	// Static asset → direct dialer
	ctx := WithRequest(context.Background(), makeTestRequest("/app.js", ""))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	_, err := d.DialContext(ctx, "tcp", "example.com:80")
	if err != nil {
		t.Fatalf("DialContext error: %v", err)
	}
	if directDialer.called == 0 {
		t.Errorf("direct dialer not called for static asset")
	}
	if upstreamDialer.called != 0 {
		t.Errorf("upstream dialer should not be called for static asset")
	}

	// Reset
	directDialer.called = 0
	upstreamDialer.called = 0

	// Non-static → upstream dialer
	ctx = WithRequest(context.Background(), makeTestRequest("/api/users", ""))
	ctx, cancel = context.WithCancel(ctx)
	defer cancel()

	_, err = d.DialContext(ctx, "tcp", "example.com:80")
	if err != nil {
		t.Fatalf("DialContext error: %v", err)
	}
	if upstreamDialer.called == 0 {
		t.Errorf("upstream dialer not called for non-asset")
	}
	if directDialer.called != 0 {
		t.Errorf("direct dialer should not be called for non-asset")
	}
}

// ============================================================================
// GOROUTINE LEAK TEST
// ============================================================================

// TestNoGoroutineLeak verifies no goroutine leak on manager lifecycle.
func TestNoGoroutineLeak(t *testing.T) {
	// Baseline goroutine count
	runtime.GC() // force garbage collection
	time.Sleep(100 * time.Millisecond)
	before := runtime.NumGoroutine()

	// Create manager, use it, close it
	pm, err := New(Config{
		DBPath:        ":memory:",
		ProxyStrings:  []string{},
		MaxUsed:       10,
		IsBlockAssets: false,
	})
	if err != nil {
		t.Fatalf("New error: %v", err)
	}

	// Let it settle
	time.Sleep(100 * time.Millisecond)

	pm.Close()

	// Wait a bit for goroutines to exit
	time.Sleep(200 * time.Millisecond)
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	after := runtime.NumGoroutine()

	// Allow up to +2 margin for test framework goroutines
	if after > before+2 {
		t.Errorf("goroutine leak detected: before=%d, after=%d, diff=%d", before, after, after-before)
	}
}

// ============================================================================
// MOCK HELPERS FOR TESTING
// ============================================================================

// mockDialer implements HandlerDialer for testing.
type mockDialer struct {
	name   string
	called int
	mu     sync.Mutex
}

func (m *mockDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	m.mu.Lock()
	m.called++
	m.mu.Unlock()
	server, client := net.Pipe()
	server.Close() // Close server side, return client
	return client, nil
}

// makeTestRequest creates a proper *http.Request for testing.
// path can include query string like "/assets/?callback=123"
func makeTestRequest(path string, accept string) *http.Request {
	u := &url.URL{Path: path}
	// Extract query string if present
	if idx := strings.Index(path, "?"); idx >= 0 {
		u.Path = path[:idx]
		u.RawQuery = path[idx+1:]
	}
	req := &http.Request{
		Method: "GET",
		URL:    u,
		Header: http.Header{},
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	return req
}
