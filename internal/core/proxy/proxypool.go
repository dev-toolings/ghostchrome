package proxy

import (
	"strings"
	"sync"
)

// ProxyPool hands out proxy URLs round-robin across sessions. Residential /
// mobile proxies are usually sold as a pool; rotating the egress IP is the #1
// lever against IP-reputation anti-bot layers (DataDome & co.), which a single
// static --proxy cannot provide. Thread-safe.
type ProxyPool struct {
	mu      sync.Mutex
	entries []string
	idx     int
}

// ParseProxyList splits a proxy list on commas, newlines and spaces, trims each
// entry and drops empties. Accepts the value of --proxy-list or the
// GHOSTCHROME_PROXY_LIST env (or the contents of a file passed to either).
func ParseProxyList(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if p := strings.TrimSpace(f); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// NewProxyPool builds a pool from already-parsed entries. A nil/empty list
// yields an empty pool whose Next() returns "".
func NewProxyPool(entries []string) *ProxyPool {
	return &ProxyPool{entries: entries}
}

// Len reports how many proxies are in the pool.
func (p *ProxyPool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}

// Next returns the next proxy round-robin, or "" when the pool is empty (so
// callers can fall back to the global --proxy / direct connection).
func (p *ProxyPool) Next() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.entries) == 0 {
		return ""
	}
	e := p.entries[p.idx%len(p.entries)]
	p.idx++
	return e
}
