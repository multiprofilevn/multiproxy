package multiproxy

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"time"
)

// hopHeaders are removed before forwarding requests/responses per RFC 2616 §13.5.1.
var hopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Connection",
	"Proxy-Authorization",
	"Te",
	"Trailers",
	"Transfer-Encoding",
	"Upgrade",
}

// delHopHeaders removes hop-by-hop headers from h.
func delHopHeaders(h http.Header) {
	for _, hdr := range hopHeaders {
		h.Del(hdr)
	}
}

// copyHeader copies all headers from src into dst.
func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

// hijack promotes the HTTP/1 connection to a raw TCP connection and clears any
// existing deadline so subsequent reads/writes are not prematurely cancelled.
func hijack(hijackable interface{}) (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := hijackable.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("connection does not support hijacking")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, nil, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, nil, err
	}
	return conn, rw, nil
}

// flush calls Flush on flusher if it implements http.Flusher.
func flush(flusher interface{}) {
	if f, ok := flusher.(http.Flusher); ok {
		f.Flush()
	}
}
