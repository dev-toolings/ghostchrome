package engine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

var playwrightLocatorPattern = regexp.MustCompile(`^(?:page\.)?getBy(Role|Text|Label)\(\s*("(?:\\.|[^"\\])*")\s*(?:,\s*\{\s*name\s*:\s*("(?:\\.|[^"\\])*")\s*\})?\s*\)$`)

// xpathLiteral safely escapes a string for use as an XPath string literal.
// It handles strings containing both single and double quotes by using the
// concat() function to build a safe literal that cannot be injected.
func xpathLiteral(s string) string {
	// If no quotes, simple case
	if !strings.Contains(s, "'") && !strings.Contains(s, "\"") {
		return "'" + s + "'"
	}
	// If only double quotes, wrap in single quotes
	if !strings.Contains(s, "'") {
		return "'" + s + "'"
	}
	// If only single quotes, wrap in double quotes
	if !strings.Contains(s, "\"") {
		return "\"" + s + "\""
	}
	// Mixed quotes: use concat() to build a literal safely
	parts := strings.Split(s, "'")
	var b strings.Builder
	b.WriteString("concat(")
	for i, p := range parts {
		if i > 0 {
			b.WriteString(", \"'\", ")
		}
		b.WriteString("'")
		b.WriteString(p)
		b.WriteString("'")
	}
	b.WriteString(")")
	return b.String()
}

// Locator describes a semantic element match. At least one field must be set.
// When multiple fields are set, the match is conjunctive (all must hold).
type Locator struct {
	// Role matches the ARIA role (see engine.interactiveRoles, engine.skeletonRoles).
	// Accepts canonical role strings or their one-letter agent abbreviation
	// ("b"=button, "a"=link, "t"=textbox, etc.).
	Role string

	// Name matches the accessible name. Comparison is case-insensitive and uses
	// substring matching, so "Sign in" matches "Sign in now".
	Name string

	// Label matches the accessible label derived from <label for=...> or
	// aria-labelledby. In Chromium's a11y tree, that's exposed as the name of
	// a textbox / combobox — so this is equivalent to Name for inputs.
	// Included as a separate field for ergonomic CLI flags (--by-label).
	Label string

	// Text matches via page-wide text search — like Playwright's getByText.
	// When set, Role is ignored.
	Text string
}

// IsEmpty reports whether the locator has no criteria set.
func (l Locator) IsEmpty() bool {
	return l.Role == "" && l.Name == "" && l.Label == "" && l.Text == ""
}

// ResolveByLocator returns the single visible element matching the locator. Matching
// strategy:
//   - If Text is set: XPath text contains (case-insensitive). Matches <button>,
//     <a>, <label>, generic text containers.
//   - Else: extract the a11y tree at skeleton level and filter by
//     (role, name|label).
//
// Zero or multiple visible matches are errors. This strictness is shared by
// action --by-* flags and generated Playwright locator strings so an ambiguous
// agent instruction can never silently act on the first element in tree order.
func ResolveByLocator(page *rod.Page, loc Locator) (*rod.Element, error) {
	if loc.IsEmpty() {
		return nil, fmt.Errorf("locator: at least one of --by-role, --by-text, --by-name, --by-label required")
	}
	return resolveStrictByLocator(page, loc)
}

// ResolveTarget resolves one interaction target. Targets may be snapshot refs
// (@N or playwright-cli eN), a strict CSS selector, or the small Playwright
// locator subset emitted by generate-locator: getByRole, getByText, and
// getByLabel (with an optional { name: "..." } for getByRole).
//
// CSS selectors must match exactly one element. This deliberately avoids the
// accidental-first-match behavior that is especially unsafe for agent input.
func ResolveTarget(page *rod.Page, target string, snapshot *PageSnapshot) (*rod.Element, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, fmt.Errorf("target is required")
	}
	if strings.HasPrefix(InternalRef(target), "@") {
		return ResolveRef(page, target, snapshot)
	}
	if locator, isLocator, err := parsePlaywrightLocator(target); isLocator {
		if err != nil {
			return nil, err
		}
		return resolveStrictByLocator(page, locator)
	}

	elements, err := page.Elements(target)
	if err != nil {
		return nil, fmt.Errorf("CSS selector %q: %w", target, err)
	}
	if len(elements) != 1 {
		return nil, fmt.Errorf("CSS selector %q matched %d elements; target must be unique", target, len(elements))
	}
	return elements[0], nil
}

// resolveStrictByLocator matches the strict target semantics used by CSS
// selectors: exactly one visible element must be eligible for interaction.
func resolveStrictByLocator(page *rod.Page, loc Locator) (*rod.Element, error) {
	if loc.Text != "" {
		return resolveByTextStrict(page, loc.Text)
	}

	role := normaliseRole(loc.Role)
	name := pickName(loc)
	result, err := proto.AccessibilityGetFullAXTree{}.Call(page)
	if err != nil {
		return nil, fmt.Errorf("a11y tree: %w", err)
	}

	seen := make(map[proto.DOMBackendNodeID]struct{})
	matches := make([]*rod.Element, 0, 1)
	for _, n := range result.Nodes {
		if !roleMatches(n, role) || !nameMatches(n, name) || n.BackendDOMNodeID == 0 {
			continue
		}
		if loc.Label != "" && loc.Role == "" && !labelTargetRole(n) {
			continue
		}
		if _, ok := seen[n.BackendDOMNodeID]; ok {
			continue
		}
		el, err := page.ElementFromNode(&proto.DOMNode{BackendNodeID: n.BackendDOMNodeID})
		if err != nil {
			continue
		}
		visible, err := el.Visible()
		if err != nil || !visible {
			continue
		}
		seen[n.BackendDOMNodeID] = struct{}{}
		matches = append(matches, el)
	}
	return requireUniqueLocatorMatch(matches, fmt.Sprintf("role=%q name=%q", role, name))
}

