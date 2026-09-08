// Package gen turns the canonical ops catalog into the artefacts every surface
// reads. It is a library rather than a lump of code inside the generator binary
// so that a test can regenerate in memory and compare against what is committed:
// running `go generate` is the one manual step left in adding an op, and a stale
// generated file is the one drift the structure cannot rule out by itself.
//
// Nothing here emits behaviour. The Go files bind op names to hand-written
// methods; a new op in the catalog produces a binding that does not compile
// until its handler exists.
package gen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"sort"
	"strings"

	"github.com/dev-toolings/ghostchrome/internal/ops"
)

// File is one generated artefact: a slash-separated path relative to the repo
// root, and its full content.
type File struct {
	Path    string
	Content []byte
}

// Files renders every artefact from the catalog. It returns an error only for
// a catalog ops.Validate would already reject, so callers that validate first
// can treat an error here as a bug in this package.
func Files() ([]File, error) {
	if errs := ops.Validate(); len(errs) > 0 {
		return nil, fmt.Errorf("catalog is invalid: %v", errs[0])
	}

	catalog := ops.Catalog()
	// Ensure deterministic order (Catalog already returns sorted, but guard anyway).
	sort.Slice(catalog, func(i, j int) bool {
		return catalog[i].Name < catalog[j].Name
	})

	contract, err := contract(catalog)
	if err != nil {
		return nil, err
	}
	files := []File{{Path: "contracts/commands.json", Content: contract}}

	for _, f := range []struct {
		path string
		src  []byte
	}{
		{"internal/runtime/handlers_gen.go", runtimeHandlers(catalog)},
		{"internal/surface/mcp/tools_gen.go", mcpTools(catalog)},
		{"internal/surface/ai/tools_gen.go", aiTools(catalog)},
	} {
		formatted, err := format.Source(f.src)
		if err != nil {
			return nil, fmt.Errorf("%s: %w\n%s", f.path, err, f.src)
		}
		files = append(files, File{Path: f.path, Content: formatted})
	}
	return files, nil
}

// ── contract ───────────────────────────────────────────────────────────────────

// contract renders the frozen op contract both SDKs are typed against. Only the
// tagged fields of ops.Op reach it: the per-surface specs are deliberately out,
// the contract describes ops, not schemas.
func contract(catalog []ops.Op) ([]byte, error) {
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal contract: %w", err)
	}
	// Append trailing newline for VCS hygiene.
	return append(data, '\n'), nil
}

// ── JSONL surface ──────────────────────────────────────────────────────────────

func runtimeHandlers(catalog []ops.Op) []byte {
	var b bytes.Buffer
	header(&b, "runtime")
	b.WriteString(`
// handlers binds every catalog op carrying the "jsonl" surface to its
// hand-written method on *Session. Dispatch routes through this table and Ops
// enumerates it, so the JSONL surface is exactly the catalog's "jsonl" set by
// construction, with no list left to keep in sync.
//
// The method bodies live in ops.go. Adding an op to the catalog regenerates
// this table, and the build fails until its method exists.
var handlers = map[string]handler{
`)
	for _, op := range catalog {
		if !on(op, "jsonl") {
			continue
		}
		fmt.Fprintf(&b, "\t%q: (*Session).op%s,\n", op.Name, ident(op.Name))
	}
	b.WriteString("}\n")
	return b.Bytes()
}

// ── MCP surface ────────────────────────────────────────────────────────────────

func mcpTools(catalog []ops.Op) []byte {
	var b bytes.Buffer
	header(&b, "mcp")
	b.WriteString(`
import mcpgo "github.com/mark3labs/mcp-go/mcp"

// toolDefs pairs every catalog op carrying the "mcp" surface with its
// hand-written handler method. registerTools registers it and Tools enumerates
// it, so the registered set is the catalog's "mcp" set by construction.
//
// Descriptions, enums and defaults come from the catalog's MCP SurfaceSpec:
// they are what an MCP client sees in tools/list, so they are declared once
// there rather than restated here. The handler bodies stay hand-written in
// tools.go.
func toolDefs() []toolDef {
	return []toolDef{
`)
	for _, op := range catalog {
		if !on(op, "mcp") {
			continue
		}
		fmt.Fprintf(&b, "\t\t{mcpgo.NewTool(%q,\n", op.Name)
		fmt.Fprintf(&b, "\t\t\tmcpgo.WithDescription(%s),\n", quote(op.MCP.Description))
		for _, arg := range op.MCP.Args {
			fmt.Fprintf(&b, "\t\t\t%s,\n", mcpArg(op.Name, arg))
		}
		fmt.Fprintf(&b, "\t\t), (*Server).handle%s},\n\n", ident(op.Name))
	}
	b.WriteString("\t}\n}\n")
	return b.Bytes()
}

