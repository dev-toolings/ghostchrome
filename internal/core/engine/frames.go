package engine

import (
	"fmt"
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

const maxIframeDepth = 3

// frameElements walks one document, including closed shadow roots. CSS queries
// cannot see these hosts' contents. Stop at frame owners: child documents must
// be inspected through their own frame/session, never through the parent DOM.
func frameElements(page *rod.Page, selector string) ([]*rod.Element, error) {
	var root *proto.DOMNode
	if selector != "" || page.IsIframe() {
		if selector == "" {
			selector = "html"
		}
		el, err := page.Element(selector)
		if err != nil {
			return nil, err
		}
		root, err = el.Describe(-1, true)
		if err != nil {
			return nil, err
		}
	} else {
		depth := -1
		doc, err := (proto.DOMGetDocument{Depth: &depth, Pierce: true}).Call(page)
		if err != nil {
			return nil, err
		}
		root = doc.Root
	}
	var elements []*rod.Element
	var walk func(*proto.DOMNode) error
	walk = func(node *proto.DOMNode) error {
		if node == nil {
			return nil
		}
		if strings.EqualFold(node.NodeName, "iframe") || strings.EqualFold(node.NodeName, "frame") {
			el, err := page.ElementFromNode(&proto.DOMNode{BackendNodeID: node.BackendNodeID})
			if err != nil {
				return err
			}
			elements = append(elements, el)
			return nil
		}
		for _, children := range [][]*proto.DOMNode{node.Children, node.ShadowRoots} {
			for _, child := range children {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	err := walk(root)
	return elements, err
}

// framePage preserves Rod's frame geometry for in-process frames. An OOPIF
// needs its own CDP target/session (Chromium uses the frame ID as target ID),
// as in Playwright's FrameSession routing. Do not disable site isolation.
func framePage(owner *rod.Element) (*rod.Page, error) {
	node, err := owner.Describe(1, true)
	if err != nil {
		return nil, err
	}
	if node.FrameID == "" {
		return nil, fmt.Errorf("frame has no document yet")
	}
	if node.ContentDocument != nil {
		return owner.Frame()
	}
	// Viewport emulation is only valid on top-level targets. Rod otherwise
	// applies its default device while attaching every new target.
	browser := owner.Page().Browser()
	// Clone before changing the default device, retaining the browser lifetime
	// for Rod's cached target. Only the returned handle gets the action deadline.
	page, err := browser.Context(browser.GetContext()).NoDefaultDevice().PageFromTarget(proto.TargetTargetID(node.FrameID))
	if err != nil {
		return nil, fmt.Errorf("attach frame: %w", err)
	}
	return page.Context(owner.GetContext()), nil
}
