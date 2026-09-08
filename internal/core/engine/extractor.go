package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

const defaultExtractTimeout = 30 * time.Second

// ExtractLevel controls how much of the accessibility tree is returned.
type ExtractLevel string

const (
	LevelSkeleton ExtractLevel = "skeleton"
	LevelContent  ExtractLevel = "content"
	LevelFull     ExtractLevel = "full"
)

// ExtractedNode represents a filtered accessibility node.
type ExtractedNode struct {
	Ref           string                 `json:"ref,omitempty"`
	Role          string                 `json:"role"`
	Name          string                 `json:"name,omitempty"`
	Value         string                 `json:"value,omitempty"`
	Level         int                    `json:"level,omitempty"`
	Href          string                 `json:"href,omitempty"`
	Type          string                 `json:"type,omitempty"`
	Checked       *bool                  `json:"checked,omitempty"`
	Disabled      bool                   `json:"disabled,omitempty"`
	Box           *ElementBox            `json:"box,omitempty"`
	BackendNodeID proto.DOMBackendNodeID `json:"-"`
	FrameID       proto.PageFrameID      `json:"-"`
	Children      []ExtractedNode        `json:"children,omitempty"`
}

// ElementBox is a viewport-relative element rectangle in CSS pixels.
// It is populated only when snapshot --boxes is explicitly requested.
type ElementBox struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// ExtractionResult holds the extraction output.
type ExtractionResult struct {
	Nodes []ExtractedNode          `json:"nodes"`
	Refs  map[string]ExtractedNode `json:"refs"`
	Stats ExtractionStats          `json:"stats"`
	// SSRPayloads is populated as a fallback when the accessibility tree
	// comes back empty or fails to load — e.g. an SSR/hydration-driven page
	// (Next.js, Nuxt, ...) still mid-hydration behind an unresolved bot
	// challenge. See ssrFallbackPayloads. Only populated when the caller
	// opts in (includeSSR) — these payloads (__NEXT_DATA__, __APOLLO_STATE__,
	// RSC chunks, ...) can carry tokens/PII on an authenticated page, so they
	// must not be exposed to the LLM by default.
	SSRPayloads []SSRPayload `json:"ssr_payloads,omitempty"`
}

// MarshalJSON serializes the extraction result for the wire: the JSONL `agent`
// protocol (what the TS/Python SDKs speak), `extract --json`, and the MCP
// surface. In memory every entry in Refs carries the interactive node's full
// Children subtree — ExtractionForRef relies on it to scope output to a single
// ref — but that subtree is already present in Nodes, so serializing it again
// for every interactive element duplicated a large slice of the payload the
// agent/SDK receives (the SDK's lightweight RefEntry type never modeled those
// children). Strip Children from Refs on the wire only; the in-memory struct is
// left untouched, so ExtractionForRef and other internal readers still see the
// subtree. Every interactive node already has its own top-level key in Refs, so
// dropping the nested subtree loses no ref.
func (r ExtractionResult) MarshalJSON() ([]byte, error) {
	type wire ExtractionResult // sheds MarshalJSON to avoid infinite recursion
	w := wire(r)
	if len(r.Refs) > 0 {
		trimmed := make(map[string]ExtractedNode, len(r.Refs))
		for ref, node := range r.Refs {
			node.Children = nil
			trimmed[ref] = node
		}
		w.Refs = trimmed
	}
	return json.Marshal(w)
}

// ExtractionStats provides extraction metrics.
type ExtractionStats struct {
	TotalNodes       int `json:"total_nodes"`
	FilteredNodes    int `json:"filtered_nodes"`
	InteractiveCount int `json:"interactive_count"`
}

// Roles considered interactive — get @ref assignments.
var interactiveRoles = map[string]bool{
	"button":           true,
	"link":             true,
	"textbox":          true,
	"checkbox":         true,
	"radio":            true,
	"combobox":         true,
	"menuitem":         true,
	"tab":              true,
	"switch":           true,
	"slider":           true,
	"spinbutton":       true,
	"option":           true,
	"searchbox":        true,
	"listbox":          true,
	"menuitemcheckbox": true,
	"menuitemradio":    true,
	"treeitem":         true,
}

