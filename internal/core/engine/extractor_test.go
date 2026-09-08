package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-rod/rod/lib/proto"
)

func TestFormatTextBasic(t *testing.T) {
	checked := true
	result := &ExtractionResult{
		Nodes: []ExtractedNode{
			{
				Role:  "heading",
				Name:  "Example Domain",
				Level: 1,
			},
			{
				Role: "link",
				Ref:  "@1",
				Name: "About Us",
				Href: "/about",
			},
			{
				Role: "navigation",
				Name: "Main Navigation",
				Children: []ExtractedNode{
					{
						Role: "link",
						Ref:  "@2",
						Name: "Home",
						Href: "/home",
					},
					{
						Role: "link",
						Ref:  "@3",
						Name: "Products",
						Href: "/products",
					},
				},
			},
			{
				Role: "form",
				Children: []ExtractedNode{
					{
						Role: "textbox",
						Ref:  "@4",
						Type: "text",
						Name: "Search",
					},
					{
						Role: "button",
						Ref:  "@5",
						Name: "Submit",
					},
				},
			},
			{
				Role: "paragraph",
				Name: "This domain is for illustrative examples.",
			},
			{
				Role:    "checkbox",
				Ref:     "@6",
				Name:    "Accept terms",
				Checked: &checked,
			},
		},
		Refs:  map[string]ExtractedNode{},
		Stats: ExtractionStats{},
	}

	text := FormatText(result)

	// Verify key elements are present.
	expectations := []string{
		"[h1] Example Domain",
		"[link @1 href=/about] About Us",
		"[nav] Main Navigation",
		"  [link @2 href=/home] Home",
		"  [link @3 href=/products] Products",
		"[form]",
		"  [input @4 type=text] Search",
		"  [btn @5] Submit",
		"[p] This domain is for illustrative examples.",
		"[checkbox @6 checked] Accept terms",
	}

	for _, exp := range expectations {
		if !strings.Contains(text, exp) {
			t.Errorf("expected output to contain %q\nGot:\n%s", exp, text)
		}
	}
}

func TestFormatTextIndentation(t *testing.T) {
	result := &ExtractionResult{
		Nodes: []ExtractedNode{
			{
				Role: "main",
				Children: []ExtractedNode{
					{
						Role: "navigation",
						Children: []ExtractedNode{
							{
								Role: "link",
								Ref:  "@1",
								Name: "Deep Link",
								Href: "/deep",
							},
						},
					},
				},
			},
		},
		Refs:  map[string]ExtractedNode{},
		Stats: ExtractionStats{},
	}

	text := FormatText(result)

	if !strings.Contains(text, "    [link @1 href=/deep] Deep Link") {
		t.Errorf("expected 4-space indent for depth-2 node\nGot:\n%s", text)
	}
}

func TestFormatPlaywrightSnapshot(t *testing.T) {
	result := &ExtractionResult{
		Nodes: []ExtractedNode{
			{Role: "heading", Name: "todos", Level: 1},
			{Role: "textbox", Ref: "@5", Name: "What needs to be done?"},
			{
				Role: "listitem",
				Children: []ExtractedNode{
					{Role: "checkbox", Ref: "@10", Name: "Toggle Todo"},
					{Role: "StaticText", Name: "Buy groceries"},
				},
			},
		},
	}

	got := FormatPlaywrightSnapshot(result)
	expectations := []string{
		`- heading "todos" [level=1]`,
		`- textbox "What needs to be done?" [ref=e5]`,
		`- listitem:`,
		`  - checkbox "Toggle Todo" [ref=e10]`,
		`  - text: "Buy groceries"`,
	}
	for _, want := range expectations {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in snapshot:\n%s", want, got)
		}
	}
}

