package ops

import (
	"fmt"
)

// knownSurfaces is the closed set of surface identifiers the generator knows
// how to emit code for. A typo in Op.Surfaces is a silent no-op otherwise.
var knownSurfaces = map[string]bool{"jsonl": true, "mcp": true, "ai": true}

// Validate reports every internal inconsistency of the catalog. The generator
// refuses to write any file while this returns a non-empty slice, so a catalog
// that would produce broken or ambiguous code never reaches the surfaces.
//
// It checks the catalog against itself only: whether the surfaces actually
// implement what is declared here is no longer testable, it is generated.
func Validate() []error {
	catalog := Catalog()
	var errs []error

	seen := make(map[string]bool, len(catalog))
	aiOrder := make(map[int]string)

	for i, op := range catalog {
		if op.Name == "" {
			errs = append(errs, fmt.Errorf("op at index %d has an empty name", i))
			continue
		}
		if seen[op.Name] {
			errs = append(errs, fmt.Errorf("op %q is declared twice", op.Name))
		}
		seen[op.Name] = true
		if i > 0 && catalog[i-1].Name >= op.Name {
			errs = append(errs, fmt.Errorf("catalog not sorted: %q must come after %q", op.Name, catalog[i-1].Name))
		}
		if op.Summary == "" {
			errs = append(errs, fmt.Errorf("op %q has an empty summary", op.Name))
		}
		if len(op.Surfaces) == 0 {
			errs = append(errs, fmt.Errorf("op %q has no surfaces", op.Name))
		}

		onSurface := make(map[string]bool, len(op.Surfaces))
		for _, s := range op.Surfaces {
			if !knownSurfaces[s] {
				errs = append(errs, fmt.Errorf("op %q lists unknown surface %q", op.Name, s))
			}
			onSurface[s] = true
		}

		for _, arg := range op.Args {
			if arg.Name == "" {
				errs = append(errs, fmt.Errorf("op %q has a contract arg with an empty name", op.Name))
			}
			if arg.Type == "" {
				errs = append(errs, fmt.Errorf("op %q: contract arg %q has no type", op.Name, arg.Name))
			}
		}

		// A spec without its surface would generate nothing; a surface without
		// its spec would generate a tool with no schema. Both are catalog bugs.
		if (op.MCP != nil) != onSurface["mcp"] {
			errs = append(errs, fmt.Errorf("op %q: surface \"mcp\" listed=%v but MCP spec present=%v", op.Name, onSurface["mcp"], op.MCP != nil))
		}
		if (op.AI != nil) != onSurface["ai"] {
			errs = append(errs, fmt.Errorf("op %q: surface \"ai\" listed=%v but AI spec present=%v", op.Name, onSurface["ai"], op.AI != nil))
		}

		errs = append(errs, validateSpec(op.Name, "mcp", op.MCP)...)
		errs = append(errs, validateSpec(op.Name, "ai", op.AI)...)

		if op.AI != nil && op.AI.Order != 0 {
			if other, dup := aiOrder[op.AI.Order]; dup {
				errs = append(errs, fmt.Errorf("ops %q and %q share AI order %d", other, op.Name, op.AI.Order))
			}
			aiOrder[op.AI.Order] = op.Name
		}
	}

	return errs
}

// validateSpec checks one surface declaration.
func validateSpec(op, surface string, spec *SurfaceSpec) []error {
	if spec == nil {
		return nil
	}
	var errs []error
	if spec.Description == "" {
		errs = append(errs, fmt.Errorf("op %q: %s spec has an empty description", op, surface))
	}
	names := make(map[string]bool, len(spec.Args))
	for _, arg := range spec.Args {
		switch {
		case arg.Name == "":
			errs = append(errs, fmt.Errorf("op %q: %s spec has an arg with an empty name", op, surface))
			continue
		case names[arg.Name]:
			errs = append(errs, fmt.Errorf("op %q: %s spec declares arg %q twice", op, surface, arg.Name))
		}
		names[arg.Name] = true

		switch arg.Type {
		case ArgString, ArgInteger, ArgNumber, ArgBoolean, ArgObject:
			if arg.Items != "" {
				errs = append(errs, fmt.Errorf("op %q: %s arg %q is %s but declares Items", op, surface, arg.Name, arg.Type))
			}
		case ArgArray:
			if arg.Items == "" {
				errs = append(errs, fmt.Errorf("op %q: %s arg %q is an array without Items", op, surface, arg.Name))
			}
		default:
			errs = append(errs, fmt.Errorf("op %q: %s arg %q has unknown type %q", op, surface, arg.Name, arg.Type))
		}

		switch v := arg.Default.(type) {
		case nil:
		case string:
			if arg.Type != ArgString {
				errs = append(errs, fmt.Errorf("op %q: %s arg %q is %s with a string default", op, surface, arg.Name, arg.Type))
			}
			if len(arg.Enum) > 0 && !contains(arg.Enum, v) {
				errs = append(errs, fmt.Errorf("op %q: %s arg %q default %q is not in its enum", op, surface, arg.Name, v))
			}
		case bool:
			if arg.Type != ArgBoolean {
				errs = append(errs, fmt.Errorf("op %q: %s arg %q is %s with a bool default", op, surface, arg.Name, arg.Type))
			}
		case int, float64:
			if arg.Type != ArgNumber && arg.Type != ArgInteger {
				errs = append(errs, fmt.Errorf("op %q: %s arg %q is %s with a numeric default", op, surface, arg.Name, arg.Type))
			}
		default:
			errs = append(errs, fmt.Errorf("op %q: %s arg %q has an unsupported default type %T", op, surface, arg.Name, arg.Default))
		}

		if len(arg.Enum) > 0 && arg.Type != ArgString {
			errs = append(errs, fmt.Errorf("op %q: %s arg %q is %s with an enum (only strings are supported)", op, surface, arg.Name, arg.Type))
		}
	}
	return errs
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