// Skeleton-level roles.
var skeletonRoles = map[string]bool{
	"heading":       true,
	"button":        true,
	"link":          true,
	"textbox":       true,
	"checkbox":      true,
	"radio":         true,
	"combobox":      true,
	"menuitem":      true,
	"tab":           true,
	"switch":        true,
	"slider":        true,
	"spinbutton":    true,
	"option":        true,
	"searchbox":     true,
	"listbox":       true,
	"navigation":    true,
	"form":          true,
	"search":        true,
	"banner":        true,
	"main":          true,
	"complementary": true,
	"contentinfo":   true,
}

// Content-level additions on top of skeleton.
var contentRoles = map[string]bool{
	"StaticText":   true,
	"text":         true,
	"paragraph":    true,
	"listitem":     true,
	"img":          true,
	"image":        true,
	"table":        true,
	"rowheader":    true,
	"columnheader": true,
}

// Extract retrieves the accessibility tree from the page and filters it.
// The CDP call uses its own 30 s timeout so a slow navigation doesn't eat
// into the extraction budget. includeSSR controls whether the SSR-fallback
// payloads (see ExtractionResult.SSRPayloads) are returned; pass false unless
// the caller explicitly wants them exposed.
func Extract(page *rod.Page, level ExtractLevel, selector string, includeSSR bool) (*ExtractionResult, error) {
	return ExtractWithTimeout(page, level, selector, defaultExtractTimeout, includeSSR)
}

// ExtractWithTimeout is like Extract but accepts an explicit timeout for the
// CDP AccessibilityGetFullAXTree call. Use 0 to inherit the page's context
// deadline (previous behavior).
func ExtractWithTimeout(page *rod.Page, level ExtractLevel, selector string, timeout time.Duration, includeSSR bool) (*ExtractionResult, error) {
	if err := ValidateExtractLevel(level); err != nil {
		return nil, err
	}

	// Scope the CDP call to its own timeout so a slow page load doesn't
	// starve the extraction.
	p := page
	if timeout > 0 {
		p = page.Timeout(timeout)
	}

	// Get the full accessibility tree via CDP.
	_ = proto.AccessibilityEnable{}.Call(p)
	result, err := proto.AccessibilityGetFullAXTree{}.Call(p)
	if err != nil {
		// The AX tree read can fail outright on a page stuck mid-hydration
		// behind an unresolved bot challenge (the Accessibility domain never
		// settles). Rather than surface an opaque timeout with zero data,
		// fall back to scanning the page's raw HTML for SSR data islands
		// (__NEXT_DATA__, Nuxt, JSON-LD, ...) — the same parser fastfetch uses.
		// Only when the caller opted in: these payloads can carry
		// tokens/PII on an authenticated page.
		if includeSSR {
			if payloads := ssrFallbackPayloads(p); len(payloads) > 0 {
				return &ExtractionResult{Refs: map[string]ExtractedNode{}, SSRPayloads: payloads}, nil
			}
		}
		return nil, fmt.Errorf("get accessibility tree: %w", err)
	}

	// Build a lookup map by node ID and find the root(s).
	nodeMap := make(map[proto.AccessibilityAXNodeID]*proto.AccessibilityAXNode, len(result.Nodes))
	for _, n := range result.Nodes {
		nodeMap[n.NodeID] = n
	}

	// If selector is provided, scope to that subtree via DOM + AX query.
	var scopeNodeIDs map[proto.AccessibilityAXNodeID]bool
	if selector != "" {
		scopeNodeIDs, err = resolveScope(page, selector, nodeMap)
		if err != nil {
			return nil, fmt.Errorf("resolve selector %q: %w", selector, err)
		}
	}

	// Build tree from flat nodes.
	refCounter := 0
	stats := ExtractionStats{TotalNodes: len(result.Nodes)}
	refs := make(map[string]ExtractedNode)

	// Find root nodes (no parent or parent not in map).
	var rootIDs []proto.AccessibilityAXNodeID
	for _, n := range result.Nodes {
		if n.ParentID == "" {
			rootIDs = append(rootIDs, n.NodeID)
		}
	}

	budget := newEnrichmentBudget()
	var extractedNodes []ExtractedNode
	for _, rootID := range rootIDs {
		children := buildTree(page, nodeMap, rootID, level, scopeNodeIDs, &refCounter, refs, &stats, budget)
		extractedNodes = append(extractedNodes, children...)
	}

	var ssrPayloads []SSRPayload
	if len(extractedNodes) == 0 && includeSSR {
		// The a11y tree came back structurally empty — typical of an
		// SSR/hydration-driven page (Next.js, Nuxt, ...) whose real content
		// lives in a data island rather than in rendered, named DOM nodes.
		// Fall back to the raw HTML rather than reporting nothing. Only when
		// the caller opted in — see includeSSR doc on Extract.
		ssrPayloads = ssrFallbackPayloads(p)
	}

	if selector == "" {
		appendSameOriginIframes(page, level, &extractedNodes, refs, &stats, &refCounter)
	}

	return &ExtractionResult{
		Nodes:       extractedNodes,
		Refs:        refs,
		Stats:       stats,
		SSRPayloads: ssrPayloads,
	}, nil
}