// mcpArg renders one argument as an mcp-go ToolOption.
func mcpArg(op string, arg ops.SurfaceArg) string {
	var with string
	switch arg.Type {
	case ops.ArgString:
		with = "WithString"
	case ops.ArgNumber:
		with = "WithNumber"
	case ops.ArgInteger:
		with = "WithInteger"
	case ops.ArgBoolean:
		with = "WithBoolean"
	case ops.ArgObject:
		with = "WithObject"
	case ops.ArgArray:
		with = "WithArray"
	default:
		panic(unreachable(op, arg.Name, "type "+string(arg.Type)))
	}

	parts := []string{fmt.Sprintf("mcpgo.%s(%q", with, arg.Name)}
	if arg.Required {
		parts = append(parts, "mcpgo.Required()")
	}
	if arg.Description != "" {
		parts = append(parts, fmt.Sprintf("mcpgo.Description(%s)", quote(arg.Description)))
	}
	if len(arg.Enum) > 0 {
		parts = append(parts, fmt.Sprintf("mcpgo.Enum(%s)", quoteList(arg.Enum)))
	}
	if arg.Type == ops.ArgArray {
		switch arg.Items {
		case ops.ArgString:
			parts = append(parts, "mcpgo.WithStringItems()")
		case ops.ArgNumber:
			parts = append(parts, "mcpgo.WithNumberItems()")
		case ops.ArgInteger:
			parts = append(parts, "mcpgo.WithIntegerItems()")
		case ops.ArgBoolean:
			parts = append(parts, "mcpgo.WithBooleanItems()")
		default:
			panic(unreachable(op, arg.Name, "item type "+string(arg.Items)))
		}
	}
	switch v := arg.Default.(type) {
	case nil:
	case string:
		parts = append(parts, fmt.Sprintf("mcpgo.DefaultString(%s)", quote(v)))
	case bool:
		parts = append(parts, fmt.Sprintf("mcpgo.DefaultBool(%t)", v))
	case int:
		parts = append(parts, fmt.Sprintf("mcpgo.DefaultNumber(%d)", v))
	case float64:
		parts = append(parts, fmt.Sprintf("mcpgo.DefaultNumber(%v)", v))
	default:
		panic(unreachable(op, arg.Name, fmt.Sprintf("default %T", arg.Default)))
	}
	return strings.Join(parts, ", ") + ")"
}

// ── AI surface ─────────────────────────────────────────────────────────────────

