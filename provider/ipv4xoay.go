package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const ipv4xoayBaseURL = "https://proxyxoay.shop/api/get.php"

// IPv4XoayResponse cấu trúc response từ IPv4Xoay API
type IPv4XoayResponse struct {
	Status              int    `json:"status"`
	Message             string `json:"message"`
	ProxyHTTP           string `json:"proxyhttp"`
	ProxySOCKS5         string `json:"proxysocks5"`
	NhaMang             string `json:"Nha Mang"`
	ViTri               string `json:"Vi Tri"`
	TokenExpirationDate string `json:"Token expiration date"`
	IP                  string `json:"ip"`
}

// IPv4Xoay implements Provider for proxyxoay.shop
type IPv4Xoay struct {
	client *http.Client
}

// NewIPv4Xoay creates a new IPv4Xoay provider instance
func NewIPv4Xoay() *IPv4Xoay {
	return &IPv4Xoay{client: &http.Client{Timeout: 30 * time.Second}}
}

// GetNewProxy lấy proxy mới từ IPv4Xoay (dùng chung endpoint với GetCurrentProxy)
func (i *IPv4Xoay) GetNewProxy(ctx context.Context, apiKey string, opts map[string]string) (*ProxyResult, error) {
	return i.getProxy(ctx, apiKey)
}

// GetCurrentProxy lấy proxy hiện tại từ IPv4Xoay (dùng chung endpoint với GetNewProxy)
func (i *IPv4Xoay) GetCurrentProxy(ctx context.Context, apiKey string) (*ProxyResult, error) {
	return i.getProxy(ctx, apiKey)
}

// getProxy calls the shared IPv4Xoay endpoint and maps status codes to ProxyResult
func (i *IPv4Xoay) getProxy(ctx context.Context, apiKey string) (*ProxyResult, error) {
	reqURL := fmt.Sprintf("%s?key=%s&nhamang=random&tinhthanh=0", ipv4xoayBaseURL, url.QueryEscape(apiKey))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("ipv4xoay: create request: %w", err)
	}

	httpResp, err := i.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ipv4xoay: send request: %w", err)
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("ipv4xoay: read response: %w", err)
	}

	var result IPv4XoayResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("ipv4xoay: unmarshal response: %w", err)
	}

	switch result.Status {
	case 100:
		// thành công
		return &ProxyResult{
			ProxyStr: result.ProxyHTTP,
			Success:  true,
		}, nil
	case 101:
		// bị block (cooldown), caller nên thử lại sau
		return &ProxyResult{Blocked: true}, nil
	default:
		return &ProxyResult{
			Error: fmt.Sprintf("ipv4xoay status=%d message=%s", result.Status, result.Message),
		}, nil
	}
}
