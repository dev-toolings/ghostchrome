//go:build ignore

// gen turns the canonical ops catalog into the artefacts every surface reads.
// Invoked via: go generate ./internal/ops/...
// or directly:  go run ./internal/ops/cmd/gen/main.go
//
// It writes:
//
//	contracts/commands.json            the frozen contract the SDKs are typed against
//	internal/surface/mcp/tools_gen.go  MCP tool schemas paired with their handlers
//
// The generated Go files bind to hand-written methods and never contain behaviour:
// a new op in the catalog produces a binding that does not compile until its
// handler is written, which is the point.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/dev-toolings/ghostchrome/internal/ops"
)

func main() {
	if errs := ops.Validate(); len(errs) > 0 {
		for _, err := range errs {
			fmt.Fprintf(os.Stderr, "gen: catalog: %v\n", err)
		}
		fmt.Fprintf(os.Stderr, "gen: %d catalog error(s), nothing written\n", len(errs))
		os.Exit(1)
	}

	catalog := ops.Catalog()
	// Ensure deterministic order (Catalog already returns sorted, but guard anyway).
	sort.Slice(catalog, func(i, j int) bool {
		return catalog[i].Name < catalog[j].Name
	})

	root := repoRoot()
	writeContract(root, catalog)
	writeGo(filepath.Join(root, "internal", "surface", "mcp", "tools_gen.go"), mcpTools(catalog))
}

// ── contract ───────────────────────────────────────────────────────────────────

func writeContract(root string, catalog []ops.Op) {
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		fatal("marshal: %v", err)
	}
	// Append trailing newline for VCS hygiene.
	data = append(data, '\n')

	out := filepath.Join(root, "contracts", "commands.json")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		fatal("mkdir: %v", err)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		fatal("write %s: %v", out, err)
	}
	fmt.Printf("gen: wrote %s (%d ops)\n", out, len(catalog))
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
// there rather than restated here. The handler bodies stay hand-written below
// in tools.go.
func toolDefs() []toolDef {
	return []toolDef{
`)
	for _, op := range catalog {
		if !on(op, "mcp") {
			continue
		}
		spec := op.MCP
		fmt.Fprintf(&b, "\t\t{mcpgo.NewTool(%q,\n", op.Name)
		fmt.Fprintf(&b, "\t\t\tmcpgo.WithDescription(%s),\n", quote(spec.Description))
		for _, arg := range spec.Args {
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
		fatal("mcp: op %q arg %q: unsupported type %q", op, arg.Name, arg.Type)
	}

	parts := []string{fmt.Sprintf("mcpgo.%s(%q", with, arg.Name)}
	if arg.Required {
		parts = append(parts, "mcpgo.Required()")
	}
	if arg.Description != "" {
		parts = append(parts, fmt.Sprintf("mcpgo.Description(%s)", quote(arg.Description)))
	}
	if len(arg.Enum) > 0 {
		quoted := make([]string, len(arg.Enum))
		for i, v := range arg.Enum {
			quoted[i] = fmt.Sprintf("%q", v)
		}
		parts = append(parts, fmt.Sprintf("mcpgo.Enum(%s)", strings.Join(quoted, ", ")))
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
			fatal("mcp: op %q arg %q: unsupported array item type %q", op, arg.Name, arg.Items)
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
		fatal("mcp: op %q arg %q: unsupported default %T", op, arg.Name, arg.Default)
	}
	return strings.Join(parts, ", ") + ")"
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

func writeGo(path string, src []byte) {
	formatted, err := format.Source(src)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen: %s: %v\n---\n%s\n---\n", path, err, src)
		os.Exit(1)
	}
	if err := os.WriteFile(path, formatted, 0o644); err != nil {
		fatal("write %s: %v", path, err)
	}
	fmt.Printf("gen: wrote %s\n", path)
}

// repoRoot resolves the repository root relative to this source file so the
// generator works regardless of the caller's working directory.
func repoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		fatal("cannot resolve source path")
	}
	// file = .../internal/ops/cmd/gen/main.go
	// repo root is 4 levels up (gen/ -> cmd/ -> ops/ -> internal/ -> root)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gen: "+format+"\n", args...)
	os.Exit(1)
}
