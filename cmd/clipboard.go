package cmd

import (
	"fmt"

	"github.com/dev-toolings/ghostchrome/internal/core/interact"
	"github.com/spf13/cobra"
)

var clipboardCmd = &cobra.Command{
	Use:   "clipboard",
	Short: "Read or write the page clipboard",
}

var clipboardReadCmd = &cobra.Command{
	Use:   "read",
	Short: "Read clipboard text content",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		b, page := openPage()
		defer b.Close()

		text, err := interact.ClipboardRead(page)
		if err != nil {
			exitErr("clipboard read", err)
		}

		type clipResult struct {
			Action string `json:"action"`
			Text   string `json:"text"`
		}
		output(&clipResult{Action: "read", Text: text},
			fmt.Sprintf("[clipboard] %s", text))
	},
}

var clipboardWriteCmd = &cobra.Command{
	Use:   "write <text>",
	Short: "Write text to clipboard",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		text := args[0]

		b, page := openPage()
		defer b.Close()

		if err := interact.ClipboardWrite(page, text); err != nil {
			exitErr("clipboard write", err)
		}

		type clipResult struct {
			Action string `json:"action"`
			Chars  int    `json:"chars"`
		}
		output(&clipResult{Action: "write", Chars: len(text)},
			fmt.Sprintf("[clipboard] wrote %d chars", len(text)))
	},
}

func init() {
	clipboardCmd.AddCommand(clipboardReadCmd)
	clipboardCmd.AddCommand(clipboardWriteCmd)
	rootCmd.AddCommand(clipboardCmd)
}