func aiTools(catalog []ops.Op) []byte {
	specs := make([]ops.Op, 0, len(catalog))
	for _, op := range catalog {
		if on(op, "ai") {
			specs = append(specs, op)
		}
	}
	// Order carries a weak recall bias for the model, so the catalog fixes it.
	// Ops that declare no order (0) fall to the end, alphabetically.
	sort.SliceStable(specs, func(i, j int) bool {
		a, b := specs[i].AI.Order, specs[j].AI.Order
		switch {
		case a == b:
			return specs[i].Name < specs[j].Name
		case a == 0:
			return false
		case b == 0:
			return true
		}
		return a < b
	})

	var b bytes.Buffer
	header(&b, "ai")
	b.WriteString(`
// ToolSpecs returns the tool catalog exposed to the LLM. The names match the
// JSONL ops dispatched by internal/runtime.Session.Dispatch — this is
// intentional: the LLM can read CLAUDE.md / agent docs and pick the same op
// names a human operator would.
//
// Schemas are deliberately minimal — overspec'd schemas hurt model recall
// (extra fields the model has to reason about) without buying us safety beyond
// what the agent ops already enforce at runtime. That is why an op's AI schema
// is narrower than its JSONL contract: internal/ops declares both widths.
func ToolSpecs() []ToolSpec {
	return []ToolSpec{
`)
	for _, op := range specs {
		b.WriteString("\t\t{\n")
		fmt.Fprintf(&b, "\t\t\tName:        %q,\n", op.Name)
		fmt.Fprintf(&b, "\t\t\tDescription: %s,\n", quote(op.AI.Description))
		b.WriteString("\t\t\tInputSchema: map[string]any{\n")
		b.WriteString("\t\t\t\t\"type\": \"object\",\n")
		if len(op.AI.Args) == 0 {
			b.WriteString("\t\t\t\t\"properties\": map[string]any{},\n")
		} else {
			b.WriteString("\t\t\t\t\"properties\": map[string]any{\n")
			for _, arg := range op.AI.Args {
				fmt.Fprintf(&b, "\t\t\t\t\t%q: %s,\n", arg.Name, aiSchema(op.Name, arg))
			}
			b.WriteString("\t\t\t\t},\n")
		}
		var required []string
		for _, arg := range op.AI.Args {
			if arg.Required {
				required = append(required, arg.Name)
			}
		}
		if len(required) > 0 {
			fmt.Fprintf(&b, "\t\t\t\t\"required\": []string{%s},\n", quoteList(required))
		}
		b.WriteString("\t\t\t},\n\t\t},\n")
	}
	b.WriteString("\t}\n}\n")
	return b.Bytes()
}

// aiSchema renders one argument as a JSON Schema fragment. The AI surface
// hand-rolls its schemas as maps instead of going through a helper library, so
// the generator emits the same map literals the file used to hold.
func aiSchema(op string, arg ops.SurfaceArg) string {
	parts := []string{fmt.Sprintf("%q: %q", "type", string(arg.Type))}
	if arg.Description != "" {
		parts = append(parts, fmt.Sprintf("%q: %s", "description", quote(arg.Description)))
	}
	if len(arg.Enum) > 0 {
		parts = append(parts, fmt.Sprintf("%q: []string{%s}", "enum", quoteList(arg.Enum)))
	}
	if arg.Type == ops.ArgArray {
		parts = append(parts, fmt.Sprintf("%q: map[string]any{%q: %q}", "items", "type", string(arg.Items)))
	}
	if arg.Default != nil {
		panic(unreachable(op, arg.Name, "an AI default, which the schema has no keyword for"))
	}
	return "map[string]any{" + strings.Join(parts, ", ") + "}"
}

// ── shared ─────────────────────────────────────────────────────────────────────

func header(b *bytes.Buffer, pkg string) {
	b.WriteString("// Code generated by internal/ops/cmd/gen from internal/ops/ops.go. DO NOT EDIT.\n\n")
	fmt.Fprintf(b, "package %s\n", pkg)
}

func on(op ops.Op, surface string) bool {
	for _, s := range op.Surfaces {
		if s == surface {
			return true
		}
	}
	return false
}

// initialisms keeps generated identifiers idiomatic: op "url" binds to opURL,
// not opUrl.
var initialisms = map[string]string{
	"url":  "URL",
	"id":   "ID",
	"js":   "JS",
	"dom":  "DOM",
	"html": "HTML",
}

// ident turns an op name into the Go identifier suffix its handler uses:
// "wait_for" -> "WaitFor", "url" -> "URL".
func ident(name string) string {
	var out strings.Builder
	for _, word := range strings.Split(name, "_") {
		if word == "" {
			continue
		}
		if up, ok := initialisms[word]; ok {
			out.WriteString(up)
			continue
		}
		out.WriteString(strings.ToUpper(word[:1]))
		out.WriteString(word[1:])
	}
	return out.String()
}

// quote renders a Go string literal. Always interpreted, never raw: the
// descriptions carry backquotes, quotes and em dashes, and a raw literal
// cannot hold the first of those.
func quote(s string) string {
	return fmt.Sprintf("%q", s)
}

func quoteList(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = quote(v)
	}
	return strings.Join(quoted, ", ")
}

// unreachable describes a case ops.Validate is supposed to have rejected
// before any of this ran.
func unreachable(op, arg, what string) string {
	return fmt.Sprintf("gen: op %q arg %q: %s should have been rejected by ops.Validate", op, arg, what)
}
