package engine

import (
	"fmt"
	"strings"
)

// ElideDataURL shortens a data: URL for JSON output to its media type plus
// the payload length, e.g. "data:image/svg+xml;charset=utf8,[10234 chars]".
// Any other string is returned unchanged.
func ElideDataURL(u string) string {
	if !strings.HasPrefix(u, "data:") {
		return u
	}
	head := len("data:")
	if i := strings.IndexByte(u, ','); i >= 0 {
		head = i + 1
	}
	return fmt.Sprintf("%s[%d chars]", u[:head], len(u)-head)
}

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
