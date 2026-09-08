package cli

import (
	"fmt"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/spf13/cobra"
)

var typeWaitFor string
var typeWaitTimeoutMs int
var typeSubmit bool

var typeLocator LocatorFlags

var typeCmd = &cobra.Command{
	Use:     "type <text> | type <ref> <text> [url]",
	Aliases: []string{"fill"},
	Short:   "Type text into the focused element or an input by ref/locator",
	Long: `Type text into an element.

With one positional and no locator: type into the currently focused element.
With a ref: first positional is @N, second is the text to type.
With a locator: pass --by-* flags, and a single positional (the text to type).
An optional URL argument may follow.

Examples:
  ghostchrome type "hello world"
  ghostchrome type @2 "hello world"
  ghostchrome type --by-label "Email" "kev@example.com"
  ghostchrome type --by-role textbox --by-name "Search" "ghostchrome"`,
	Args: cobra.RangeArgs(1, 3),
	Run: func(cmd *cobra.Command, args []string) {
		var ref, text, targetURL string
		focusedMode := false
		if typeLocator.Any() {
			// With a locator: positionals are [text] [url]
			if len(args) < 1 {
				exitErr("type", fmt.Errorf("need TEXT positional when using --by-*"))
			}
			text = args[0]
			if len(args) > 1 {
				targetURL = args[1]
			}
		} else {
			if len(args) == 1 {
				text = args[0]
				focusedMode = true
			} else {
				ref = args[0]
				text = args[1]
				if len(args) > 2 {
					targetURL = args[2]
				}
			}
		}

		b, page := openPage()
		defer b.Close()

		waitState, waitTimeout := resolveWaitFlags(cmd, typeWaitFor, typeWaitTimeoutMs)

		// Resolve the target element through every path so --submit can
		// re-focus the exact field it typed into.
		var typedEl *rod.Element
		var rerr error
		if focusedMode {
			if err := engine.TypeFocused(page, text); err != nil {
				exitErr("type", err)
			}
		} else {
			var snapshot *engine.PageSnapshot
			if targetURL != "" || isSnapshotRef(ref) {
				snapshot = ensureSnapshot(b, page, targetURL, "load", engine.LevelSkeleton)
			}
			switch {
			case typeLocator.Any():
				typedEl, rerr = engine.WaitForLocator(page, typeLocator.ToLocator(), waitState, waitTimeout)
				if rerr != nil {
					exitErr("type", rerr)
				}
				if err := engine.TypeElement(page, typedEl, text); err != nil {
					exitErr("type", err)
				}
			default:
				if waitTimeout > 0 {
					if _, rerr = engine.WaitForTarget(page, ref, snapshot, waitState, waitTimeout); rerr != nil {
						exitIfStaleRef(rerr, "type")
						exitErr("type", rerr)
					}
				}
				if rerr = engine.TypeRef(page, ref, text, snapshot); rerr != nil {
					exitIfStaleRef(rerr, "type")
					exitErr("type", rerr)
				}
				typedEl, rerr = engine.ResolveRef(page, ref, snapshot)
				if rerr != nil {
					exitIfStaleRef(rerr, "type")
					exitErr("type", rerr)
				}
			}

			if typeSubmit {
				if err := engine.SubmitOnElement(page, typedEl); err != nil {
					exitErr("type --submit", err)
				}
			}
		}
		if focusedMode && typeSubmit {
			if err := page.Keyboard.Type(input.Enter); err != nil {
				exitErr("type --submit", err)
			}
		}

		emitMutationOutput("type", ref, b, page, nil)
	},
}

func init() {
	typeLocator.RegisterOn(typeCmd)
	typeCmd.Flags().StringVar(&typeWaitFor, "wait-for", "", "Wait for element state before typing: attached|visible|hidden|enabled|stable|none (default: visible)")
	typeCmd.Flags().IntVar(&typeWaitTimeoutMs, "wait-timeout-ms", 0, "Max milliseconds to wait for the element state (0 = no wait; default: 5000)")
	typeCmd.Flags().BoolVar(&typeSubmit, "submit", false, "Press Enter after typing (submit the form)")
	registerSnapshotModeFlag(typeCmd)
	rootCmd.AddCommand(typeCmd)
}
