package engine

import "strings"

// TruncateURL strips common scheme/www prefixes and shortens the URL to maxLen.
// Used by formatted output from preview and collect.
func TruncateURL(u string, maxLen int) string {
	u = strings.TrimPrefix(u, "https://www.")
	u = strings.TrimPrefix(u, "http://localhost")
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	if maxLen <= 3 || len(u) <= maxLen {
		return u
	}
	return u[:maxLen-3] + "..."
}