// ssrFallbackPayloads scans the page's current HTML for SSR data islands.
// Best-effort: always returns nil (never an error) so callers can use it
// unconditionally as a fallback when the accessibility tree is empty or
// fails to load.
func ssrFallbackPayloads(page *rod.Page) []SSRPayload {
	if page == nil {
		return nil
	}
	html, err := page.HTML()
	if err != nil {
		return nil
	}
	return ExtractSSRPayloads(html)
}

func appendSameOriginIframes(page *rod.Page, level ExtractLevel, nodes *[]ExtractedNode, refs map[string]ExtractedNode, stats *ExtractionStats, refCounter *int) {
	if page == nil || selectorScoped(nodes) {
		return
	}

	appendSameOriginIframesAt(page, level, nodes, refs, stats, refCounter, 0)
}

const maxIframeDepth = 3

func appendSameOriginIframesAt(page *rod.Page, level ExtractLevel, nodes *[]ExtractedNode, refs map[string]ExtractedNode, stats *ExtractionStats, refCounter *int, depth int) {
	if page == nil || depth >= maxIframeDepth {
		return
	}
	iframes, err := page.Elements("iframe")
	if err != nil || len(iframes) == 0 {
		return
	}
	budget := newEnrichmentBudget()
	for _, iframe := range iframes {
		frame, err := iframe.Frame()
		if err != nil || frame == nil {
			continue
		}
		_ = proto.AccessibilityEnable{}.Call(frame)
		result, err := proto.AccessibilityGetFullAXTree{FrameID: frame.FrameID}.Call(frame)
		if err != nil || result == nil {
			continue
		}
		StartFileChooserIntercept(frame)
		nodeMap := make(map[proto.AccessibilityAXNodeID]*proto.AccessibilityAXNode, len(result.Nodes))
		for _, n := range result.Nodes {
			nodeMap[n.NodeID] = n
		}
		stats.TotalNodes += len(result.Nodes)
		var frameNodes []ExtractedNode
		for _, n := range result.Nodes {
			if n.ParentID == "" {
				frameNodes = append(frameNodes, buildTree(frame, nodeMap, n.NodeID, level, nil, refCounter, refs, stats, budget)...)
			}
		}
		stampFrame(frameNodes, frame.FrameID)
		syncFrameRefs(frameNodes, refs)
		appendSameOriginIframesAt(frame, level, &frameNodes, refs, stats, refCounter, depth+1)
		if len(frameNodes) == 0 {
			continue
		}
		*nodes = append(*nodes, ExtractedNode{Role: "iframe", Name: iframeName(iframe), Children: frameNodes})
	}
}

func selectorScoped(nodes *[]ExtractedNode) bool {
	return nodes == nil
}

func iframeName(el *rod.Element) string {
	if el == nil {
		return ""
	}
	if title, err := el.Attribute("title"); err == nil && title != nil && *title != "" {
		return *title
	}
	if name, err := el.Attribute("name"); err == nil && name != nil && *name != "" {
		return *name
	}
	return ""
}

func stampFrame(nodes []ExtractedNode, frame proto.PageFrameID) {
	for i := range nodes {
		nodes[i].FrameID = frame
		stampFrame(nodes[i].Children, frame)
	}
}

func syncFrameRefs(nodes []ExtractedNode, refs map[string]ExtractedNode) {
	for _, n := range nodes {
		if n.Ref != "" {
			if stored, ok := refs[n.Ref]; ok {
				stored.FrameID = n.FrameID
				refs[n.Ref] = stored
			}
		}
		syncFrameRefs(n.Children, refs)
	}
}