func TestFormatPlaywrightSnapshotIncludesBoxes(t *testing.T) {
	result := &ExtractionResult{Nodes: []ExtractedNode{{
		Ref:  "@3",
		Role: "button",
		Name: "Save",
		Box:  &ElementBox{X: 10, Y: 20, Width: 80, Height: 30},
	}}}
	got := FormatPlaywrightSnapshot(result)
	if !strings.Contains(got, `[ref=e3 box=10,20,80,30]`) {
		t.Fatalf("snapshot with box = %q", got)
	}
}

func TestPlaywrightRefConversion(t *testing.T) {
	if got := PlaywrightRef("@34"); got != "e34" {
		t.Fatalf("PlaywrightRef(@34) = %q", got)
	}
	if got := InternalRef("e34"); got != "@34" {
		t.Fatalf("InternalRef(e34) = %q", got)
	}
	if got := InternalRef("@7"); got != "@7" {
		t.Fatalf("InternalRef(@7) = %q", got)
	}
}

func TestLimitExtractionDepth(t *testing.T) {
	result := &ExtractionResult{
		Nodes: []ExtractedNode{
			{
				Role: "main",
				Children: []ExtractedNode{
					{
						Role: "navigation",
						Children: []ExtractedNode{
							{Role: "link", Ref: "@1", Name: "Deep Link"},
						},
					},
				},
			},
		},
		Refs: map[string]ExtractedNode{"@1": {Role: "link", Ref: "@1", Name: "Deep Link"}},
	}

	limited := LimitExtractionDepth(result, 1)
	if len(limited.Nodes) != 1 {
		t.Fatalf("expected one root node, got %d", len(limited.Nodes))
	}
	if len(limited.Nodes[0].Children) != 1 {
		t.Fatalf("expected depth-1 child to remain")
	}
	if len(limited.Nodes[0].Children[0].Children) != 0 {
		t.Fatalf("expected depth-2 child to be removed")
	}
	if _, ok := limited.Refs["@1"]; ok {
		t.Fatalf("deep ref should not remain in limited refs")
	}
}

func TestExtractionForRef(t *testing.T) {
	result := &ExtractionResult{
		Nodes: []ExtractedNode{
			{
				Role: "main",
				Children: []ExtractedNode{
					{Role: "button", Ref: "@1", Name: "Save"},
				},
			},
		},
		Refs: map[string]ExtractedNode{
			"@1": {Role: "button", Ref: "@1", Name: "Save"},
		},
	}

	scoped, ok := ExtractionForRef(result, "@1")
	if !ok {
		t.Fatal("expected ref to resolve")
	}
	if got := FormatText(scoped); !strings.Contains(got, "[btn @1] Save") {
		t.Fatalf("unexpected scoped output: %s", got)
	}
	if _, ok := ExtractionForRef(result, "@2"); ok {
		t.Fatal("missing ref should not resolve")
	}
}

