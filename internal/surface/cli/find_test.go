package cli

import (
	"strings"
	"testing"
)

func TestFindSnapshotMatchesPlainTextWithContext(t *testing.T) {
	matcher, err := newFindMatcher("add to cart", false)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := strings.Join([]string{
		`- heading "Store" [level=1]`,
		`- navigation:`,
		`  - link "Home" [ref=e1]`,
		`  - button "Add to cart" [ref=e2]`,
		`  - text: "Free shipping"`,
		`- contentinfo:`,
		`  - link "Privacy" [ref=e3]`,
		`  - link "Terms" [ref=e4]`,
	}, "\n")

	matches := findSnapshotMatches(snapshot, matcher)
	if len(matches) != 1 {
		t.Fatalf("matches = %#v, want one", matches)
	}
	if matches[0].Line != 4 || matches[0].Text != `  - button "Add to cart" [ref=e2]` {
		t.Fatalf("match = %#v, want line 4 Add to cart", matches[0])
	}
	wantContext := []string{
		`- heading "Store" [level=1]`,
		`- navigation:`,
		`  - link "Home" [ref=e1]`,
		`  - button "Add to cart" [ref=e2]`,
		`  - text: "Free shipping"`,
		`- contentinfo:`,
		`  - link "Privacy" [ref=e3]`,
	}
	if got := strings.Join(matches[0].Context, "\n"); got != strings.Join(wantContext, "\n") {
		t.Fatalf("context = %q, want %q", got, strings.Join(wantContext, "\n"))
	}
}

func TestFindSnapshotMatchesRegex(t *testing.T) {
	matcher, err := newFindMatcher(`\$[0-9]+\.[0-9]{2}`, true)
	if err != nil {
		t.Fatal(err)
	}
	matches := findSnapshotMatches("- text: \"$12.50\"\n- text: \"Unavailable\"", matcher)
	if len(matches) != 1 || matches[0].Line != 1 {
		t.Fatalf("matches = %#v, want price on line 1", matches)
	}
}

func TestFindArgumentsAndRegexErrors(t *testing.T) {
	oldRegex := flagFindRegex
	defer func() { flagFindRegex = oldRegex }()

	flagFindRegex = ""
	if err := validateFindArgs(findCmd, nil); err == nil || !strings.Contains(err.Error(), "provide text") {
		t.Fatalf("missing query error = %v", err)
	}
	flagFindRegex = "price"
	if err := validateFindArgs(findCmd, []string{"price"}); err == nil || !strings.Contains(err.Error(), "either text or --regex") {
		t.Fatalf("conflicting query error = %v", err)
	}
	if _, err := newFindMatcher("[", true); err == nil || !strings.Contains(err.Error(), "invalid --regex") {
		t.Fatalf("invalid regex error = %v", err)
	}
}
