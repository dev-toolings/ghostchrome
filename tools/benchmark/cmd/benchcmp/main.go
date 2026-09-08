package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/dev-toolings/ghostchrome/tools/benchmark/report"
)

func main() {
	var (
		inputPath   string
		markdownOut string
		jsonOut     string
	)

	flag.StringVar(&inputPath, "input", "", "Path to a benchmark suite JSON file")
	flag.StringVar(&markdownOut, "markdown-out", "", "Optional path to write the Markdown report")
	flag.StringVar(&jsonOut, "json-out", "", "Optional path to write the JSON summary")
	flag.Parse()

	if inputPath == "" {
		exitf("missing --input")
	}

	data, err := os.ReadFile(inputPath)
	if err != nil {
		exitf("read input: %v", err)
	}

	var suite report.Suite
	if err := json.Unmarshal(data, &suite); err != nil {
		exitf("parse input: %v", err)
	}

	rep, err := report.Build(suite)
	if err != nil {
		exitf("build report: %v", err)
	}

	markdown := report.Markdown(rep)
	if markdownOut == "" {
		fmt.Print(markdown)
	} else if err := os.WriteFile(markdownOut, []byte(markdown), 0644); err != nil {
		exitf("write markdown: %v", err)
	}

	if jsonOut != "" {
		payload, err := report.JSON(rep)
		if err != nil {
			exitf("encode json: %v", err)
		}
		if err := os.WriteFile(jsonOut, payload, 0644); err != nil {
			exitf("write json: %v", err)
		}
	}
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
