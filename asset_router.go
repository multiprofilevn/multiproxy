package multiproxy

import (
	"context"
	"net"
	"net/http"
	"path"
	"strings"
)

// ctxKey is an unexported type for context keys in this package.
type ctxKey int

const requestKey ctxKey = iota

// WithRequest attaches an *http.Request to the context for downstream dialers.
func WithRequest(ctx context.Context, req *http.Request) context.Context {
	return context.WithValue(ctx, requestKey, req)
}

// RequestFromContext retrieves the *http.Request previously stored by WithRequest.
// Returns nil if no request is present.
func RequestFromContext(ctx context.Context) *http.Request {
	req, _ := ctx.Value(requestKey).(*http.Request)
	return req
}

// staticAssetExtensions lists file extensions that are treated as static assets
// and routed through the direct (non-proxy) dialer.
var staticAssetExtensions = map[string]bool{
	// JavaScript & CSS
	".js": true, ".css": true, ".mjs": true, ".cjs": true,
	// Images
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".svg": true, ".webp": true, ".ico": true, ".avif": true,
	".bmp": true, ".tiff": true, ".tif": true,
	// Fonts
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	// Video
	".mp4": true, ".webm": true, ".mkv": true, ".avi": true,
	".mov": true, ".wmv": true, ".flv": true, ".m4v": true,
	// Audio
	".mp3": true, ".ogg": true, ".wav": true, ".flac": true,
	".m4a": true, ".aac": true, ".wma": true,
	// Documents & Maps
	".pdf": true, ".map": true,
}

// AssetRoutingDialer routes dial requests: static assets go through directDialer,
// all other requests through upstreamDialer.
type AssetRoutingDialer struct {
	directDialer   HandlerDialer
	upstreamDialer HandlerDialer
}

// NewAssetRoutingDialer constructs an AssetRoutingDialer.
func NewAssetRoutingDialer(direct, upstream HandlerDialer) *AssetRoutingDialer {
	return &AssetRoutingDialer{
		directDialer:   direct,
		upstreamDialer: upstream,
	}
}

// DialContext selects the dialer based on whether the request in ctx is for a
// static asset. Falls back to upstreamDialer when no request is present.
func (d *AssetRoutingDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if d.isStaticAsset(ctx) {
		return d.directDialer.DialContext(ctx, network, address)
	}
	return d.upstreamDialer.DialContext(ctx, network, address)
}

// isStaticAsset returns true if the request in ctx targets a static asset,
// determined by URL path extension, Accept header, or well-known CDN path segments.
func (d *AssetRoutingDialer) isStaticAsset(ctx context.Context) bool {
	req := RequestFromContext(ctx)
	if req == nil {
		return false
	}

	// Check URL path extension.
	ext := strings.ToLower(path.Ext(req.URL.Path))
	if staticAssetExtensions[ext] {
		return true
	}

	// Check Accept header for media content types.
	accept := req.Header.Get("Accept")
	if accept != "" &&
		(strings.Contains(accept, "image/") ||
			strings.Contains(accept, "video/") ||
			strings.Contains(accept, "audio/") ||
			strings.Contains(accept, "font/") ||
			strings.Contains(accept, "application/font")) {
		return true
	}

	// Check for common CDN / static-serving path segments.
	lowerPath := strings.ToLower(req.URL.Path)
	if (strings.Contains(lowerPath, "/assets/") ||
		strings.Contains(lowerPath, "/static/") ||
		strings.Contains(lowerPath, "/images/") ||
		strings.Contains(lowerPath, "/img/") ||
		strings.Contains(lowerPath, "/fonts/") ||
		strings.Contains(lowerPath, "/css/") ||
		strings.Contains(lowerPath, "/js/") ||
		strings.Contains(lowerPath, "/media/")) &&
		!strings.Contains(lowerPath, "/api/") &&
		(req.URL.RawQuery == "" || !strings.Contains(req.URL.RawQuery, "callback")) {
		return true
	}

	return false
}
