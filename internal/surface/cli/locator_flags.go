package cli

import (
	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/spf13/cobra"
)

// LocatorFlags are the shared --by-role / --by-name / --by-label / --by-text
// flags wired onto click / type / hover. They're mutually compatible with the
// positional @ref argument: if a --by-* flag is set, it wins; otherwise the
// ref is used.
type LocatorFlags struct {
	Role  string
	Name  string
	Label string
	Text  string
}

// Any reports whether at least one locator flag was set.
func (f LocatorFlags) Any() bool {
	return f.Role != "" || f.Name != "" || f.Label != "" || f.Text != ""
}

// ToLocator converts flags into an engine.Locator.
func (f LocatorFlags) ToLocator() engine.Locator {
	return engine.Locator{
		Role:  f.Role,
		Name:  f.Name,
		Label: f.Label,
		Text:  f.Text,
	}
}

// Describe renders a short label used by output helpers.
func (f LocatorFlags) Describe() string {
	loc := f.ToLocator()
	out := ""
	if loc.Role != "" {
		out += "role=" + loc.Role + " "
	}
	if loc.Name != "" {
		out += "name=" + loc.Name + " "
	}
	if loc.Label != "" {
		out += "label=" + loc.Label + " "
	}
	if loc.Text != "" {
		out += "text=" + loc.Text
	}
	return out
}

// RegisterOn installs the standard --by-* flags on cmd, writing to the fields
// of f. Call this from the init() of each command that supports locators.
func (f *LocatorFlags) RegisterOn(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.Role, "by-role", "", "Semantic locator: ARIA role (button, link, textbox, …) or short form (b/a/t)")
	cmd.Flags().StringVar(&f.Name, "by-name", "", "Semantic locator: accessible name (substring, case-insensitive)")
	cmd.Flags().StringVar(&f.Label, "by-label", "", "Semantic locator: accessible label (alias for --by-name on inputs)")
	cmd.Flags().StringVar(&f.Text, "by-text", "", "Semantic locator: visible text content contains")
}
