package multiproxy

import (
	"fmt"
	"strconv"
	"strings"
)

// proxyParseResult holds the parsed fields from a raw proxy string
type proxyParseResult struct {
	pType     ProxyType
	proxyStr  string // raw proxy string (empty for API-based types)
	apiKey    string // API key (tmproxy/kiotproxy/ipv4xoay)
	changeUrl string
	minTime   int
	unique    bool
}

// parseProxyString parses a pipe-delimited proxy definition string.
// Format: type|value|[minTime|changeUrl] with type-specific variations.
// MobileHop:  mobilehop|proxy_str|change_url          (no minTime)
// Sticky:     sticky|proxy_str|[true|false]|[minTime]|[changeUrl]
// Others:     type|apiKeyOrProxyStr|[minTime]|[changeUrl]
func parseProxyString(raw string) (*proxyParseResult, error) {
	parts := strings.Split(strings.TrimSpace(raw), "|")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid format: %s", raw)
	}

	pType := ProxyType(parts[0])
	if !validProxyTypes[pType] {
		return nil, fmt.Errorf("invalid proxy type: %s", parts[0])
	}

	res := &proxyParseResult{pType: pType}

	// Set unique defaults: all types except sticky default to unique=true
	switch pType {
	case ProxyTypeTMProxy, ProxyTypeMobileHop, ProxyTypeStatic, ProxyTypeKiotProxy, ProxyTypeAuto, ProxyTypeIPv4Xoay:
		res.unique = true
	}

	// parts[1]: API key (no colon) or proxy_str (has colon)
	if strings.Contains(parts[1], ":") {
		res.proxyStr = parts[1]
	} else {
		res.apiKey = parts[1]
	}

	if len(parts) <= 2 || parts[2] == "" {
		return res, nil
	}

	// MobileHop: parts[2] is changeUrl, no minTime field
	if pType == ProxyTypeMobileHop {
		res.changeUrl = parts[2]
		return res, nil
	}

	// Sticky: parts[2] may be unique flag (true/false) then optional minTime/changeUrl
	if pType == ProxyTypeSticky && (parts[2] == "true" || parts[2] == "false") {
		res.unique = parts[2] == "true"
		parseStickyExtraFields(res, parts[3:])
		return res, nil
	}

	// All other types + sticky without unique flag: parts[2] is minTime or changeUrl
	parseMinTimeAndChangeUrl(res, parts[2:])
	return res, nil
}

// parseStickyExtraFields parses optional [minTime] [changeUrl] tail for sticky type.
func parseStickyExtraFields(res *proxyParseResult, tail []string) {
	if len(tail) == 0 || tail[0] == "" {
		return
	}
	if val, _ := strconv.Atoi(tail[0]); val > 0 {
		res.minTime = val
		if len(tail) > 1 {
			res.changeUrl = tail[1]
		}
	} else {
		res.changeUrl = tail[0]
		if len(tail) > 1 {
			if val2, _ := strconv.Atoi(tail[1]); val2 > 0 {
				res.minTime = val2
			}
		}
	}
}

// parseMinTimeAndChangeUrl parses [minTime] [changeUrl] or [changeUrl] [minTime] order.
func parseMinTimeAndChangeUrl(res *proxyParseResult, tail []string) {
	if len(tail) == 0 || tail[0] == "" {
		return
	}
	if val, _ := strconv.Atoi(tail[0]); val > 0 {
		res.minTime = val
		if len(tail) > 1 {
			res.changeUrl = tail[1]
		}
	} else {
		res.changeUrl = tail[0]
		if len(tail) > 1 {
			if val2, _ := strconv.Atoi(tail[1]); val2 > 0 {
				res.minTime = val2
			}
		}
	}
}
