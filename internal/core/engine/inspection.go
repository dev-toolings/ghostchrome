package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

const passiveInspectionTimeout = 2 * time.Second

// InspectionInfo bounds target metadata reads too: Rod's Page.Info delegates
// to the browser context and otherwise ignores a timeout set on the page.
func InspectionInfo(page *rod.Page) (*proto.TargetTargetInfo, error) {
	ctx, cancel := context.WithTimeout(page.GetContext(), passiveInspectionTimeout)
	defer cancel()
	result, err := (proto.TargetGetTargetInfo{TargetID: page.TargetID}).Call(page.Browser().Context(ctx))
	if err != nil {
		return nil, err
	}
	if result.TargetInfo == nil {
		return nil, fmt.Errorf("inspect target: missing target metadata")
	}
	return result.TargetInfo, nil
}

// InspectionHTML reads document markup through CDP without running page scripts
// or Rod's JavaScript selector helpers. Passive diagnostics have a separate,
// bounded budget and must not stall an otherwise completed navigation.
func InspectionHTML(page *rod.Page) (string, error) {
	ctx, cancel := context.WithTimeout(page.GetContext(), passiveInspectionTimeout)
	defer cancel()
	page = page.Context(ctx)
	depth := 0
	document, err := (proto.DOMGetDocument{Depth: &depth}).Call(page)
	if err != nil {
		return "", fmt.Errorf("inspect document: %w", err)
	}
	if document.Root == nil {
		return "", fmt.Errorf("inspect document: missing document root")
	}
	markup, err := (proto.DOMGetOuterHTML{NodeID: document.Root.NodeID}).Call(page)
	if err != nil {
		return "", fmt.Errorf("inspect document markup: %w", err)
	}
	return markup.OuterHTML, nil
}
