package cmd

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/interact"
	"github.com/go-rod/rod/lib/proto"
	"github.com/spf13/cobra"
)

var flagMouseButton string

var mouseCmd = &cobra.Command{
	Use:   "mouse",
	Short: "Raw mouse operations (move, click, down, up, wheel)",
}

var mouseMoveCmd = &cobra.Command{
	Use:   "move <x> <y>",
	Short: "Move mouse to coordinates",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		x, y := parseCoords(args[0], args[1])
		b, page := openPage()
		defer b.Close()

		if err := page.Mouse.MoveTo(proto.NewPoint(x, y)); err != nil {
			exitErr("mouse move", err)
		}
		output(map[string]float64{"x": x, "y": y},
			fmt.Sprintf("[mouse] moved to (%.0f, %.0f)", x, y))
	},
}

var mouseClickCmd = &cobra.Command{
	Use:   "click <x> <y>",
	Short: "Click at coordinates",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		x, y := parseCoords(args[0], args[1])
		b, page := openPage()
		defer b.Close()

		btn := parseMouseButton(flagMouseButton)
		if err := page.Mouse.MoveTo(proto.NewPoint(x, y)); err != nil {
			exitErr("mouse move", err)
		}
		if err := page.Mouse.Down(btn, 1); err != nil {
			exitErr("mouse down", err)
		}
		time.Sleep(50 * time.Millisecond)
		if err := page.Mouse.Up(btn, 1); err != nil {
			exitErr("mouse up", err)
		}
		output(map[string]float64{"x": x, "y": y},
			fmt.Sprintf("[mouse] clicked at (%.0f, %.0f)", x, y))
	},
}

var mouseDownCmd = &cobra.Command{
	Use:   "down",
	Short: "Press mouse button down",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		b, page := openPage()
		defer b.Close()

		btn := parseMouseButton(flagMouseButton)
		if err := page.Mouse.Down(btn, 1); err != nil {
			exitErr("mouse down", err)
		}
		output(map[string]string{"action": "down", "button": flagMouseButton},
			fmt.Sprintf("[mouse] %s down", flagMouseButton))
	},
}

var mouseUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Release mouse button",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		b, page := openPage()
		defer b.Close()

		btn := parseMouseButton(flagMouseButton)
		if err := page.Mouse.Up(btn, 1); err != nil {
			exitErr("mouse up", err)
		}
		output(map[string]string{"action": "up", "button": flagMouseButton},
			fmt.Sprintf("[mouse] %s up", flagMouseButton))
	},
}

var (
	flagWheelDeltaX float64
	flagWheelDeltaY float64
)

var mouseWheelCmd = &cobra.Command{
	Use:   "wheel",
	Short: "Scroll using mouse wheel",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		b, page := openPage()
		defer b.Close()

		if err := page.Mouse.Scroll(flagWheelDeltaX, flagWheelDeltaY, 0); err != nil {
			exitErr("mouse wheel", err)
		}
		output(map[string]float64{"delta_x": flagWheelDeltaX, "delta_y": flagWheelDeltaY},
			fmt.Sprintf("[mouse] wheel dx=%.0f dy=%.0f", flagWheelDeltaX, flagWheelDeltaY))
	},
}

func parseCoords(xStr, yStr string) (float64, float64) {
	x, err := strconv.ParseFloat(xStr, 64)
	if err != nil {
		exitErr("parse x", err)
	}
	y, err := strconv.ParseFloat(yStr, 64)
	if err != nil {
		exitErr("parse y", err)
	}
	return x, y
}

func parseMouseButton(name string) proto.InputMouseButton {
	button, err := interact.ParseMouseButton(name)
	if err != nil {
		exitErr("button", err)
	}
	return button
}

// parseMouseButtonStrict is used by higher-level command handlers that need to
// distinguish a true button value from another string (for example a URL).
func parseMouseButtonStrict(name string) (proto.InputMouseButton, bool) {
	button, err := interact.ParseMouseButton(name)
	if err != nil || strings.TrimSpace(name) == "" {
		return proto.InputMouseButtonLeft, false
	}
	return button, true
}

func init() {
	mouseClickCmd.Flags().StringVar(&flagMouseButton, "button", "left", "Mouse button: left, right, middle")
	mouseDownCmd.Flags().StringVar(&flagMouseButton, "button", "left", "Mouse button: left, right, middle")
	mouseUpCmd.Flags().StringVar(&flagMouseButton, "button", "left", "Mouse button: left, right, middle")
	mouseWheelCmd.Flags().Float64Var(&flagWheelDeltaX, "delta-x", 0, "Horizontal scroll delta")
	mouseWheelCmd.Flags().Float64Var(&flagWheelDeltaY, "delta-y", 100, "Vertical scroll delta")

	mouseCmd.AddCommand(mouseMoveCmd, mouseClickCmd, mouseDownCmd, mouseUpCmd, mouseWheelCmd)
	rootCmd.AddCommand(mouseCmd)
}
