package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/dev-toolings/ghostchrome/internal/core/artifact"
	"github.com/go-rod/rod"
)

func startObserveOut(page *rod.Page) func(opName string, ok bool, durationMs int64, summary string) {
	if flagObserveOut == "" {
		return func(string, bool, int64, string) {}
	}
	outPath, err := validateOutputPath(flagObserveOut)
	if err != nil {
		fmt.Fprintf(os.Stderr, "observe-out: %v\n", err)
		return func(string, bool, int64, string) {}
	}
	if flagStealth {
		fmt.Fprintln(os.Stderr, "ghostchrome: --observe-out enables the Runtime CDP domain, weakening --stealth")
	}
	obs := engine.NewObserver(page, engine.ObserverOpts{})
	if err := obs.Start(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "observe-out: observer start: %v\n", err)
		return func(string, bool, int64, string) {}
	}
	go func() {
		for range obs.Events() {
		}
	}()

	return func(opName string, ok bool, durationMs int64, summary string) {
		_ = obs.Stop()

		f, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "observe-out: open file: %v\n", err)
			return
		}
		defer f.Close()

		enc := json.NewEncoder(f)

		cmdEntry := artifact.TraceEntry{
			TS:         time.Now().UnixMilli() - durationMs,
			Op:         opName,
			OK:         ok,
			DurationMs: durationMs,
			Summary:    summary,
		}
		_ = enc.Encode(cmdEntry)

		for _, evt := range obs.Drain(0) {
			op := string(evt.Kind)
			summ := evt.URL
			if evt.Event != "" {
				summ = evt.Event
			}
			if evt.Text != "" {
				summ = evt.Text
			}
			_ = enc.Encode(artifact.TraceEntry{
				TS:         evt.TS,
				Op:         op,
				OK:         evt.Failed == "" && (evt.Status == 0 || evt.Status < 400),
				DurationMs: evt.DurationMs,
				Summary:    summ,
				Error:      evt.Failed,
				Args: map[string]any{
					"url":    evt.URL,
					"method": evt.Method,
					"status": evt.Status,
					"type":   evt.Type,
				},
			})
		}
	}
}
