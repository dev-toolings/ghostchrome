// ghostchrome-mcp is a standalone MCP server binary that exposes the
// ghostchrome browser automation engine over JSON-RPC stdio.
//
// Configuration is via environment variables:
//
//	GHOSTCHROME_CONNECT     — ws:// URL or "auto"
//	GHOSTCHROME_HEADLESS    — "false" to run headful (default true)
//	GHOSTCHROME_STEALTH     — "true" to enable stealth
//	GHOSTCHROME_PROXY       — upstream proxy URL
//	GHOSTCHROME_PROFILE     — persistent profile name
//	GHOSTCHROME_TIMEOUT     — timeout in seconds (default 30)
//	GHOSTCHROME_VAULT_KEY   — encryption key for state vault
//	GHOSTCHROME_MCP_LAZY    — "1" to skip Chrome prewarm
//	GHOSTCHROME_IDLE_TIMEOUT — idle Chrome reap window (default 15m; 0/off disables)
//	GHOSTCHROME_POLICY      — path to a JSON policy file (fail closed if unreadable)
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/policy"
	mcpsurface "github.com/dev-toolings/ghostchrome/internal/surface/mcp"
	mcpsrv "github.com/mark3labs/mcp-go/server"
)

var version = "dev"

const defaultMCPIdleTimeout = 15 * time.Minute

func main() {
	if hasArg("--version") || hasArg("-v") {
		fmt.Println(version)
		return
	}
	opts, err := optionsFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ghostchrome-mcp: %v\n", err)
		os.Exit(1)
	}

	s := mcpsurface.New(opts)
	defer s.Close()
	s.StartIdleReaper()

	if os.Getenv("GHOSTCHROME_MCP_LAZY") != "1" {
		s.PrewarmAsync()
	}

	fmt.Fprintln(os.Stderr, "[ghostchrome-mcp] ready on stdio")
	// SIGTERM/SIGINT surface as context.Canceled from ServeStdio: that is a
	// normal shutdown, not an error. Close Chrome gracefully in both cases —
	// os.Exit would skip the defer and leave the kill to leakless.
	if err := mcpsrv.ServeStdio(s.Build("ghostchrome", version)); err != nil && !errors.Is(err, context.Canceled) {
		s.Close()
		fmt.Fprintf(os.Stderr, "mcp server: %v\n", err)
		os.Exit(1)
	}
}

// optionsFromEnv reads the server configuration. A policy that cannot be read
// fails the process instead of starting unrestricted: the whole point of
// GHOSTCHROME_POLICY is to deny, so silently ignoring it would be the worst
// possible default.
func optionsFromEnv() (mcpsurface.Options, error) {
	p, err := policyFromEnv()
	if err != nil {
		return mcpsurface.Options{}, err
	}
	return mcpsurface.Options{
		Connect:     os.Getenv("GHOSTCHROME_CONNECT"),
		Headless:    envBool("GHOSTCHROME_HEADLESS", true),
		Stealth:     envBool("GHOSTCHROME_STEALTH", false) || hasArg("--stealth"),
		Proxy:       os.Getenv("GHOSTCHROME_PROXY"),
		UserProfile: os.Getenv("GHOSTCHROME_PROFILE"),
		TimeoutSec:  envInt("GHOSTCHROME_TIMEOUT", 30),
		IdleTimeout: mcpIdleTimeout(),
		Policy:      p,
	}, nil
}

func policyFromEnv() (*policy.Policy, error) {
	path := strings.TrimSpace(os.Getenv("GHOSTCHROME_POLICY"))
	if path == "" {
		return nil, nil
	}
	p, err := policy.Load(path)
	if err != nil {
		return nil, fmt.Errorf("GHOSTCHROME_POLICY: %w", err)
	}
	return p, nil
}

func hasArg(want string) bool {
	for _, arg := range os.Args[1:] {
		if arg == want {
			return true
		}
	}
	return false
}

// mcpIdleTimeout mirrors the `ghostchrome mcp` command. The MCP process stays
// alive for its stdio client, but releasing an idle browser bounds memory when
// an agent leaves a session open. A Go duration (30m) or bare seconds (900)
// is accepted; zero, off, or an invalid value disables reaping.
func mcpIdleTimeout() time.Duration {
	v := strings.TrimSpace(os.Getenv("GHOSTCHROME_IDLE_TIMEOUT"))
	if v == "" {
		return defaultMCPIdleTimeout
	}
	if strings.EqualFold(v, "off") || v == "0" {
		return 0
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	fmt.Fprintf(os.Stderr, "ignoring invalid GHOSTCHROME_IDLE_TIMEOUT=%q\n", v)
	return 0
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
