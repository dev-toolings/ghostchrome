package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/go-rod/rod"
	"github.com/spf13/cobra"
)

var flagFillFormFile string

var fillformCmd = &cobra.Command{
	Use:   "fill-form [json]",
	Short: "Fill many form fields in one call with a JSON map {@ref: value}",
	Long: `fill-form accepts a JSON object whose keys are refs (e.g. "@3") and whose
values are the content to apply. One command replaces N type/click/select calls
— a single tool-use round-trip for the agent.

Value types:
  string  → typed into textbox / combobox (TypeRef)
  bool    → toggled via click (ClickRef) — caller should only send bool when a
            toggle is desired
  [str..] → multi-select (SelectOption)

Examples:
  ghostchrome fill-form '{"@3":"alice@example.com","@5":"hunter2","@7":true}'
  ghostchrome fill-form --file form.json
  cat form.json | ghostchrome fill-form -`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		raw, err := readFillPayload(args)
		if err != nil {
			exitErr("fill-form", err)
		}

		var form map[string]any
		if err := json.Unmarshal(raw, &form); err != nil {
			exitErr("fill-form", fmt.Errorf("parse JSON: %w", err))
		}
		if len(form) == 0 {
			exitErr("fill-form", fmt.Errorf("empty form (nothing to fill)"))
		}

		b, page := openPage()
		defer b.Close()

		snapshot := ensureSnapshot(b, page, "", "none", engine.LevelSkeleton)
		defer func() { _ = b.InvalidateCachedExtract(page) }()

		type fieldResult struct {
			Ref   string `json:"ref"`
			OK    bool   `json:"ok"`
			Kind  string `json:"kind"`
			Error string `json:"error,omitempty"`
		}
		var results []fieldResult
		refs := make([]string, 0, len(form))
		for ref := range form {
			refs = append(refs, ref)
		}
		sort.Slice(refs, func(i, j int) bool {
			ni, iok := engine.SnapshotRefNum(refs[i])
			nj, jok := engine.SnapshotRefNum(refs[j])
			if iok && jok {
				return ni < nj
			}
			if iok != jok {
				return iok
			}
			return refs[i] < refs[j]
		})
		for _, ref := range refs {
			kind, errMsg := applyFormField(page, ref, form[ref], snapshot)
			results = append(results, fieldResult{
				Ref:   ref,
				Kind:  kind,
				OK:    errMsg == "",
				Error: errMsg,
			})
			if errMsg == "" {
				_ = b.InvalidateCachedExtract(page)
				if result, err := engine.Extract(page, engine.LevelSkeleton, "", false); err == nil {
					_ = b.SaveSnapshot(page, result)
					if next, serr := engine.BuildSnapshot(page, result); serr == nil {
						snapshot = next
					}
				}
			}
		}

		ok, failed := 0, 0
		for _, r := range results {
			if r.OK {
				ok++
			} else {
				failed++
			}
		}

		type fillResult struct {
			Filled []fieldResult `json:"filled"`
			OK     int           `json:"ok"`
			Failed int           `json:"failed"`
			Total  int           `json:"total"`
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "[fill-form] %d fields, %d ok, %d failed\n", len(results), ok, failed)
		for _, r := range results {
			marker := "✓"
			if !r.OK {
				marker = "✗"
			}
			fmt.Fprintf(&sb, "  %s %s (%s)", marker, r.Ref, r.Kind)
			if r.Error != "" {
				fmt.Fprintf(&sb, " — %s", r.Error)
			}
			sb.WriteString("\n")
		}
		text := strings.TrimRight(sb.String(), "\n")

		output(&fillResult{Filled: results, OK: ok, Failed: failed, Total: len(results)}, text)
	},
}

func readFillPayload(args []string) ([]byte, error) {
	if flagFillFormFile != "" {
		return os.ReadFile(flagFillFormFile)
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("need JSON as positional arg, --file, or '-' for stdin")
	}
	if args[0] == "-" {
		return io.ReadAll(os.Stdin)
	}
	return []byte(args[0]), nil
}

// applyFormField dispatches a (ref, value) pair to the correct engine primitive
// based on the value type. Returns (kind, error-message-or-empty).
func applyFormField(page *rod.Page, ref string, value any, snapshot *engine.PageSnapshot) (string, string) {
	switch v := value.(type) {
	case string:
		if err := engine.TypeRef(page, ref, v, snapshot); err != nil {
			return "text", err.Error()
		}
		return "text", ""
	case bool:
		if err := engine.ClickRef(page, ref, snapshot); err != nil {
			return "toggle", err.Error()
		}
		return "toggle", ""
	case []any:
		opts := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return "select", fmt.Sprintf("non-string item in array: %v", item)
			}
			opts = append(opts, s)
		}
		if err := engine.SelectOption(page, ref, opts, snapshot); err != nil {
			return "select", err.Error()
		}
		return "select", ""
	case float64, int:
		if err := engine.TypeRef(page, ref, fmt.Sprintf("%v", v), snapshot); err != nil {
			return "text", err.Error()
		}
		return "text", ""
	case nil:
		return "skip", "nil value"
	default:
		return "unknown", fmt.Sprintf("unsupported value type %T", value)
	}
}

func init() {
	fillformCmd.Flags().StringVar(&flagFillFormFile, "file", "", "Read JSON payload from file")
	rootCmd.AddCommand(fillformCmd)
}