func labelTargetRole(n *proto.AccessibilityAXNode) bool {
	switch strings.ToLower(axValueStr(n.Role)) {
	case "textbox", "combobox", "checkbox", "radio", "switch", "slider", "spinbutton", "listbox", "button":
		return true
	default:
		return false
	}
}

func resolveByTextStrict(page *rod.Page, text string) (*rod.Element, error) {
	literal := xpathLiteral(strings.ToLower(text))
	xpath := fmt.Sprintf(
		`//*[self::button or self::a or self::label or self::span or self::div or self::li or self::p or self::h1 or self::h2 or self::h3 or self::h4 or self::h5 or self::h6][contains(translate(normalize-space(.), 'ABCDEFGHIJKLMNOPQRSTUVWXYZÀÂÄÉÈÊËÏÎÔÙÛÜÇ', 'abcdefghijklmnopqrstuvwxyzàâäéèêëïîôùûüç'), %s)]`,
		literal,
	)
	els, err := page.ElementsX(xpath)
	if err != nil {
		return nil, fmt.Errorf("text search: %w", err)
	}
	matches := make([]*rod.Element, 0, len(els))
	for _, el := range els {
		visible, err := el.Visible()
		if err == nil && visible {
			matches = append(matches, el)
		}
	}
	return requireUniqueLocatorMatch(matches, fmt.Sprintf("text=%q", text))
}

func requireUniqueLocatorMatch(matches []*rod.Element, description string) (*rod.Element, error) {
	if len(matches) != 1 {
		return nil, &LocatorMatchCountError{Description: description, Count: len(matches)}
	}
	return matches[0], nil
}

// LocatorMatchCountError identifies a strict semantic-locator cardinality
// failure. Waiters retry zero matches, but reject ambiguity immediately.
type LocatorMatchCountError struct {
	Description string
	Count       int
}

func (e *LocatorMatchCountError) Error() string {
	return fmt.Sprintf("playwright locator %s matched %d visible elements; target must be unique", e.Description, e.Count)
}

func parsePlaywrightLocator(target string) (Locator, bool, error) {
	matches := playwrightLocatorPattern.FindStringSubmatch(strings.TrimSpace(target))
	if matches == nil {
		return Locator{}, false, nil
	}

	var value string
	if err := json.Unmarshal([]byte(matches[2]), &value); err != nil {
		return Locator{}, true, fmt.Errorf("playwright locator %q: invalid string: %w", target, err)
	}

	switch matches[1] {
	case "Text":
		if matches[3] != "" {
			return Locator{}, true, fmt.Errorf("playwright locator %q: getByText does not accept options", target)
		}
		return Locator{Text: value}, true, nil
	case "Label":
		if matches[3] != "" {
			return Locator{}, true, fmt.Errorf("playwright locator %q: getByLabel does not accept options", target)
		}
		return Locator{Label: value}, true, nil
	case "Role":
		loc := Locator{Role: value}
		if matches[3] != "" {
			if err := json.Unmarshal([]byte(matches[3]), &loc.Name); err != nil {
				return Locator{}, true, fmt.Errorf("playwright locator %q: invalid name: %w", target, err)
			}
		}
		return loc, true, nil
	default:
		return Locator{}, true, fmt.Errorf("unsupported Playwright locator %q", target)
	}
}

func pickName(loc Locator) string {
	if loc.Name != "" {
		return loc.Name
	}
	return loc.Label
}

func roleMatches(n *proto.AccessibilityAXNode, want string) bool {
	if want == "" {
		return true
	}
	got := strings.ToLower(axValueStr(n.Role))
	want = strings.ToLower(want)
	return got == want
}

func nameMatches(n *proto.AccessibilityAXNode, want string) bool {
	if want == "" {
		return true
	}
	got := strings.ToLower(axValueStr(n.Name))
	return strings.Contains(got, strings.ToLower(want))
}

// normaliseRole expands one-letter role codes to full role names. Unknown
// inputs are returned as-is so users can pass "dialog", "tooltip", etc.
func normaliseRole(r string) string {
	switch strings.ToLower(strings.TrimSpace(r)) {
	case "":
		return ""
	case "b", "button":
		return "button"
	case "a", "link":
		return "link"
	case "t", "textbox", "input":
		return "textbox"
	case "c", "checkbox":
		return "checkbox"
	case "r", "radio":
		return "radio"
	case "s", "combobox", "select":
		return "combobox"
	case "m", "menuitem":
		return "menuitem"
	case "x", "tab":
		return "tab"
	case "h", "heading":
		return "heading"
	case "img", "image":
		return "image"
	}
	return strings.ToLower(r)
}
