package engine

import (
	"os"
	"strings"
)

// RenderProfile controls how output is rendered for the calling environment.
// It is resolved once per CLI invocation (see ResolveProfile) and then threaded
// into formatters.
type RenderProfile struct {
	// Agent is true when the output is being consumed by an LLM agent runner
	// (Claude Code, Cursor, Aider, etc.) rather than a human terminal.
	Agent bool

	// Format is "text" or "json".
	Format string

	// MaxLabelLen truncates node names / values to this length in agent mode.
	// 0 means no truncation.
	MaxLabelLen int

	// AbbrevRoles uses 1-2 character role abbreviations (b/a/t/c/s/r/m/x/h).
	AbbrevRoles bool

	// DropEmptyStats omits "[errors] 0 ..." / "[network] ... 0 failed" headers
	// when counts are zero.
	DropEmptyStats bool

	// ContentBoundary wraps page-derived text (node names / values) with sentinel
	// markers so an LLM consuming the snapshot can distinguish tool output from
	// untrusted page content (prompt-injection defense). Default false = opt-in only.
	// Markers used: ⟦page⟧ … ⟦/page⟧
	ContentBoundary bool
}

// ProfileHuman returns the default human-friendly profile.
func ProfileHuman(format string) RenderProfile {
	return RenderProfile{
		Agent:          false,
		Format:         format,
		MaxLabelLen:    0,
		AbbrevRoles:    false,
		DropEmptyStats: false,
	}
}

// ProfileAgent returns the compact agent-optimised profile.
func ProfileAgent(format string) RenderProfile {
	return RenderProfile{
		Agent:          true,
		Format:         format,
		MaxLabelLen:    80,
		AbbrevRoles:    true,
		DropEmptyStats: true,
	}
}

// ResolveProfile picks a RenderProfile from an explicit flag ("auto", "human",
// "agent") with environment-variable fallback for "auto".
func ResolveProfile(explicit, format string) RenderProfile {
	switch strings.ToLower(explicit) {
	case "human":
		return ProfileHuman(format)
	case "agent":
		return ProfileAgent(format)
	}
	if DetectAgent() {
		return ProfileAgent(format)
	}
	return ProfileHuman(format)
}

// DetectAgent reports whether the current process runs inside an LLM agent.
// Detection order:
//  1. GHOSTCHROME_PROFILE=agent|human (explicit override).
//  2. GHOSTCHROME_AGENT=1|true (explicit opt-in).
//  3. Known agent environment variables set by Claude Code, Cursor, Aider,
//     Devin, Gemini CLI, and similar tools.
func DetectAgent() bool {
	switch strings.ToLower(os.Getenv("GHOSTCHROME_PROFILE")) {
	case "agent":
		return true
	case "human":
		return false
	}
	if isTruthy(os.Getenv("GHOSTCHROME_AGENT")) {
		return true
	}
	for _, key := range agentEnvMarkers {
		if os.Getenv(key) != "" {
			return true
		}
	}
	return false
}

// agentEnvMarkers lists environment variables known to be set by popular AI
// coding agents. Presence (non-empty value) is sufficient to trigger agent
// mode — their own value is not inspected.
var agentEnvMarkers = []string{
	"CLAUDECODE",             // Claude Code
	"CLAUDE_CODE_ENTRYPOINT", // Claude Code (alt)
	"CURSOR_SESSION",         // Cursor IDE
	"CURSOR_TRACE_ID",        // Cursor (alt)
	"AIDER_CHAT",             // Aider
	"DEVIN_SESSION",          // Devin
	"GEMINI_CLI",             // Gemini CLI
	"CODEX_CLI",              // OpenAI Codex CLI
	"CONTINUE_GLOBAL_DIR",    // Continue.dev
	"ZED_ASSISTANT_SESSION",  // Zed AI
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
