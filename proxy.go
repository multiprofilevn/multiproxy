package multiproxy

import "time"

// ProxyType defines the proxy provider type
type ProxyType string

const (
	ProxyTypeTMProxy   ProxyType = "tmproxy"
	ProxyTypeStatic    ProxyType = "static"
	ProxyTypeMobileHop ProxyType = "mobilehop"
	ProxyTypeSticky    ProxyType = "sticky"
	ProxyTypeKiotProxy ProxyType = "kiotproxy"
	ProxyTypeAuto      ProxyType = "auto"
	ProxyTypeIPv4Xoay  ProxyType = "ipv4xoay"
)

// validProxyTypes maps valid proxy types for fast lookup
var validProxyTypes = map[ProxyType]bool{
	ProxyTypeTMProxy:   true,
	ProxyTypeStatic:    true,
	ProxyTypeMobileHop: true,
	ProxyTypeSticky:    true,
	ProxyTypeKiotProxy: true,
	ProxyTypeAuto:      true,
	ProxyTypeIPv4Xoay:  true,
}

// Proxy represents a proxy entry stored in the database
type Proxy struct {
	ID          int64
	Type        ProxyType
	ProxyStr    string
	ApiKey      string
	ChangeUrl   string
	MinTime     int // seconds between rotations
	Running     int // number of handles currently held (0 = idle)
	Used        int
	Unique      bool
	LastChanged time.Time
	LastIP      string
	Error       string
	ThreadID    int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Config holds library configuration passed to New()
type Config struct {
	DBPath              string        // default: "proxy.db"
	ChangeProxyWaitTime time.Duration // wait after proxy rotation
	ProxyStrings        []string      // proxy definitions to load
	ClearAllProxy       bool          // clear all proxies on init
	MaxUsed             int           // max usage count before rotation
	IsBlockAssets       bool          // route static assets directly
	Limit               int           // concurrent handles allowed per unique proxy (0/1 = one at a time)
}

// effectiveSlots clamps Limit into a usable concurrency count. 0 (unset) and
// negatives mean "one handle at a time", the behaviour before slots existed.
func effectiveSlots(limit int) int {
	if limit <= 1 {
		return 1
	}
	return limit
}

// ProxyHandle is returned by Acquire(). Caller MUST call Release() when done.
type ProxyHandle struct {
	ID       int64
	Type     ProxyType
	Addr     string // connection string (localhost:port or ip:port:user:pass)
	Upstream string // raw upstream proxy string
	manager  *ProxyManager
}

// Release marks the proxy as available for reuse
func (h *ProxyHandle) Release() error {
	return h.manager.releaseProxy(h.ID)
}

const (
	BasePort     = 20000
	MaxPortRange = 100
)
