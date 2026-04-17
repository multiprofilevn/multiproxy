package provider

import "strconv"

// parseIntOpt reads an integer value from opts map by key.
// Returns defaultVal if key is absent or cannot be parsed.
func parseIntOpt(opts map[string]string, key string, defaultVal int) int {
	if opts == nil {
		return defaultVal
	}
	v, ok := opts[key]
	if !ok || v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}