// resolveScope finds all AX node IDs that are descendants of the given CSS selector.
func resolveScope(page *rod.Page, selector string, nodeMap map[proto.AccessibilityAXNodeID]*proto.AccessibilityAXNode) (map[proto.AccessibilityAXNodeID]bool, error) {
	// Get the DOM node for the selector, then query AX tree for its subtree.
	el, err := page.Element(selector)
	if err != nil {
		return nil, fmt.Errorf("element %q not found: %w", selector, err)
	}

	// Get the backend node ID.
	desc, err := el.Describe(0, false)
	if err != nil {
		return nil, fmt.Errorf("describe element: %w", err)
	}

	rootID := axNodeIDForBackend(nodeMap, desc.BackendNodeID)
	if rootID == "" {
		return nil, fmt.Errorf("no accessibility node for selector %q", selector)
	}
	return axSubtreeIDs(nodeMap, rootID), nil
}

func axNodeIDForBackend(nodeMap map[proto.AccessibilityAXNodeID]*proto.AccessibilityAXNode, backend proto.DOMBackendNodeID) proto.AccessibilityAXNodeID {
	var fallback proto.AccessibilityAXNodeID
	for id, n := range nodeMap {
		if n == nil || n.BackendDOMNodeID != backend {
			continue
		}
		if len(n.ChildIDs) > 0 {
			return id
		}
		if fallback == "" {
			fallback = id
		}
	}
	return fallback
}

func axSubtreeIDs(nodeMap map[proto.AccessibilityAXNodeID]*proto.AccessibilityAXNode, root proto.AccessibilityAXNodeID) map[proto.AccessibilityAXNodeID]bool {
	ids := map[proto.AccessibilityAXNodeID]bool{}
	var walk func(proto.AccessibilityAXNodeID)
	walk = func(id proto.AccessibilityAXNodeID) {
		if id == "" || ids[id] {
			return
		}
		ids[id] = true
		n := nodeMap[id]
		if n == nil {
			return
		}
		for _, child := range n.ChildIDs {
			walk(child)
		}
	}
	walk(root)
	return ids
}

// buildTree recursively builds ExtractedNode tree from the flat AX node map.
func buildTree(
	page *rod.Page,
	nodeMap map[proto.AccessibilityAXNodeID]*proto.AccessibilityAXNode,
	nodeID proto.AccessibilityAXNodeID,
	level ExtractLevel,
	scope map[proto.AccessibilityAXNodeID]bool,
	refCounter *int,
	refs map[string]ExtractedNode,
	stats *ExtractionStats,
	budget *enrichmentBudget,
) []ExtractedNode {
	axNode, ok := nodeMap[nodeID]
	if !ok {
		return nil
	}

	// If scoped, skip nodes not in scope.
	if scope != nil && !scope[nodeID] {
		return nil
	}

	// Skip ignored nodes but still recurse into children.
	if axNode.Ignored {
		var result []ExtractedNode
		for _, childID := range axNode.ChildIDs {
			result = append(result, buildTree(page, nodeMap, childID, level, scope, refCounter, refs, stats, budget)...)
		}
		return result
	}

	role := axValueStr(axNode.Role)
	name := axValueStr(axNode.Name)

	// Apply level filter.
	if !shouldInclude(role, name, level) {
		// Still recurse — children might be relevant.
		var result []ExtractedNode
		for _, childID := range axNode.ChildIDs {
			result = append(result, buildTree(page, nodeMap, childID, level, scope, refCounter, refs, stats, budget)...)
		}
		return result
	}

	node := ExtractedNode{
		Role:          role,
		Name:          name,
		BackendNodeID: axNode.BackendDOMNodeID,
	}

	// Extract value.
	if axNode.Value != nil {
		node.Value = axValueStr(axNode.Value)
	}

	// Extract properties.
	for _, prop := range axNode.Properties {
		switch prop.Name {
		case proto.AccessibilityAXPropertyNameLevel:
			if prop.Value != nil {
				if v, ok := prop.Value.Value.Val().(float64); ok {
					node.Level = int(v)
				}
			}
		case proto.AccessibilityAXPropertyNameChecked:
			if prop.Value != nil {
				val := axValueStr(prop.Value) == "true"
				node.Checked = &val
			}
		case proto.AccessibilityAXPropertyNameDisabled:
			if prop.Value != nil {
				node.Disabled = axValueStr(prop.Value) == "true"
			}
		case proto.AccessibilityAXPropertyNameURL:
			if prop.Value != nil {
				node.Href = axValueStr(prop.Value)
			}
		}
	}

	// DOM fallback: obfuscated apps (LinkedIn, Notion, X) often expose
	// interactive widgets without an AX name. Probe the DOM for a
	// human-readable hint when AX gives us nothing usable.
	if node.Name == "" && node.Value == "" && page != nil &&
		domFallbackRoles[role] && axNode.BackendDOMNodeID != 0 {
		if hint, ok := enrichFromDOM(page, axNode.BackendDOMNodeID, budget); ok {
			node.Name = hint
		}
	}

	// Assign ref to interactive elements.
	if interactiveRoles[role] && axNode.BackendDOMNodeID != 0 {
		*refCounter++
		node.Ref = fmt.Sprintf("@%d", *refCounter)
		stats.InteractiveCount++
	}

	// Recurse children.
	for _, childID := range axNode.ChildIDs {
		node.Children = append(node.Children, buildTree(page, nodeMap, childID, level, scope, refCounter, refs, stats, budget)...)
	}

	stats.FilteredNodes++

	// Store in refs map if interactive.
	if node.Ref != "" {
		refs[node.Ref] = node
	}

	return []ExtractedNode{node}
}

