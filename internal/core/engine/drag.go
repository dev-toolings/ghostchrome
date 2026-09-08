package engine

import (
	"fmt"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// DragDrop drags from one element to another using mouse events.
func DragDrop(page *rod.Page, fromRef, toRef string, snapshot *PageSnapshot, steps int) error {
	if steps <= 0 {
		steps = 10
	}
	srcEl, err := ResolveRefSemantic(page, fromRef, snapshot)
	if err != nil {
		return fmt.Errorf("source %s: %w", fromRef, err)
	}
	dstEl, err := ResolveRefSemantic(page, toRef, snapshot)
	if err != nil {
		return fmt.Errorf("target %s: %w", toRef, err)
	}

	srcBox, err := srcEl.Shape()
	if err != nil {
		return fmt.Errorf("source shape: %w", err)
	}
	dstBox, err := dstEl.Shape()
	if err != nil {
		return fmt.Errorf("target shape: %w", err)
	}
	srcRect := srcBox.Box()
	dstRect := dstBox.Box()
	if srcRect == nil || dstRect == nil {
		return fmt.Errorf("source or target has no visible box")
	}

	sx := srcRect.X + srcRect.Width/2
	sy := srcRect.Y + srcRect.Height/2
	dx := dstRect.X + dstRect.Width/2
	dy := dstRect.Y + dstRect.Height/2

	mouse := page.Mouse
	if err := mouse.MoveTo(proto.NewPoint(sx, sy)); err != nil {
		return fmt.Errorf("move to source: %w", err)
	}
	if err := mouse.Down(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("mouse down: %w", err)
	}

	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		mx := sx + (dx-sx)*t
		my := sy + (dy-sy)*t
		if err := mouse.MoveTo(proto.NewPoint(mx, my)); err != nil {
			return fmt.Errorf("drag step %d: %w", i, err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := mouse.Up(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("mouse up: %w", err)
	}
	settleAfterAction(page, 0)
	return nil
}

// DragDropCoords drags from one coordinate to another.
func DragDropCoords(page *rod.Page, sx, sy, dx, dy float64, steps int) error {
	if steps <= 0 {
		steps = 10
	}
	mouse := page.Mouse
	if err := mouse.MoveTo(proto.NewPoint(sx, sy)); err != nil {
		return fmt.Errorf("move to source: %w", err)
	}
	if err := mouse.Down(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("mouse down: %w", err)
	}
	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		mx := sx + (dx-sx)*t
		my := sy + (dy-sy)*t
		if err := mouse.MoveTo(proto.NewPoint(mx, my)); err != nil {
			return fmt.Errorf("drag step %d: %w", i, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := mouse.Up(proto.InputMouseButtonLeft, 1); err != nil {
		return fmt.Errorf("mouse up: %w", err)
	}
	settleAfterAction(page, 0)
	return nil
}
