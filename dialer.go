package multiproxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const connectHandshakeTimeout = 30 * time.Second

// UpstreamDialer dials through an HTTP CONNECT proxy.
// It does not support TLS to the upstream proxy (HTTP only).
type UpstreamDialer struct {
	address  string
	userinfo *url.Userinfo
	dialer   *net.Dialer
}

// NewUpstreamDialer parses proxyURL (http://[user:pass@]host:port) and returns
// an UpstreamDialer. Returns an error if the URL is invalid or scheme is not http.
func NewUpstreamDialer(proxyURL string) (*UpstreamDialer, error) {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}
	if strings.ToLower(u.Scheme) != "http" {
		return nil, fmt.Errorf("unsupported proxy scheme %q: only http is supported", u.Scheme)
	}

	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "80"
	}

	return &UpstreamDialer{
		address:  net.JoinHostPort(host, port),
		userinfo: u.User,
		dialer:   &net.Dialer{},
	}, nil
}

// DialContext connects to the upstream proxy and performs an HTTP CONNECT
// handshake to reach the target address. A 30-second deadline is set for the
// handshake and cleared on success.
func (d *UpstreamDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	conn, err := d.dialer.DialContext(ctx, "tcp", d.address)
	if err != nil {
		return nil, fmt.Errorf("dial upstream proxy %s: %w", d.address, err)
	}

	// Set handshake deadline; cleared after successful CONNECT.
	if err := conn.SetDeadline(time.Now().Add(connectHandshakeTimeout)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("set handshake deadline: %w", err)
	}

	var reqBuf bytes.Buffer
	fmt.Fprintf(&reqBuf, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n", address, address)
	if d.userinfo != nil {
		fmt.Fprintf(&reqBuf, "Proxy-Authorization: %s\r\n", basicAuthHeader(d.userinfo))
	}
	fmt.Fprintf(&reqBuf, "User-Agent: multiproxy\r\n\r\n")

	if _, err := io.Copy(conn, &reqBuf); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write CONNECT request: %w", err)
	}

	resp, err := readResponse(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read CONNECT response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("upstream proxy returned status %d", resp.StatusCode)
	}

	// Clear deadline — normal I/O timeouts are managed by the caller.
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("clear deadline after CONNECT: %w", err)
	}

	return conn, nil
}

// responseTerminator is the byte sequence marking end of HTTP response headers.
var responseTerminator = []byte("\r\n\r\n")

const maxHeaderSize = 64 * 1024 // 64KB max for CONNECT response headers

// readResponse reads an HTTP response header from r by scanning byte-by-byte
// until the header terminator is found, then decodes it.
func readResponse(r io.Reader) (*http.Response, error) {
	var buf bytes.Buffer
	b := make([]byte, 1)
	for !bytes.HasSuffix(buf.Bytes(), responseTerminator) {
		if buf.Len() > maxHeaderSize {
			return nil, fmt.Errorf("response header too large (>%d bytes)", maxHeaderSize)
		}
		n, err := r.Read(b)
		if err != nil {
			return nil, fmt.Errorf("read HTTP response: %w", err)
		}
		if n == 0 {
			continue
		}
		if _, err := buf.Write(b[:n]); err != nil {
			return nil, fmt.Errorf("buffer HTTP response: %w", err)
		}
	}
	resp, err := http.ReadResponse(bufio.NewReader(&buf), nil)
	if err != nil {
		return nil, fmt.Errorf("decode proxy response: %w", err)
	}
	return resp, nil
}

// basicAuthHeader returns a "Basic <base64>" Proxy-Authorization value.
func basicAuthHeader(userinfo *url.Userinfo) string {
	username := userinfo.Username()
	password, _ := userinfo.Password()
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}