// shouldInclude checks if a node should be included based on the extraction level.
func shouldInclude(role, name string, level ExtractLevel) bool {
	// Skip generic/none roles unless they have meaningful content.
	if role == "" || role == "none" || role == "generic" {
		return false
	}

	switch level {
	case LevelSkeleton:
		return skeletonRoles[role]
	case LevelContent:
		return skeletonRoles[role] || contentRoles[role]
	case LevelFull:
		// Include everything with a non-empty name or a known role.
		return name != "" || skeletonRoles[role] || contentRoles[role]
	}
	return false
}

// ValidateExtractLevel ensures the extraction level is supported.
func ValidateExtractLevel(level ExtractLevel) error {
	switch level {
	case LevelSkeleton, LevelContent, LevelFull:
		return nil
	default:
		return fmt.Errorf("invalid level %q: use skeleton, content, or full", level)
	}
}

// axValueStr extracts the string representation from an AXValue.
func axValueStr(v *proto.AccessibilityAXValue) string {
	if v == nil {
		return ""
	}
	raw := v.Value.Val()
	if raw == nil {
		return ""
	}
	switch val := raw.(type) {
	case string:
		return val
	default:
		return fmt.Sprintf("%v", val)
	}
}

// ContentBoundaryOpen and ContentBoundaryClose are the sentinel markers used to
// fence untrusted page-derived text when ContentBoundary is enabled.
// They use Unicode brackets (U+27E6 / U+27E7) that are unlikely to appear in
// real page content and are visually distinct from tool output.
//
//	⟦page⟧ <untrusted text here> ⟦/page⟧
const (
	ContentBoundaryOpen  = "⟦page⟧"
	ContentBoundaryClose = "⟦/page⟧"
)

// wrapContent wraps s with boundary markers when enabled. Returns s unchanged
// when s is empty or ContentBoundary is false.
func wrapContent(s string, enabled bool) string {
	if !enabled || s == "" {
		return s
	}
	return ContentBoundaryOpen + s + ContentBoundaryClose
}

// FormatText renders the extraction result as compact text (human profile).
func FormatText(result *ExtractionResult) string {
	return FormatTextProfile(result, ProfileHuman("text"))
}

// FormatTextProfile renders the extraction result using the given profile.
// The agent profile uses one-letter role tags, truncates long labels and
// shortens hrefs (see TruncateURL).
func FormatTextProfile(result *ExtractionResult, p RenderProfile) string {
	var buf strings.Builder
	state := renderState{lastKey: "", repeat: 0}
	for _, node := range result.Nodes {
		formatNode(&buf, node, 0, p, &state)
	}
	return strings.TrimRight(buf.String(), "\n")
}