// TestMarshalJSONStripsRefChildren guards the wire-compaction fix: the JSONL /
// SDK payload must NOT duplicate each interactive node's subtree inside the
// refs map (that subtree already lives in nodes). The in-memory struct keeps
// its children so ExtractionForRef still works.
func TestMarshalJSONStripsRefChildren(t *testing.T) {
	combo := ExtractedNode{
		Role: "combobox", Ref: "@1", Name: "Country",
		Children: []ExtractedNode{
			{Role: "StaticText", Name: "UNIQUE_CHILD_TOKEN"},
		},
	}
	result := &ExtractionResult{
		Nodes: []ExtractedNode{
			{Role: "form", Children: []ExtractedNode{combo}},
		},
		Refs: map[string]ExtractedNode{"@1": combo},
	}

	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var wire struct {
		Nodes []map[string]any          `json:"nodes"`
		Refs  map[string]map[string]any `json:"refs"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// refs entry keeps its scalar metadata but drops the redundant subtree.
	ref, ok := wire.Refs["@1"]
	if !ok {
		t.Fatal("refs @1 missing")
	}
	if _, hasChildren := ref["children"]; hasChildren {
		t.Fatalf("refs @1 must not carry children on the wire: %v", ref)
	}
	if ref["name"] != "Country" {
		t.Fatalf("refs @1 lost its metadata: %v", ref)
	}

	// The subtree is still present exactly once — in nodes.
	if !strings.Contains(string(raw), "UNIQUE_CHILD_TOKEN") {
		t.Fatal("child subtree should survive inside nodes")
	}
	if strings.Count(string(raw), "UNIQUE_CHILD_TOKEN") != 1 {
		t.Fatalf("child subtree duplicated on the wire: %s", raw)
	}

	// In-memory struct is untouched: ExtractionForRef still sees the subtree.
	if len(result.Refs["@1"].Children) != 1 {
		t.Fatal("in-memory refs children must be preserved for ExtractionForRef")
	}
}

func TestShouldInclude(t *testing.T) {
	tests := []struct {
		role  string
		name  string
		level ExtractLevel
		want  bool
	}{
		{"button", "Click", LevelSkeleton, true},
		{"heading", "Title", LevelSkeleton, true},
		{"paragraph", "Text", LevelSkeleton, false},
		{"paragraph", "Text", LevelContent, true},
		{"StaticText", "hello", LevelContent, true},
		{"StaticText", "hello", LevelSkeleton, false},
		{"generic", "", LevelFull, false},
		{"none", "", LevelFull, false},
		{"group", "Named Group", LevelFull, true},
		{"group", "", LevelFull, false},
		{"switch", "Notifications", LevelSkeleton, true},
		{"slider", "Volume", LevelSkeleton, true},
		{"option", "France", LevelSkeleton, true},
	}

	for _, tt := range tests {
		got := shouldInclude(tt.role, tt.name, tt.level)
		if got != tt.want {
			t.Errorf("shouldInclude(%q, %q, %q) = %v, want %v", tt.role, tt.name, tt.level, got, tt.want)
		}
	}
}

func TestRoleToTag(t *testing.T) {
	tests := []struct {
		role string
		want string
	}{
		{"button", "btn"},
		{"link", "link"},
		{"textbox", "input"},
		{"heading", "h"},
		{"navigation", "nav"},
		{"complementary", "aside"},
		{"contentinfo", "footer"},
		{"paragraph", "p"},
		{"unknown_role", "unknown_role"},
	}

	for _, tt := range tests {
		got := roleToTag(tt.role)
		if got != tt.want {
			t.Errorf("roleToTag(%q) = %q, want %q", tt.role, got, tt.want)
		}
	}
}

// sharedResult builds a minimal ExtractionResult for boundary-marker tests.
func sharedBoundaryResult() *ExtractionResult {
	return &ExtractionResult{
		Nodes: []ExtractedNode{
			{Role: "heading", Name: "Example Domain", Level: 1},
			{Role: "link", Ref: "@1", Name: "More information", Href: "/info"},
			{Role: "button", Ref: "@2", Name: "Submit"},
		},
		Refs:  map[string]ExtractedNode{},
		Stats: ExtractionStats{},
	}
}

// TestContentBoundaryDefaultOff ensures that without ContentBoundary, output is
// byte-identical to the baseline (no markers introduced).
func TestContentBoundaryDefaultOff(t *testing.T) {
	result := sharedBoundaryResult()

	// Both calls must produce the same output.
	baseline := FormatText(result)
	withOff := FormatTextProfile(result, ProfileHuman("text"))

	if baseline != withOff {
		t.Errorf("FormatTextProfile with default profile differs from FormatText\ngot: %q\nwant: %q", withOff, baseline)
	}
	if strings.Contains(baseline, ContentBoundaryOpen) {
		t.Errorf("default output must not contain boundary marker %q, got:\n%s", ContentBoundaryOpen, baseline)
	}
}

// TestContentBoundaryHumanProfile verifies the human profile applies markers
// around node names/values when ContentBoundary=true.
func TestContentBoundaryHumanProfile(t *testing.T) {
	result := sharedBoundaryResult()

	p := ProfileHuman("text")
	p.ContentBoundary = true
	marked := FormatTextProfile(result, p)

	// Every text label must be wrapped.
	if !strings.Contains(marked, ContentBoundaryOpen+"Example Domain"+ContentBoundaryClose) {
		t.Errorf("heading name not wrapped; got:\n%s", marked)
	}
	if !strings.Contains(marked, ContentBoundaryOpen+"More information"+ContentBoundaryClose) {
		t.Errorf("link name not wrapped; got:\n%s", marked)
	}
	if !strings.Contains(marked, ContentBoundaryOpen+"Submit"+ContentBoundaryClose) {
		t.Errorf("button name not wrapped; got:\n%s", marked)
	}

	// Structural tokens (@ref, role tags) must NOT be inside the markers.
	if strings.Contains(marked, ContentBoundaryOpen+"@1") {
		t.Errorf("ref @1 must not be inside boundary markers; got:\n%s", marked)
	}
	if strings.Contains(marked, ContentBoundaryOpen+"[") {
		t.Errorf("role tag must not be inside boundary markers; got:\n%s", marked)
	}
}

// TestContentBoundaryAgentProfile verifies the agent profile also applies markers.
func TestContentBoundaryAgentProfile(t *testing.T) {
	result := sharedBoundaryResult()

	p := ProfileAgent("text")
	p.ContentBoundary = true
	marked := FormatTextProfile(result, p)

	if !strings.Contains(marked, ContentBoundaryOpen) {
		t.Errorf("agent profile with ContentBoundary must contain markers; got:\n%s", marked)
	}
	// Markers must appear exactly once per label (not double-wrapped).
	count := strings.Count(marked, ContentBoundaryOpen)
	// Three nodes each with a name → three open markers.
	if count != 3 {
		t.Errorf("expected 3 boundary-open markers (one per named node), got %d; output:\n%s", count, marked)
	}
}

// TestContentBoundaryEmptyLabel ensures nodes without a name or value emit no
// markers (wrapContent must not wrap empty strings).
func TestContentBoundaryEmptyLabel(t *testing.T) {
	result := &ExtractionResult{
		Nodes: []ExtractedNode{
			{Role: "navigation"}, // no name, no value
			{Role: "form"},
		},
		Refs:  map[string]ExtractedNode{},
		Stats: ExtractionStats{},
	}

	p := ProfileHuman("text")
	p.ContentBoundary = true
	out := FormatTextProfile(result, p)

	if strings.Contains(out, ContentBoundaryOpen) {
		t.Errorf("empty-label nodes must not produce boundary markers; got:\n%s", out)
	}
}

func TestAxSubtreeIDsIncludesNestedDescendants(t *testing.T) {
	root := proto.AccessibilityAXNodeID("root")
	mid := proto.AccessibilityAXNodeID("mid")
	leaf := proto.AccessibilityAXNodeID("leaf")
	outside := proto.AccessibilityAXNodeID("outside")
	nodeMap := map[proto.AccessibilityAXNodeID]*proto.AccessibilityAXNode{
		root:    {NodeID: root, ChildIDs: []proto.AccessibilityAXNodeID{mid}, BackendDOMNodeID: 1},
		mid:     {NodeID: mid, ChildIDs: []proto.AccessibilityAXNodeID{leaf}, BackendDOMNodeID: 2},
		leaf:    {NodeID: leaf, BackendDOMNodeID: 3},
		outside: {NodeID: outside, BackendDOMNodeID: 4},
	}
	got := axSubtreeIDs(nodeMap, root)
	if !got[root] || !got[mid] || !got[leaf] {
		t.Fatalf("expected nested descendants in scope, got %#v", got)
	}
	if got[outside] {
		t.Fatal("did not expect unrelated node in selector scope")
	}
}
