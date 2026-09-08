package engine

import (
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// maxDOMEnrichments caps the number of DOM-fallback lookups per extraction to
// keep latency bounded on large obfuscated pages (LinkedIn, Notion, X).
const maxDOMEnrichments = 50

// domFallbackRoles are the interactive roles for which we attempt a DOM
// enrichment when the AX tree provides no usable name.
var domFallbackRoles = map[string]bool{
	"button":   true,
	"link":     true,
	"textbox":  true,
	"combobox": true,
	"checkbox": true,
	"tab":      true,
	"menuitem": true,
	"option":   true,
	"switch":   true,
	"radio":    true,
}

// enrichmentBudget tracks remaining DOM lookups for a single extraction pass.
type enrichmentBudget struct {
	remaining int
}

func newEnrichmentBudget() *enrichmentBudget {
	return &enrichmentBudget{remaining: maxDOMEnrichments}
}

func (b *enrichmentBudget) take() bool {
	if b == nil || b.remaining <= 0 {
		return false
	}
	b.remaining--
	return true
}

// candidateAttrs lists the DOM attributes (in priority order) we probe for a
// human-readable name when AX has none.
var candidateAttrs = []string{
	"aria-label",
	"title",
	"placeholder",
	"alt",
	"value",
	"data-test",
	"data-testid",
	"data-qa",
	"name",
	"id",
}

// enrichFromDOM tries to derive a human-readable name for an interactive node
// whose AX `name` is empty. It returns the candidate name (already prefixed
// with "~" to mark its DOM origin) and ok=true on success.
func enrichFromDOM(page *rod.Page, backendID proto.DOMBackendNodeID, budget *enrichmentBudget) (string, bool) {
	if backendID == 0 || !budget.take() {
		return "", false
	}

	desc, err := proto.DOMDescribeNode{
		BackendNodeID: backendID,
		Depth:         intPtr(1),
	}.Call(page)
	if err != nil || desc.Node == nil {
		return "", false
	}

	if name := pickAttrName(desc.Node.Attributes); name != "" {
		return "~" + truncateLabel(name, 60), true
	}

	if txt := firstChildText(desc.Node); txt != "" {
		return "~" + txt, true
	}

	// Last resort: small slice of outerHTML to derive a tag.classes hint.
	outer, err := proto.DOMGetOuterHTML{BackendNodeID: backendID}.Call(page)
	if err == nil && outer.OuterHTML != "" {
		if hint := tagClassHint(desc.Node, outer.OuterHTML); hint != "" {
			return "~" + hint, true
		}
	}
	return "", false
}

// pickAttrName walks the flat [k1,v1,k2,v2,...] attribute slice looking for
// the first attribute matching `candidateAttrs` (priority order).
func pickAttrName(attrs []string) string {
	if len(attrs) < 2 {
		return ""
	}
	lookup := make(map[string]string, len(attrs)/2)
	for i := 0; i+1 < len(attrs); i += 2 {
		lookup[strings.ToLower(attrs[i])] = attrs[i+1]
	}
	for _, k := range candidateAttrs {
		if v, ok := lookup[k]; ok {
			v = strings.TrimSpace(v)
			if v != "" {
				return v
			}
		}
	}
	return ""
}

// firstChildText returns the first non-empty text-node content found in the
// immediate children of node (depth=1), bounded to 60 chars.
func firstChildText(node *proto.DOMNode) string {
	for _, c := range node.Children {
		// nodeType 3 = TEXT_NODE
		if c == nil {
			continue
		}
		if c.NodeType == 3 {
			t := strings.TrimSpace(c.NodeValue)
			if t != "" {
				return truncateLabel(collapseWS(t), 60)
			}
		}
	}
	return ""
}

// tagClassHint builds a "<tag.classA.classB>" short hint from a DOM node and
// its outerHTML (used only to confirm class presence cheaply).
func tagClassHint(node *proto.DOMNode, _ string) string {
	tag := strings.ToLower(node.LocalName)
	if tag == "" {
		tag = strings.ToLower(node.NodeName)
	}
	if tag == "" {
		return ""
	}
	classes := ""
	for i := 0; i+1 < len(node.Attributes); i += 2 {
		if strings.EqualFold(node.Attributes[i], "class") {
			classes = node.Attributes[i+1]
			break
		}
	}
	if classes == "" {
		return "<" + tag + ">"
	}
	parts := strings.Fields(classes)
	if len(parts) > 2 {
		parts = parts[:2]
	}
	return "<" + tag + "." + strings.Join(parts, ".") + ">"
}

func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