// FormatPlaywrightSnapshot renders an accessibility tree in the documented
// playwright-cli snapshot style:
//
//   - heading "Title" [level=1]
//   - textbox "Search" [ref=e2]
//   - listitem:
//   - text: "Item"
func FormatPlaywrightSnapshot(result *ExtractionResult) string {
	if result == nil {
		return ""
	}
	var buf strings.Builder
	for _, node := range result.Nodes {
		formatPlaywrightNode(&buf, node, 0)
	}
	return strings.TrimRight(buf.String(), "\n")
}

func formatPlaywrightNode(buf *strings.Builder, node ExtractedNode, depth int) {
	indent := strings.Repeat("  ", depth)
	label := node.Name
	if label == "" {
		label = node.Value
	}
	role := playwrightRole(node.Role)

	fmt.Fprintf(buf, "%s- %s", indent, role)
	if label != "" {
		if role == "text" {
			fmt.Fprintf(buf, ": %q", label)
		} else {
			fmt.Fprintf(buf, " %q", label)
		}
	}

	var attrs []string
	if node.Ref != "" {
		attrs = append(attrs, "ref="+PlaywrightRef(node.Ref))
	}
	if node.Role == "heading" && node.Level > 0 {
		attrs = append(attrs, fmt.Sprintf("level=%d", node.Level))
	}
	if node.Checked != nil {
		if *node.Checked {
			attrs = append(attrs, "checked")
		} else {
			attrs = append(attrs, "unchecked")
		}
	}
	if node.Disabled {
		attrs = append(attrs, "disabled")
	}
	if node.Box != nil {
		attrs = append(attrs, fmt.Sprintf("box=%d,%d,%d,%d", node.Box.X, node.Box.Y, node.Box.Width, node.Box.Height))
	}
	if len(attrs) > 0 {
		fmt.Fprintf(buf, " [%s]", strings.Join(attrs, " "))
	}
	if len(node.Children) > 0 && label == "" && len(attrs) == 0 {
		buf.WriteString(":")
	}
	buf.WriteString("\n")

	for _, child := range node.Children {
		formatPlaywrightNode(buf, child, depth+1)
	}
}

func playwrightRole(role string) string {
	switch role {
	case "StaticText":
		return "text"
	default:
		return role
	}
}

// PlaywrightRef maps ghostchrome's internal @N refs to playwright-cli's eN refs.
func PlaywrightRef(ref string) string {
	return "e" + strings.TrimPrefix(ref, "@")
}

// InternalRef maps playwright-cli eN refs back to ghostchrome's internal @N refs.
func InternalRef(ref string) string {
	if strings.HasPrefix(ref, "e") && len(ref) > 1 {
		allDigits := true
		for _, r := range ref[1:] {
			if r < '0' || r > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return "@" + ref[1:]
		}
	}
	return ref
}

// LimitExtractionDepth returns a copy of result whose node children are cut
// after maxDepth. A negative maxDepth returns result unchanged; 0 keeps only
// root nodes.
func LimitExtractionDepth(result *ExtractionResult, maxDepth int) *ExtractionResult {
	if result == nil || maxDepth < 0 {
		return result
	}
	limited := &ExtractionResult{
		Nodes: make([]ExtractedNode, 0, len(result.Nodes)),
		Refs:  map[string]ExtractedNode{},
	}
	for _, node := range result.Nodes {
		limited.Nodes = append(limited.Nodes, limitNodeDepth(node, 0, maxDepth, limited.Refs, &limited.Stats))
	}
	limited.Stats.TotalNodes = limited.Stats.FilteredNodes
	return limited
}

func limitNodeDepth(node ExtractedNode, depth, maxDepth int, refs map[string]ExtractedNode, stats *ExtractionStats) ExtractedNode {
	copyNode := node
	copyNode.Children = nil
	stats.FilteredNodes++
	if depth >= maxDepth {
		if copyNode.Ref != "" {
			stats.InteractiveCount++
			refs[copyNode.Ref] = copyNode
		}
		return copyNode
	}
	for _, child := range node.Children {
		copyNode.Children = append(copyNode.Children, limitNodeDepth(child, depth+1, maxDepth, refs, stats))
	}
	if copyNode.Ref != "" {
		stats.InteractiveCount++
		refs[copyNode.Ref] = copyNode
	}
	return copyNode
}

