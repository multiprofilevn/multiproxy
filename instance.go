package multiproxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Instance represents a running local proxy instance.
type Instance struct {
	ProxyID  int64
	Port     int
	Server   *http.Server
	Listener net.Listener
	Cancel   func()
	wg       sync.WaitGroup // tracks Serve() goroutine
}

// stop shuts down the instance in the correct order:
// cancel context → close listener → graceful server shutdown → wait for goroutine.
func (i *Instance) stop() {
	if i.Cancel != nil {
		i.Cancel()
	}
	if i.Listener != nil {
		i.Listener.Close()
	}
	if i.Server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		i.Server.Shutdown(ctx) //nolint:errcheck
	}
	i.wg.Wait()
}

// InstanceManager manages local proxy instances keyed by proxyID.
type InstanceManager struct {
	instances map[int64]*Instance
	mu        sync.RWMutex
}

// NewInstanceManager creates an initialised InstanceManager.
func NewInstanceManager() *InstanceManager {
	return &InstanceManager{instances: make(map[int64]*Instance)}
}

// StartInstance starts (or restarts) a local HTTP proxy for proxyID that
// forwards traffic through upstreamProxyStr. Returns the listen address.
// upstreamProxyStr format: "host:port" or "host:port:user:pass".
func (im *InstanceManager) StartInstance(proxyID int64, upstreamProxyStr string) (string, error) {
	im.mu.Lock()
	defer im.mu.Unlock()

	// Stop existing instance for this proxyID.
	if existing, ok := im.instances[proxyID]; ok {
		existing.stop()
		delete(im.instances, proxyID)
	}

	port := BasePort + int(proxyID)
	if port > BasePort+MaxPortRange {
		return "", fmt.Errorf("port %d exceeds max range (%d-%d), proxyID too large", port, BasePort, BasePort+MaxPortRange)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	// directDialer satisfies HandlerDialer via net.Dialer.DialContext.
	directDialer := &net.Dialer{}

	upstreamURL := formatProxyURL(upstreamProxyStr)
	upstreamDialer, err := NewUpstreamDialer(upstreamURL)
	if err != nil {
		return "", fmt.Errorf("create upstream dialer: %w", err)
	}

	// Route static assets directly; all other traffic via upstream.
	assetDialer := NewAssetRoutingDialer(directDialer, upstreamDialer)
	proxyHandler := NewProxyHandler(assetDialer)

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("listen on %s: %w", addr, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	server := &http.Server{
		Handler: proxyHandler,
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}

	inst := &Instance{
		ProxyID:  proxyID,
		Port:     port,
		Server:   server,
		Listener: listener,
		Cancel:   cancel,
	}

	inst.wg.Add(1)
	go func() {
		defer inst.wg.Done()
		server.Serve(listener) //nolint:errcheck
	}()

	im.instances[proxyID] = inst
	return addr, nil
}

// Has reports whether an instance is currently registered for proxyID.
// Note: registered != listening — use only as a fast pre-check; a stale
// entry may still exist for an instance whose Serve goroutine died.
func (im *InstanceManager) Has(proxyID int64) bool {
	im.mu.RLock()
	defer im.mu.RUnlock()
	_, ok := im.instances[proxyID]
	return ok
}

// StopInstance stops and removes the instance for proxyID.
func (im *InstanceManager) StopInstance(proxyID int64) {
	im.mu.Lock()
	defer im.mu.Unlock()

	if inst, ok := im.instances[proxyID]; ok {
		inst.stop()
		delete(im.instances, proxyID)
	}
}

// StopAll stops all instances and kills any orphaned processes in the port range.
func (im *InstanceManager) StopAll() {
	im.mu.Lock()
	for id, inst := range im.instances {
		inst.stop()
		delete(im.instances, id)
	}
	im.mu.Unlock()

	KillPortRange()
}

// KillPortRange kills any process occupying ports BasePort..BasePort+MaxPortRange.
// Useful after a crash where ports were not released cleanly.
func KillPortRange() {
	if runtime.GOOS == "windows" {
		killPortRangeWindows()
	} else {
		killPortRangeUnix()
	}
}

func killPortRangeWindows() {
	for port := BasePort; port <= BasePort+MaxPortRange; port++ {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err != nil {
			continue
		}
		conn.Close()
		script := fmt.Sprintf(
			`(Get-NetTCPConnection -LocalPort %d -ErrorAction SilentlyContinue).OwningProcess | ForEach-Object { Stop-Process -Id $_ -Force -ErrorAction SilentlyContinue }`,
			port,
		)
		exec.Command("powershell", "-NoProfile", "-Command", script).Run() //nolint:errcheck
	}
}

func killPortRangeUnix() {
	for port := BasePort; port <= BasePort+MaxPortRange; port++ {
		exec.Command("sh", "-c", fmt.Sprintf("lsof -ti:%d | xargs -r kill -9", port)).Run() //nolint:errcheck
	}
}

// formatProxyURL converts a proxy string to http:// URL form.
// "host:port"           → "http://host:port"
// "host:port:user:pass" → "http://user:pass@host:port"
// Trailing empty credential fields ("host:port::") are dropped — some upstream
// APIs pad no-auth proxies that way, and keeping them would emit an empty
// Proxy-Authorization header that auth-less proxies reject.
func formatProxyURL(proxyStr string) string {
	parts := strings.Split(proxyStr, ":")
	if len(parts) == 4 && parts[2] == "" && parts[3] == "" {
		parts = parts[:2]
	}
	switch len(parts) {
	case 2:
		return fmt.Sprintf("http://%s:%s", parts[0], parts[1])
	case 4:
		return fmt.Sprintf("http://%s:%s@%s:%s", parts[2], parts[3], parts[0], parts[1])
	default:
		return fmt.Sprintf("http://%s", proxyStr)
	}
}
