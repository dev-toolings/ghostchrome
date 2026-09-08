package cli

import (
	"testing"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
)

func TestLocatorFromEngineLocator(t *testing.T) {
	tests := []struct {
		name string
		loc  engine.Locator
		want string
	}{
		{name: "role and name", loc: engine.Locator{Role: "button", Name: "Submit"}, want: `page.getByRole("button", { name: "Submit" })`},
		{name: "text", loc: engine.Locator{Text: "Hello"}, want: `page.getByText("Hello")`},
		{name: "label fallback", loc: engine.Locator{Name: "Email"}, want: `page.getByLabel("Email")`},
		{name: "role only", loc: engine.Locator{Role: "textbox"}, want: `page.getByRole("textbox")`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := locatorFromEngineLocator(tt.loc); got != tt.want {
				t.Fatalf("locatorFromEngineLocator() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLocatorSummary(t *testing.T) {
	got := locatorSummary(engine.Locator{Role: "button", Name: "Save"})
	if got != "role=button name=Save" {
		t.Fatalf("locatorSummary = %q", got)
	}
}
