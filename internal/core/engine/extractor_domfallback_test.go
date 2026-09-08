package engine

import (
	"testing"

	"github.com/go-rod/rod/lib/proto"
)

func TestPickAttrNamePriority(t *testing.T) {
	tests := []struct {
		name  string
		attrs []string
		want  string
	}{
		{
			name:  "aria-label wins over title",
			attrs: []string{"title", "Click me", "aria-label", "Send message"},
			want:  "Send message",
		},
		{
			name:  "title fallback when no aria-label",
			attrs: []string{"class", "btn", "title", "Submit"},
			want:  "Submit",
		},
		{
			name:  "data-testid surfaces",
			attrs: []string{"data-testid", "like-button", "tabindex", "0"},
			want:  "like-button",
		},
		{
			name:  "placeholder for inputs",
			attrs: []string{"type", "text", "placeholder", "Search Notion"},
			want:  "Search Notion",
		},
		{
			name:  "id last resort",
			attrs: []string{"class", "x", "id", "send-btn"},
			want:  "send-btn",
		},
		{
			name:  "skips empty values",
			attrs: []string{"aria-label", "", "title", "Real"},
			want:  "Real",
		},
		{
			name:  "no candidates returns empty",
			attrs: []string{"class", "abc", "tabindex", "0"},
			want:  "",
		},
		{
			name:  "empty slice",
			attrs: nil,
			want:  "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickAttrName(tc.attrs); got != tc.want {
				t.Errorf("pickAttrName = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFirstChildText(t *testing.T) {
	node := &proto.DOMNode{
		Children: []*proto.DOMNode{
			{NodeType: 1, NodeName: "SPAN"},
			{NodeType: 3, NodeValue: "  Reply  "},
			{NodeType: 3, NodeValue: "ignored"},
		},
	}
	if got := firstChildText(node); got != "Reply" {
		t.Errorf("firstChildText = %q, want %q", got, "Reply")
	}

	empty := &proto.DOMNode{Children: []*proto.DOMNode{
		{NodeType: 1, NodeName: "DIV"},
	}}
	if got := firstChildText(empty); got != "" {
		t.Errorf("firstChildText(no text) = %q, want empty", got)
	}
}

func TestTagClassHint(t *testing.T) {
	node := &proto.DOMNode{
		LocalName:  "div",
		Attributes: []string{"class", "btn primary large fancy"},
	}
	got := tagClassHint(node, "<div class=\"btn primary large fancy\"></div>")
	if got != "<div.btn.primary>" {
		t.Errorf("tagClassHint = %q, want <div.btn.primary>", got)
	}

	noClass := &proto.DOMNode{LocalName: "button"}
	if got := tagClassHint(noClass, "<button></button>"); got != "<button>" {
		t.Errorf("tagClassHint(no class) = %q, want <button>", got)
	}

	noTag := &proto.DOMNode{}
	if got := tagClassHint(noTag, ""); got != "" {
		t.Errorf("tagClassHint(no tag) = %q, want empty", got)
	}
}

func TestEnrichmentBudget(t *testing.T) {
	b := newEnrichmentBudget()
	for i := 0; i < maxDOMEnrichments; i++ {
		if !b.take() {
			t.Fatalf("budget exhausted early at %d", i)
		}
	}
	if b.take() {
		t.Errorf("budget should be exhausted after %d takes", maxDOMEnrichments)
	}
}
