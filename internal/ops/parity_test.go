package ops_test

// parity_test.go — divergence guard for the canonical ops catalog.
//
// Purpose: fail loudly when any of the three real surfaces drifts away from
// the catalog in internal/ops/ops.go.
//
// Design notes on introspection strategy:
//
//   - AI surface (internal/surface/ai.ToolSpecs):   importable; names read at runtime.
//   - JSONL surface (internal/runtime.Ops):    importable; names read at runtime.
//   - MCP surface (engine/mcp/registerTools):  unexported; names hardcoded here.
//
// When a developer adds/removes an MCP tool they must also update the hardcoded
// set below AND the catalog in ops.go — this file will then fail if they forget
// one of the two changes, making the divergence immediately visible.

import (
	"testing"

	"github.com/dev-toolings/ghostchrome/internal/ops"
	"github.com/dev-toolings/ghostchrome/internal/runtime"
	"github.com/dev-toolings/ghostchrome/internal/surface/ai"
)

// ── Known op-name sets per surface ────────────────────────────────────────────
//
// MCP is the only surface still read from a hardcoded set, derived from
// engine/mcp/tools.go registerTools (all srv.AddTool calls). If you change that
// file, update the set below AND ops.Catalog().

// mcpOps is the set of tool names registered in engine/mcp/tools.go registerTools.
var mcpOps = map[string]bool{
	"snapshot":   true,
	"navigate":   true,
	"click":      true,
	"type":       true,
	"select":     true,
	"press":      true,
	"wait_for":   true,
	"eval":       true,
	"screenshot": true,
	"back":       true,
	"forward":    true,
	"hover":      true,
	"drag":       true,
	"swipe":      true,
	"emulate":    true,
	"fill_form":  true,
	"tabs":       true,
	"upload":     true,
	"dialog":     true,
}

// ── Helpers ────────────────────────────────────────────────────────────────────

// catalogIndex builds maps from op-name to Op and from surface to op-name set.
func catalogIndex() (byName map[string]ops.Op, bySurface map[string]map[string]bool) {
	catalog := ops.Catalog()
	byName = make(map[string]ops.Op, len(catalog))
	bySurface = make(map[string]map[string]bool)
	for _, op := range catalog {
		byName[op.Name] = op
		for _, s := range op.Surfaces {
			if bySurface[s] == nil {
				bySurface[s] = make(map[string]bool)
			}
			bySurface[s][op.Name] = true
		}
	}
	return byName, bySurface
}

// ── Tests ──────────────────────────────────────────────────────────────────────

// TestJSONLParity verifies that every op in the JSONL dispatch is present in
// the catalog with surface "jsonl", and vice-versa. The real surface is
// introspected at runtime via internal/runtime.Ops(), which is derived from the
// dispatch table itself — no hand-maintained copy of the op names.
func TestJSONLParity(t *testing.T) {
	_, bySurface := catalogIndex()
	catalogJSONL := bySurface["jsonl"]

	// Collect real JSONL op names from the live dispatch table.
	realJSONL := make(map[string]bool)
	for _, name := range runtime.Ops() {
		realJSONL[name] = true
	}

	// 1. Every real JSONL op must be in the catalog with surface "jsonl".
	for name := range realJSONL {
		if !catalogJSONL[name] {
			t.Errorf("JSONL op %q is handled by internal/runtime.Dispatch but is missing from ops.Catalog() with surface \"jsonl\"", name)
		}
	}

	// 2. Every catalog "jsonl" op must be in the real dispatch.
	for name := range catalogJSONL {
		if !realJSONL[name] {
			t.Errorf("catalog op %q has surface \"jsonl\" but is NOT handled by internal/runtime.Dispatch — remove the surface or add the handler", name)
		}
	}
}

// TestMCPParity verifies that every tool registered in engine/mcp/tools.go is
// present in the catalog with surface "mcp", and vice-versa.
func TestMCPParity(t *testing.T) {
	_, bySurface := catalogIndex()
	catalogMCP := bySurface["mcp"]

	// 1. Every real MCP tool must be in the catalog with surface "mcp".
	for name := range mcpOps {
		if !catalogMCP[name] {
			t.Errorf("MCP tool %q exists in engine/mcp/tools.go but is missing from ops.Catalog() with surface \"mcp\"", name)
		}
	}

	// 2. Every catalog "mcp" op must be registered in registerTools.
	for name := range catalogMCP {
		if !mcpOps[name] {
			t.Errorf("catalog op %q has surface \"mcp\" but is NOT registered in engine/mcp/tools.go registerTools — remove the surface or add the tool", name)
		}
	}
}

// TestAIParity verifies that every tool in internal/surface/ai.ToolSpecs() is present in
// the catalog with surface "ai", and vice-versa. Unlike MCP and JSONL, this test
// introspects the real surface at runtime via the exported ToolSpecs() function.
func TestAIParity(t *testing.T) {
	_, bySurface := catalogIndex()
	catalogAI := bySurface["ai"]

	// Collect real AI tool names from the live ToolSpecs().
	realAI := make(map[string]bool)
	for _, spec := range ai.ToolSpecs() {
		realAI[spec.Name] = true
	}

	// 1. Every real AI tool must be in the catalog with surface "ai".
	for name := range realAI {
		if !catalogAI[name] {
			t.Errorf("AI tool %q exists in internal/surface/ai.ToolSpecs() but is missing from ops.Catalog() with surface \"ai\"", name)
		}
	}

	// 2. Every catalog "ai" op must be in the real ToolSpecs output.
	for name := range catalogAI {
		if !realAI[name] {
			t.Errorf("catalog op %q has surface \"ai\" but is NOT returned by internal/surface/ai.ToolSpecs() — remove the surface or add the tool", name)
		}
	}
}

// TestCatalogSorted verifies the catalog is sorted alphabetically (required for
// stable contracts/commands.json diffs).
func TestCatalogSorted(t *testing.T) {
	catalog := ops.Catalog()
	for i := 1; i < len(catalog); i++ {
		if catalog[i].Name <= catalog[i-1].Name {
			t.Errorf("catalog not sorted: %q (index %d) should come after %q (index %d)",
				catalog[i-1].Name, i-1, catalog[i].Name, i)
		}
	}
}

// TestCatalogNoEmpty verifies every op has a non-empty name and summary.
func TestCatalogNoEmpty(t *testing.T) {
	for _, op := range ops.Catalog() {
		if op.Name == "" {
			t.Error("op with empty name found in catalog")
		}
		if op.Summary == "" {
			t.Errorf("op %q has empty summary", op.Name)
		}
		if len(op.Surfaces) == 0 {
			t.Errorf("op %q has no surfaces", op.Name)
		}
	}
}
