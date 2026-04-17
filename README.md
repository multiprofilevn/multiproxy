# MultiProxy

Go library for managing multiple proxy sources with automatic rotation, health checking, and asset routing.

Supports 7 proxy types: TMProxy, KiotProxy, IPv4Xoay, MobileHop, Sticky (unique/non-unique), Static, Auto.

## Quick Start

```go
package main

import (
    "context"
    "github.com/tuwibu/multiproxy"
)

func main() {
    // Create manager
    pm, err := multiproxy.New(multiproxy.Config{
        DBPath: "proxy.db",
        ProxyStrings: []string{
            "tmproxy|api.example.com|your-api-key",
            "static|192.168.1.1:8080",
            "kiotproxy|api.kiot.com|key|region=us",
        },
        IsBlockAssets: true,  // Route static assets directly
    })
    if err != nil {
        panic(err)
    }
    defer pm.Close()

    // Acquire proxy
    handle, err := pm.Acquire(context.Background(), 1)
    if err != nil {
        panic(err)
    }
    defer handle.Release()

    // Use as HTTP proxy
    // handle.Addr: "localhost:20001" (dumbproxy instance)
    // handle.Upstream: original proxy string
}
```

## Supported Proxy Types

| Type | Format | Rotation | Notes |
|------|--------|----------|-------|
| `tmproxy` | `tmproxy\|API_KEY\|MIN_TIME\|` | On demand | Calls GetNewProxy API |
| `kiotproxy` | `kiotproxy\|API_KEY\|MIN_TIME\|REGION` | On demand | Region param in opts |
| `ipv4xoay` | `ipv4xoay\|API_KEY\|MIN_TIME\|` | On demand | Handles blocked (101) |
| `mobilehop` | `mobilehop\|HOST:PORT:USER:PASS\|CHANGE_URL` | Always | GET webhook URL |
| `sticky` | `sticky\|HOST:PORT:USER{random}:PASS\|UNIQUE\|MIN_TIME` | On demand | Replaces {random} |
| `static` | `static\|HOST:PORT:USER:PASS\|MIN_TIME\|` | Never | No rotation |
| `auto` | `auto\|HOST:PORT:USER:PASS\|` | Never | Pass-through only |

## Config Options

```go
type Config struct {
    DBPath              string        // SQLite path (default: "proxy.db")
    ChangeProxyWaitTime time.Duration // Wait after rotation (default: 0)
    ProxyStrings        []string      // Proxy definitions
    ClearAllProxy       bool          // Clear all on init
    MaxUsed             int           // Max uses before rotation (0=unlimited)
    IsBlockAssets       bool          // Route assets directly (default: false)
}
```

## API Reference

### New(config Config) (*ProxyManager, error)

Initialize manager, load database, start instances.

### Acquire(ctx context.Context, threadID int) (*ProxyHandle, error)

Get available proxy. **Caller MUST call Release().**

Returns ProxyHandle with:
- `ID` - Proxy ID
- `Type` - Proxy type
- `Addr` - Connection string (localhost:port OR ip:port:user:pass)
- `Upstream` - Original proxy string

### ProxyHandle.Release() error

Mark proxy available for reuse.

### Reconfigure(config Config) error

Update config at runtime. Reloads proxies, restarts instances.

### Close() error

Stop all instances, close database. Unblocks all waiters.

### HealthCheck() []ProxyStatus

Return status of all proxies (ID, Type, Running, Used, Error).

## Architecture

**Flow:** New() → Acquire() → [use handle] → Release() → Close()

**Components:**
- Manager: Entry point, orchestrates Acquire/Release
- Database: SQLite with WAL mode, ~100 concurrent proxies
- Rotation: Selects best proxy per type, calls providers
- Instances: Local HTTP proxy servers (BasePort 20000+)
- Providers: API clients (TMProxy, KiotProxy, IPv4Xoay)
- Handler: HTTP CONNECT + regular requests
- Dialer: Upstream proxy dialer with timeout
- Forward: Bidirectional copy with deadlines

## Features

- **7 Proxy Types** - TMProxy, KiotProxy, IPv4Xoay, MobileHop, Sticky, Static, Auto
- **Automatic Rotation** - Per-type rotation logic with provider API calls
- **Asset Routing** - Optional: static assets bypass proxy
- **Local Instances** - HTTP servers per proxy (auto-scaled)
- **Database Backed** - SQLite for persistent state
- **Concurrent Safe** - RWMutex cache, WaitGroup goroutine tracking
- **Clean API** - Constructor + Acquire/Release pattern
- **No Leaks** - 8 critical goroutine/memory leaks fixed

## Dependencies

- `modernc.org/sqlite` - Pure Go SQLite driver (no CGO)
- `golang.org/x/sync/errgroup` - Goroutine management
- Standard library (net, http, database/sql, context, time, sync)

## Testing

Run tests:
```bash
go test -v -race ./...
```

Tests cover:
- All 7 proxy types
- Rotation per type
- Instance lifecycle
- Goroutine leak detection
- Concurrent access (race detector)
