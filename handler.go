package multiproxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// HandlerDialer is satisfied by any dialer that supports DialContext.
type HandlerDialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// ProxyHandler is an http.Handler that serves as an HTTP/HTTPS proxy.
// CONNECT requests are tunnelled; other requests are forwarded via RoundTrip.
type ProxyHandler struct {
	dialer        HandlerDialer
	httptransport http.RoundTripper
}

// NewProxyHandler constructs a ProxyHandler using the given dialer.
func NewProxyHandler(dialer HandlerDialer) *ProxyHandler {
	return &ProxyHandler{
		dialer: dialer,
		httptransport: &http.Transport{
			DialContext:       dialer.DialContext,
			DisableKeepAlives: true,
		},
	}
}

// ServeHTTP dispatches incoming proxy requests to the appropriate handler.
func (s *ProxyHandler) ServeHTTP(wr http.ResponseWriter, req *http.Request) {
	if (req.URL.Host == "" || req.URL.Scheme == "" && req.Method != http.MethodConnect) && req.ProtoMajor < 2 ||
		req.Host == "" && req.ProtoMajor == 2 {
		http.Error(wr, "Bad Request", http.StatusBadRequest)
		return
	}

	// Attach request to context so AssetRoutingDialer can inspect it.
	ctx := WithRequest(req.Context(), req)
	req = req.WithContext(ctx)

	delHopHeaders(req.Header)

	if req.Method == http.MethodConnect {
		s.handleTunnel(wr, req)
	} else {
		s.handleRequest(wr, req)
	}
}

// handleTunnel handles CONNECT requests by establishing a bidirectional tunnel.
func (s *ProxyHandler) handleTunnel(wr http.ResponseWriter, req *http.Request) {
	conn, err := s.dialer.DialContext(req.Context(), "tcp", req.RequestURI)
	if err != nil {
		http.Error(wr, "Bad Gateway", http.StatusBadGateway)
		return
	}
	defer conn.Close()

	if req.ProtoMajor == 0 || req.ProtoMajor == 1 {
		localconn, _, err := hijack(wr)
		if err != nil {
			http.Error(wr, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		defer localconn.Close()

		fmt.Fprintf(localconn, "HTTP/%d.%d 200 OK\r\n\r\n", req.ProtoMajor, req.ProtoMinor)
		PairConnections(req.Context(), localconn, conn) //nolint:errcheck
	} else if req.ProtoMajor == 2 {
		wr.Header()["Date"] = nil
		wr.WriteHeader(http.StatusOK)
		flush(wr)
		h2conn := wrapH2(req.Body, wr)
		PairConnections(req.Context(), h2conn, conn) //nolint:errcheck
	} else {
		http.Error(wr, "Unsupported protocol version", http.StatusBadRequest)
	}
}

// handleRequest forwards a plain HTTP request upstream via RoundTrip.
func (s *ProxyHandler) handleRequest(wr http.ResponseWriter, req *http.Request) {
	req.RequestURI = ""
	if req.ProtoMajor == 2 {
		req.URL.Scheme = "http"
		req.URL.Host = req.Host
	}

	resp, err := s.httptransport.RoundTrip(req)
	if err != nil {
		http.Error(wr, "Server Error", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	delHopHeaders(resp.Header)
	copyHeader(wr.Header(), resp.Header)
	wr.WriteHeader(resp.StatusCode)
	flush(wr)
	io.Copy(wr, resp.Body) //nolint:errcheck
}

// h2Conn wraps an HTTP/2 request body + response writer as a net.Conn-like value.
// Only Read, Write, and Close are meaningful; net.Conn address methods return stubs.
type h2Conn struct {
	r io.ReadCloser
	w http.ResponseWriter
}

func wrapH2(r io.ReadCloser, w http.ResponseWriter) *h2Conn {
	return &h2Conn{r: r, w: w}
}

func (c *h2Conn) Read(p []byte) (int, error)         { return c.r.Read(p) }
func (c *h2Conn) Write(p []byte) (int, error)        { return c.w.Write(p) }
func (c *h2Conn) Close() error                       { return c.r.Close() }
func (c *h2Conn) LocalAddr() net.Addr                { return stubAddr{} }
func (c *h2Conn) RemoteAddr() net.Addr               { return stubAddr{} }
func (c *h2Conn) SetDeadline(_ time.Time) error      { return nil }
func (c *h2Conn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *h2Conn) SetWriteDeadline(_ time.Time) error { return nil }

type stubAddr struct{}

func (stubAddr) Network() string { return "h2" }
func (stubAddr) String() string  { return "h2" }
