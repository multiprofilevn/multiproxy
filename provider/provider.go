package provider

import "context"

// ProxyResult unifies response format across all proxy providers
type ProxyResult struct {
	ProxyStr string // format: https_addr:user:pass or ip:port:user:pass
	Success  bool
	Blocked  bool   // ipv4xoay status 101
	Error    string
}

// Provider defines the interface all proxy providers must implement
type Provider interface {
	GetNewProxy(ctx context.Context, apiKey string, opts map[string]string) (*ProxyResult, error)
	GetCurrentProxy(ctx context.Context, apiKey string) (*ProxyResult, error)
}

// NewProvider creates a provider instance by type name
func NewProvider(proxyType string) Provider {
	switch proxyType {
	case "tmproxy":
		return NewTMProxy()
	case "kiotproxy":
		return NewKiotProxy()
	case "ipv4xoay":
		return NewIPv4Xoay()
	default:
		return nil
	}
}
