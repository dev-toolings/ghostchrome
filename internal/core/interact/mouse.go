package interact

import (
	"fmt"
	"strings"

	"github.com/go-rod/rod/lib/proto"
)

// ParseMouseButton validates Playwright-compatible mouse buttons and returns
// the corresponding Rod protocol enum.
func ParseMouseButton(name string) (proto.InputMouseButton, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "left":
		return proto.InputMouseButtonLeft, nil
	case "right":
		return proto.InputMouseButtonRight, nil
	case "middle":
		return proto.InputMouseButtonMiddle, nil
	default:
		return "", fmt.Errorf("invalid mouse button %q: expected left, right, or middle", name)
	}
}
