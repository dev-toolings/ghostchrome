package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dev-toolings/ghostchrome/internal/core/inspect"
	"github.com/spf13/cobra"
)

var flagReactDepth int

var reactCmd = &cobra.Command{
	Use:   "react",
	Short: "React DevTools integration",
}

var reactTreeCmd = &cobra.Command{
	Use:   "tree [url]",
	Short: "Extract the React component tree",
	Long: `Inspect the React fiber tree via __REACT_DEVTOOLS_GLOBAL_HOOK__.
Shows component names, types, props (primitives only), and structure.

Examples:
  ghostchrome react tree https://app.example.com
  ghostchrome react tree --connect auto --depth 5`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		b, page := openPage()
		defer b.Close()

		if len(args) > 0 {
			navigateIfRequested(page, args[0], "load")
		}

		tree, err := inspect.ReactTree(page, flagReactDepth)
		if err != nil {
			exitErr("react tree", err)
		}

		var sb strings.Builder
		sb.WriteString("[react] component tree\n")
		printTree(&sb, tree, 0)
		output(tree, strings.TrimRight(sb.String(), "\n"))
	},
}

var reactSuspenseCmd = &cobra.Command{
	Use:   "suspense [url]",
	Short: "List React Suspense boundary states",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		b, page := openPage()
		defer b.Close()

		if len(args) > 0 {
			navigateIfRequested(page, args[0], "load")
		}

		boundaries, err := inspect.ReactSuspense(page)
		if err != nil {
			exitErr("react suspense", err)
		}

		var sb strings.Builder
		sb.WriteString("[react] suspense boundaries\n")
		for _, b := range boundaries {
			state := "resolved"
			if fb, ok := b["fallback"].(bool); ok && fb {
				state = "fallback"
			}
			name, _ := b["name"].(string)
			fmt.Fprintf(&sb, "  %s: %s\n", name, state)
		}
		output(boundaries, strings.TrimRight(sb.String(), "\n"))
	},
}

func printTree(sb *strings.Builder, components []inspect.ReactComponent, indent int) {
	prefix := strings.Repeat("  ", indent)
	for _, c := range components {
		propsStr := ""
		if len(c.Props) > 0 {
			var props map[string]any
			if json.Unmarshal(c.Props, &props) == nil && len(props) > 0 {
				pairs := make([]string, 0, len(props))
				for k, v := range props {
					pairs = append(pairs, fmt.Sprintf("%s=%v", k, v))
				}
				propsStr = " " + strings.Join(pairs, " ")
			}
		}
		fmt.Fprintf(sb, "%s<%s%s>\n", prefix, c.Name, propsStr)
		if len(c.Children) > 0 {
			printTree(sb, c.Children, indent+1)
		}
	}
}

func init() {
	reactTreeCmd.Flags().IntVar(&flagReactDepth, "depth", 10, "Max tree depth to extract")
	reactCmd.AddCommand(reactTreeCmd, reactSuspenseCmd)
	rootCmd.AddCommand(reactCmd)
}
