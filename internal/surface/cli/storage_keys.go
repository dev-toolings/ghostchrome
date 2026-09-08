package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/go-rod/rod"
	"github.com/spf13/cobra"
)

type browserStorageArea string

const (
	localStorageArea   browserStorageArea = "localStorage"
	sessionStorageArea browserStorageArea = "sessionStorage"
)

type storageKVResult struct {
	Area  browserStorageArea `json:"area"`
	Key   string             `json:"key,omitempty"`
	Value string             `json:"value,omitempty"`
	Found bool               `json:"found,omitempty"`
	Count int                `json:"count,omitempty"`
	Items map[string]string  `json:"items,omitempty"`
}

func storageAreaListCommand(prefix string, area browserStorageArea) *cobra.Command {
	return &cobra.Command{
		Use:   prefix + "-list",
		Short: fmt.Sprintf("List %s key-value pairs", area),
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			b, page := openPage()
			defer b.Close()

			items := evalStorageList(page, area)
			output(storageKVResult{Area: area, Count: len(items), Items: items}, formatStorageItems(area, items))
		},
	}
}

func storageAreaGetCommand(prefix string, area browserStorageArea) *cobra.Command {
	return &cobra.Command{
		Use:   prefix + "-get <key>",
		Short: fmt.Sprintf("Get a %s value by key", area),
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			b, page := openPage()
			defer b.Close()

			value, found := evalStorageGet(page, area, args[0])
			text := "No value"
			if found {
				text = value
			}
			output(storageKVResult{Area: area, Key: args[0], Value: value, Found: found}, text)
		},
	}
}

func storageAreaSetCommand(prefix string, area browserStorageArea) *cobra.Command {
	return &cobra.Command{
		Use:   prefix + "-set <key> <value>",
		Short: fmt.Sprintf("Set a %s value", area),
		Args:  cobra.ExactArgs(2),
		Run: func(cmd *cobra.Command, args []string) {
			b, page := openPage()
			defer b.Close()

			evalStorageSet(page, area, args[0], args[1])
			output(storageKVResult{Area: area, Key: args[0], Value: args[1], Found: true},
				fmt.Sprintf("%s set: %s", area, args[0]))
		},
	}
}

func storageAreaDeleteCommand(prefix string, area browserStorageArea) *cobra.Command {
	return &cobra.Command{
		Use:   prefix + "-delete <key>",
		Short: fmt.Sprintf("Delete a %s key", area),
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			b, page := openPage()
			defer b.Close()

			evalStorageDelete(page, area, args[0])
			output(storageKVResult{Area: area, Key: args[0]}, fmt.Sprintf("%s deleted: %s", area, args[0]))
		},
	}
}

func storageAreaClearCommand(prefix string, area browserStorageArea) *cobra.Command {
	return &cobra.Command{
		Use:   prefix + "-clear",
		Short: fmt.Sprintf("Clear %s", area),
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			b, page := openPage()
			defer b.Close()

			evalStorageClear(page, area)
			output(storageKVResult{Area: area}, fmt.Sprintf("%s cleared", area))
		},
	}
}

func evalStorageList(page *rod.Page, area browserStorageArea) map[string]string {
	res, err := page.Eval(`(areaName) => {
		const storage = window[areaName];
		const out = {};
		for (let i = 0; i < storage.length; i++) {
			const key = storage.key(i);
			out[key] = storage.getItem(key);
		}
		return out;
	}`, string(area))
	if err != nil {
		exitErr(string(area)+" list", err)
	}
	raw, err := res.Value.MarshalJSON()
	if err != nil {
		exitErr(string(area)+" list", err)
	}
	var items map[string]string
	if err := json.Unmarshal(raw, &items); err != nil {
		exitErr(string(area)+" list", err)
	}
	return items
}

func evalStorageGet(page *rod.Page, area browserStorageArea, key string) (string, bool) {
	res, err := page.Eval(`(args) => {
		const value = window[args.area].getItem(args.key);
		return {found: value !== null, value: value === null ? "" : value};
	}`, map[string]string{"area": string(area), "key": key})
	if err != nil {
		exitErr(string(area)+" get", err)
	}
	raw, err := res.Value.MarshalJSON()
	if err != nil {
		exitErr(string(area)+" get", err)
	}
	var payload struct {
		Found bool   `json:"found"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		exitErr(string(area)+" get", err)
	}
	return payload.Value, payload.Found
}

func evalStorageSet(page *rod.Page, area browserStorageArea, key, value string) {
	_, err := page.Eval(`(args) => {
		window[args.area].setItem(args.key, args.value);
		return true;
	}`, map[string]string{"area": string(area), "key": key, "value": value})
	if err != nil {
		exitErr(string(area)+" set", err)
	}
}

func evalStorageDelete(page *rod.Page, area browserStorageArea, key string) {
	_, err := page.Eval(`(args) => {
		window[args.area].removeItem(args.key);
		return true;
	}`, map[string]string{"area": string(area), "key": key})
	if err != nil {
		exitErr(string(area)+" delete", err)
	}
}

func evalStorageClear(page *rod.Page, area browserStorageArea) {
	_, err := page.Eval(`(areaName) => {
		window[areaName].clear();
		return true;
	}`, string(area))
	if err != nil {
		exitErr(string(area)+" clear", err)
	}
}

func formatStorageItems(area browserStorageArea, items map[string]string) string {
	if len(items) == 0 {
		return fmt.Sprintf("No %s entries", area)
	}
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var sb strings.Builder
	fmt.Fprintf(&sb, "[%s] %d\n", area, len(keys))
	for _, key := range keys {
		fmt.Fprintf(&sb, "  %s=%s\n", key, items[key])
	}
	return strings.TrimRight(sb.String(), "\n")
}

func init() {
	for _, spec := range []struct {
		prefix string
		area   browserStorageArea
	}{
		{prefix: "localstorage", area: localStorageArea},
		{prefix: "sessionstorage", area: sessionStorageArea},
	} {
		rootCmd.AddCommand(
			storageAreaListCommand(spec.prefix, spec.area),
			storageAreaGetCommand(spec.prefix, spec.area),
			storageAreaSetCommand(spec.prefix, spec.area),
			storageAreaDeleteCommand(spec.prefix, spec.area),
			storageAreaClearCommand(spec.prefix, spec.area),
		)
	}
}
