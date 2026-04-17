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

const kiotproxyBaseURL = "https://api.kiotproxy.com/api/v1/proxies"

// KiotProxyResponse cấu trúc response từ KiotProxy API
type KiotProxyResponse struct {
	Success   bool          `json:"success"`
	Code      int           `json:"code"`
	Message   string        `json:"message"`
	Status    string        `json:"status"`
	Error     string        `json:"error"`
	Timestamp int64         `json:"timestamp"`
	Data      KiotProxyData `json:"data"`
}

// KiotProxyData chứa thông tin proxy từ KiotProxy
type KiotProxyData struct {
	RealIPAddress string `json:"realIpAddress"`
	HTTP          string `json:"http"`
	SOCKS5        string `json:"socks5"`
	NextRequestAt int64  `json:"nextRequestAt"`
	HTTPPort      int    `json:"httpPort"`
	SOCKS5Port    int    `json:"socks5Port"`
	Host          string `json:"host"`
	Location      string `json:"location"`
	ExpirationAt  int64  `json:"expirationAt"`
	TTL           int    `json:"ttl"`
	TTC           int    `json:"ttc"`
}

// KiotProxy implements Provider for kiotproxy.com
type KiotProxy struct {
	client *http.Client
}

// NewKiotProxy creates a new KiotProxy provider instance
func NewKiotProxy() *KiotProxy {
	return &KiotProxy{client: &http.Client{Timeout: 30 * time.Second}}
}

// GetNewProxy lấy proxy mới từ KiotProxy.
// opts keys: "region"
func (k *KiotProxy) GetNewProxy(ctx context.Context, apiKey string, opts map[string]string) (*ProxyResult, error) {
	u := fmt.Sprintf("%s/new?key=%s", kiotproxyBaseURL, url.QueryEscape(apiKey))
	if region, ok := opts["region"]; ok && region != "" {
		u += fmt.Sprintf("&region=%s", url.QueryEscape(region))
	}

	resp, err := k.doGet(ctx, u)
	if err != nil {
		return nil, err
	}

	return kiotproxyToResult(resp), nil
}

// GetCurrentProxy lấy proxy hiện tại từ KiotProxy
func (k *KiotProxy) GetCurrentProxy(ctx context.Context, apiKey string) (*ProxyResult, error) {
	reqURL := fmt.Sprintf("%s/current?key=%s", kiotproxyBaseURL, url.QueryEscape(apiKey))

	resp, err := k.doGet(ctx, reqURL)
	if err != nil {
		return nil, err
	}

	return kiotproxyToResult(resp), nil
}

// doGet sends GET request with context and returns parsed KiotProxyResponse
func (k *KiotProxy) doGet(ctx context.Context, url string) (*KiotProxyResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("kiotproxy: create request: %w", err)
	}

	httpResp, err := k.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kiotproxy: send request: %w", err)
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("kiotproxy: read response: %w", err)
	}

	var result KiotProxyResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("kiotproxy: unmarshal response: %w", err)
	}

	return &result, nil
}

// kiotproxyToResult converts KiotProxyResponse to unified ProxyResult
func kiotproxyToResult(r *KiotProxyResponse) *ProxyResult {
	if !r.Success || r.Data.HTTP == "" {
		errMsg := r.Error
		if errMsg == "" {
			errMsg = r.Message
		}
		return &ProxyResult{
			Success: false,
			Error:   fmt.Sprintf("kiotproxy code=%d message=%s", r.Code, errMsg),
		}
	}
	return &ProxyResult{
		ProxyStr: r.Data.HTTP,
		Success:  true,
	}
}