// ExtractionForRef returns a single-root extraction result for ref.
func ExtractionForRef(result *ExtractionResult, ref string) (*ExtractionResult, bool) {
	if result == nil {
		return nil, false
	}
	node, ok := result.Refs[ref]
	if !ok {
		return nil, false
	}
	scoped := &ExtractionResult{
		Nodes: []ExtractedNode{node},
		Refs:  map[string]ExtractedNode{},
	}
	collectNodeStats(node, scoped.Refs, &scoped.Stats)
	scoped.Stats.TotalNodes = scoped.Stats.FilteredNodes
	return scoped, true
}

func collectNodeStats(node ExtractedNode, refs map[string]ExtractedNode, stats *ExtractionStats) {
	stats.FilteredNodes++
	if node.Ref != "" {
		stats.InteractiveCount++
		refs[node.Ref] = node
	}
	for _, child := range node.Children {
		collectNodeStats(child, refs, stats)
	}
}

// renderState tracks cross-node state used by the agent renderer to dedupe
// adjacent siblings with identical role + label (e.g. 30 "Read more" links).
// The key combines role and label so that a heading and its inner StaticText
// with the same text (very common) are NOT merged.
type renderState struct {
	lastKey string
	repeat  int
}

// formatNode writes a single node and its children to the buffer.
func formatNode(buf *strings.Builder, node ExtractedNode, depth int, p RenderProfile, state *renderState) {
	indent := strings.Repeat("  ", depth)
	if p.Agent {
		// Single-space indent in agent mode — still conveys hierarchy but saves
		// bytes on deep trees.
		indent = strings.Repeat(" ", depth)
	}

	if p.Agent {
		writeNodeAgent(buf, indent, node, p, state)
	} else {
		writeNodeHuman(buf, indent, node, p)
	}

	for _, child := range node.Children {
		formatNode(buf, child, depth+1, p, state)
	}
}

// writeNodeHuman keeps the legacy "[tag @ref attr=val] label" format.
func writeNodeHuman(buf *strings.Builder, indent string, node ExtractedNode, p RenderProfile) {
	tag := roleToTag(node.Role)
	parts := []string{tag}

	if node.Ref != "" {
		parts = append(parts, node.Ref)
	}
	if node.Href != "" {
		parts = append(parts, "href="+node.Href)
	}
	if node.Type != "" {
		parts = append(parts, "type="+node.Type)
	}
	if node.Checked != nil {
		if *node.Checked {
			parts = append(parts, "checked")
		} else {
			parts = append(parts, "unchecked")
		}
	}
	if node.Disabled {
		parts = append(parts, "disabled")
	}

	if node.Level > 0 && node.Role == "heading" {
		parts[0] = fmt.Sprintf("h%d", node.Level)
	}

	label := strings.Join(parts, " ")
	switch {
	case node.Name != "":
		fmt.Fprintf(buf, "%s[%s] %s\n", indent, label, wrapContent(node.Name, p.ContentBoundary))
	case node.Value != "":
		fmt.Fprintf(buf, "%s[%s] %s\n", indent, label, wrapContent(node.Value, p.ContentBoundary))
	default:
		fmt.Fprintf(buf, "%s[%s]\n", indent, label)
	}
}

// writeNodeAgent emits a compact representation:
//
//	@3 b Save                         (interactive, no href)
//	@1 a>iana.org/x Learn more        (link with truncated href)
//	h1 Example Domain                 (non-interactive, heading with level)
//	p Lorem ipsum…                    (non-interactive, label truncated)
//
// It also strips common navigation prefixes ("Aller à", "Go to", "Read more
// about") from the label and replaces runs of identical adjacent labels with a
// single `(xN)` marker on the last occurrence.
func writeNodeAgent(buf *strings.Builder, indent string, node ExtractedNode, p RenderProfile, state *renderState) {
	tag := agentTag(node)

	var head strings.Builder
	head.WriteString(indent)
	if node.Ref != "" {
		head.WriteString(node.Ref)
		head.WriteString(" ")
	}
	head.WriteString(tag)

	if node.Href != "" {
		head.WriteString(">")
		head.WriteString(TruncateURL(node.Href, 40))
	}
	if node.Type != "" {
		head.WriteString(" t=")
		head.WriteString(node.Type)
	}
	if node.Checked != nil {
		if *node.Checked {
			head.WriteString(" ✓")
		} else {
			head.WriteString(" ✗")
		}
	}
	if node.Disabled {
		head.WriteString(" off")
	}

	label := node.Name
	if label == "" {
		label = node.Value
	}
	label = stripNavNoise(label)
	// Drop redundant label when it duplicates the href.
	if label != "" && node.Href != "" && label == node.Href {
		label = ""
	}
	label = truncateLabel(label, p.MaxLabelLen)

	// Dedupe adjacent identical role+label (common in listings).
	dedupeKey := ""
	if label != "" {
		dedupeKey = tag + "|" + label
	}
	if dedupeKey != "" && dedupeKey == state.lastKey {
		state.repeat++
		return
	}
	if state.repeat > 0 {
		trimTrailingNewline(buf)
		fmt.Fprintf(buf, " (×%d)\n", state.repeat+1)
		state.repeat = 0
	}
	state.lastKey = dedupeKey

	if label != "" {
		fmt.Fprintf(buf, "%s %s\n", head.String(), wrapContent(label, p.ContentBoundary))
	} else {
		fmt.Fprintf(buf, "%s\n", head.String())
	}
}

