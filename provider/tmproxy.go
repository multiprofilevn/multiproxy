package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const tmproxyBaseURL = "https://tmproxy.com/api/proxy"

// TMProxyResponse cấu trúc response từ TMProxy API
type TMProxyResponse struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    TMProxyData `json:"data"`
}

// TMProxyData chứa thông tin proxy từ TMProxy
type TMProxyData struct {
	IPAllow      string `json:"ip_allow"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	PublicIP     string `json:"public_ip"`
	ISPName      string `json:"isp_name"`
	LocationName string `json:"location_name"`
	SOCKS5       string `json:"socks5"`
	HTTPS        string `json:"https"`
	Timeout      int    `json:"timeout"`
	NextRequest  int    `json:"next_request"`
	ExpiredAt    string `json:"expired_at"`
}

// TMProxy implements Provider for tmproxy.com
type TMProxy struct {
	client *http.Client
}

// NewTMProxy creates a new TMProxy provider instance
func NewTMProxy() *TMProxy {
	return &TMProxy{client: &http.Client{Timeout: 30 * time.Second}}
}

// tmproxyNewProxyRequest payload cho get-new-proxy
type tmproxyNewProxyRequest struct {
	APIKey     string `json:"api_key"`
	IDLocation int    `json:"id_location"`
	IDISP      int    `json:"id_isp"`
}

// GetNewProxy lấy proxy mới từ TMProxy.
// opts keys: "id_location", "id_isp" (default "0")
func (t *TMProxy) GetNewProxy(ctx context.Context, apiKey string, opts map[string]string) (*ProxyResult, error) {
	idLocation := parseIntOpt(opts, "id_location", 0)
	idISP := parseIntOpt(opts, "id_isp", 0)

	payload := tmproxyNewProxyRequest{
		APIKey:     apiKey,
		IDLocation: idLocation,
		IDISP:      idISP,
	}

	resp, err := t.doPost(ctx, fmt.Sprintf("%s/get-new-proxy", tmproxyBaseURL), payload)
	if err != nil {
		return nil, err
	}

	return tmproxyToResult(resp), nil
}

// GetCurrentProxy lấy proxy hiện tại từ TMProxy
func (t *TMProxy) GetCurrentProxy(ctx context.Context, apiKey string) (*ProxyResult, error) {
	payload := map[string]string{"api_key": apiKey}

	resp, err := t.doPost(ctx, fmt.Sprintf("%s/get-current-proxy", tmproxyBaseURL), payload)
	if err != nil {
		return nil, err
	}

	return tmproxyToResult(resp), nil
}

// doPost marshals payload, sends POST with context, and returns parsed TMProxyResponse
func (t *TMProxy) doPost(ctx context.Context, url string, payload any) (*TMProxyResponse, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("tmproxy: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(data))
	if err != nil {
		return nil, fmt.Errorf("tmproxy: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	httpResp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tmproxy: send request: %w", err)
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("tmproxy: read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmproxy: api status %d: %s", httpResp.StatusCode, string(body))
	}

	var result TMProxyResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("tmproxy: unmarshal response: %w", err)
	}

	return &result, nil
}

// tmproxyToResult converts TMProxyResponse to unified ProxyResult
func tmproxyToResult(r *TMProxyResponse) *ProxyResult {
	if r.Code != 0 || r.Data.HTTPS == "" {
		return &ProxyResult{
			Success: false,
			Error:   fmt.Sprintf("tmproxy code=%d message=%s", r.Code, r.Message),
		}
	}
	return &ProxyResult{
		ProxyStr: r.Data.HTTPS,
		Success:  true,
	}
}