// trimTrailingNewline removes the final '\n' from buf if present.
func trimTrailingNewline(buf *strings.Builder) {
	s := buf.String()
	if strings.HasSuffix(s, "\n") {
		buf.Reset()
		buf.WriteString(s[:len(s)-1])
	}
}

// navNoisePrefixes strips common accessibility-verbose prefixes that add no
// signal for LLM agents.
var navNoisePrefixes = []string{
	"Aller à ",
	"Aller au ",
	"Go to ",
	"Lire la suite de ",
	"Lire la suite sur ",
	"Read more about ",
	"Read more on ",
	"Voir ",
	"Link to ",
	"Lien vers ",
}

func stripNavNoise(label string) string {
	for _, prefix := range navNoisePrefixes {
		if strings.HasPrefix(label, prefix) {
			return strings.TrimSpace(label[len(prefix):])
		}
	}
	return label
}

// agentTag returns the 1-2 character role tag used in agent mode.
func agentTag(node ExtractedNode) string {
	if node.Role == "heading" && node.Level > 0 {
		return fmt.Sprintf("h%d", node.Level)
	}
	if abbrev, ok := roleAbbrev[node.Role]; ok {
		return abbrev
	}
	return node.Role
}

// roleAbbrev maps accessibility roles to short tags used in agent mode.
var roleAbbrev = map[string]string{
	"button":        "b",
	"link":          "a",
	"textbox":       "t",
	"checkbox":      "c",
	"radio":         "r",
	"combobox":      "s",
	"menuitem":      "m",
	"tab":           "x",
	"heading":       "h",
	"navigation":    "nav",
	"form":          "form",
	"search":        "search",
	"banner":        "hdr",
	"main":          "main",
	"complementary": "aside",
	"contentinfo":   "ftr",
	"paragraph":     "p",
	"listitem":      "li",
	"img":           "img",
	"image":         "img",
	"StaticText":    "txt",
	"text":          "txt",
	"table":         "tbl",
	"rowheader":     "rh",
	"columnheader":  "ch",
}

// truncateLabel shortens a label to maxLen characters, appending an ellipsis
// if truncated. A maxLen of 0 disables truncation.
func truncateLabel(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return "…"
	}
	return s[:maxLen-1] + "…"
}

// roleToTag maps accessibility roles to compact text tags.
func roleToTag(role string) string {
	switch role {
	case "button":
		return "btn"
	case "link":
		return "link"
	case "textbox":
		return "input"
	case "checkbox":
		return "checkbox"
	case "radio":
		return "radio"
	case "combobox":
		return "select"
	case "menuitem":
		return "menuitem"
	case "tab":
		return "tab"
	case "heading":
		return "h"
	case "navigation":
		return "nav"
	case "form":
		return "form"
	case "search":
		return "search"
	case "banner":
		return "banner"
	case "main":
		return "main"
	case "complementary":
		return "aside"
	case "contentinfo":
		return "footer"
	case "paragraph":
		return "p"
	case "listitem":
		return "li"
	case "img", "image":
		return "img"
	case "StaticText", "text":
		return "text"
	case "table":
		return "table"
	case "rowheader":
		return "rowheader"
	case "columnheader":
		return "colheader"
	default:
		return role
	}
}
