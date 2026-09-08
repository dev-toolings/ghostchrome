package runtime

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/dev-toolings/ghostchrome/internal/core/interact"
	"github.com/go-rod/rod/lib/proto"
)

func (s *Session) opInit(_ json.RawMessage) (any, error) {
	_, _, err := s.EnsurePage()
	if err != nil {
		return nil, err
	}
	return map[string]any{"protocol": engine.ProtocolVersion, "version": s.cfg.Version}, nil
}

func (s *Session) opURL() (any, error) {
	_, page, err := s.EnsurePage()
	if err != nil {
		return nil, err
	}
	info, _ := page.Info()
	if info == nil {
		return map[string]string{"url": "", "title": ""}, nil
	}
	return map[string]string{"url": info.URL, "title": info.Title}, nil
}

func (s *Session) opNavigate(raw json.RawMessage) (any, error) {
	var a struct {
		URL  string `json:"url"`
		Wait string `json:"wait"`
	}
	if err := unmarshalArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.URL == "" {
		return nil, errors.New("navigate: url required")
	}
	if a.Wait == "" {
		a.Wait = "load"
	}
	_, page, err := s.EnsurePage()
	if err != nil {
		return nil, err
	}
	info, err := engine.Navigate(page, a.URL, a.Wait)
	if err != nil {
		return nil, err
	}
	if s.cfg.Stealth {
		s.challengeRecovered = engine.WaitForBotChallenge(page, 45*time.Second)
	}
	if s.browser != nil {
		_ = s.browser.InvalidateCachedExtract(page)
	}
	return info, nil
}

func (s *Session) opHistory(delta int) (any, error) {
	_, page, err := s.EnsurePage()
	if err != nil {
		return nil, err
	}
	if err := engine.HistoryStep(page, delta, "load"); err != nil {
		return nil, err
	}
	info, _ := page.Info()
	out := map[string]string{}
	if info != nil {
		out["url"] = info.URL
		out["title"] = info.Title
	}
	if s.browser != nil {
		_ = s.browser.InvalidateCachedExtract(page)
	}
	return out, nil
}

func (s *Session) opExtract(raw json.RawMessage) (any, error) {
	var a struct {
		Level    string `json:"level"`
		Selector string `json:"selector"`
	}
	if err := unmarshalArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Level == "" {
		a.Level = "content"
	}
	level := engine.ExtractLevel(a.Level)
	if err := engine.ValidateExtractLevel(level); err != nil {
		return nil, err
	}
	b, page, err := s.EnsurePage()
	if err != nil {
		return nil, err
	}
	// includeSSR opts in on the recovery path (see challengeRecovered doc).
	// One-shot: consumeChallengeRecovered resets it immediately after reading.
	result, err := engine.Extract(page, level, a.Selector, s.consumeChallengeRecovered())
	if err != nil {
		return nil, err
	}
	if a.Selector == "" {
		_ = b.SaveSnapshot(page, result)
		if snap, serr := engine.BuildSnapshot(page, result); serr == nil {
			s.snapshot = snap
		}
	}
	return result, nil
}

func (s *Session) opRef(raw json.RawMessage, op string) (any, error) {
	var a struct {
		Ref    string `json:"ref"`
		Button string `json:"button"`
	}
	if err := unmarshalArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Ref == "" {
		return nil, fmt.Errorf("%s: ref required", op)
	}
	button := proto.InputMouseButtonLeft
	if op == "click" || op == "dblclick" {
		parsed, err := interact.ParseMouseButton(a.Button)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", op, err)
		}
		button = parsed
	}
	b, page, err := s.EnsurePage()
	if err != nil {
		return nil, err
	}
	prev := s.currentSnapshot(b, page)
	var popupMark uint64
	if op == "click" {
		popupMark = engine.PopupMark(page)
	}
	err = s.withRefRetry(b, page, func(snap *engine.PageSnapshot) error {
		switch op {
		case "click":
			ps := s.rt.PageSession(page)
			if ps != nil {
				return ps.Click(a.Ref, snap, button)
			}
			return engine.ClickRefWithButton(page, a.Ref, snap, button)
		case "dblclick":
			if ps := s.rt.PageSession(page); ps != nil {
				return ps.DblClick(a.Ref, snap, button)
			}
			return engine.DblClickRefWithButton(page, a.Ref, snap, button)
		case "hover":
			if ps := s.rt.PageSession(page); ps != nil {
				return ps.Hover(a.Ref, snap)
			}
			return engine.HoverRef(page, a.Ref, snap)
		}
		return fmt.Errorf("opRef: unsupported op %q", op)
	})
	if err != nil {
		return nil, err
	}
	if op == "click" {
		if popup := engine.AdoptClickPopup(page, popupMark, prev, a.Ref); popup != nil {
			_ = s.adoptPage(b, popup)
			page = popup
		}
	}
	return s.mutationResult(b, page, prev, snapshotModeFromArgs(raw))
}

// opCheck ticks (checked=true) or unticks (checked=false) a checkbox/radio.
func (s *Session) opCheck(raw json.RawMessage, checked bool) (any, error) {
	var a struct {
		Ref string `json:"ref"`
	}
	if err := unmarshalArgs(raw, &a); err != nil {
		return nil, err
	}
	verb := "check"
	if !checked {
		verb = "uncheck"
	}
	if a.Ref == "" {
		return nil, fmt.Errorf("%s: ref required", verb)
	}
	b, page, err := s.EnsurePage()
	if err != nil {
		return nil, err
	}
	prev := s.currentSnapshot(b, page)
	err = s.withRefRetry(b, page, func(snap *engine.PageSnapshot) error {
		if ps := s.rt.PageSession(page); ps != nil {
			return ps.Check(a.Ref, checked, snap)
		}
		return engine.SetCheckedRef(page, a.Ref, checked, snap)
	})
	if err != nil {
		return nil, err
	}
	return s.mutationResult(b, page, prev, snapshotModeFromArgs(raw))
}

// opReload refreshes the current page.
func (s *Session) opReload() (any, error) {
	_, page, err := s.EnsurePage()
	if err != nil {
		return nil, err
	}
	if err := engine.ReloadPage(page, "load"); err != nil {
		return nil, err
	}
	info, _ := page.Info()
	out := map[string]string{}
	if info != nil {
		out["url"] = info.URL
		out["title"] = info.Title
	}
	if s.browser != nil {
		_ = s.browser.InvalidateCachedExtract(page)
	}
	return out, nil
}

func (s *Session) opType(raw json.RawMessage) (any, error) {
	var a struct {
		Ref    string `json:"ref"`
		Text   string `json:"text"`
		Submit bool   `json:"submit"`
	}
	if err := unmarshalArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Ref == "" {
		return nil, errors.New("type: ref required")
	}
	b, page, err := s.EnsurePage()
	if err != nil {
		return nil, err
	}
	prev := s.currentSnapshot(b, page)
	if err := s.withRefRetry(b, page, func(snap *engine.PageSnapshot) error {
		if ps := s.rt.PageSession(page); ps != nil {
			if err := ps.Type(a.Ref, a.Text, snap); err != nil {
				return err
			}
		} else if err := engine.TypeRef(page, a.Ref, a.Text, snap); err != nil {
			return err
		}
		if a.Submit {
			el, err := engine.ResolveRef(page, a.Ref, snap)
			if err != nil {
				return err
			}
			return engine.SubmitOnElement(page, el)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return s.mutationResult(b, page, prev, snapshotModeFromArgs(raw))
}

func (s *Session) opPress(raw json.RawMessage) (any, error) {
	var a struct {
		Key string `json:"key"`
		Ref string `json:"ref"`
	}
	if err := unmarshalArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Key == "" {
		return nil, errors.New("press: key required")
	}
	b, page, err := s.EnsurePage()
	if err != nil {
		return nil, err
	}
	prev := s.currentSnapshot(b, page)
	err = s.withRefRetry(b, page, func(snap *engine.PageSnapshot) error {
		return engine.PressKey(page, a.Key, a.Ref, snap)
	})
	if err != nil {
		return nil, err
	}
	return s.mutationResult(b, page, prev, snapshotModeFromArgs(raw))
}

func (s *Session) opSelect(raw json.RawMessage) (any, error) {
	var a struct {
		Ref    string   `json:"ref"`
		Values []string `json:"values"`
	}
	if err := unmarshalArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Ref == "" || len(a.Values) == 0 {
		return nil, errors.New("select: ref + values required")
	}
	b, page, err := s.EnsurePage()
	if err != nil {
		return nil, err
	}
	prev := s.currentSnapshot(b, page)
	err = s.withRefRetry(b, page, func(snap *engine.PageSnapshot) error {
		if ps := s.rt.PageSession(page); ps != nil {
			return ps.Select(a.Ref, a.Values, snap)
		}
		return engine.SelectOption(page, a.Ref, a.Values, snap)
	})
	if err != nil {
		return nil, err
	}
	return s.mutationResult(b, page, prev, snapshotModeFromArgs(raw))
}

func (s *Session) opFill(raw json.RawMessage) (any, error) {
	var a struct {
		Fields map[string]string `json:"fields"`
	}
	if err := unmarshalArgs(raw, &a); err != nil {
		return nil, err
	}
	if len(a.Fields) == 0 {
		return nil, errors.New("fill: fields required")
	}
	b, page, err := s.EnsurePage()
	if err != nil {
		return nil, err
	}
	snap := s.currentSnapshot(b, page)
	filled, snap, err := engine.FillFields(b, page, a.Fields, snap)
	if err != nil {
		return nil, err
	}
	if snap != nil {
		s.snapshot = snap
	}
	return map[string]int{"filled": filled}, nil
}

func (s *Session) opScrollBy(raw json.RawMessage) (any, error) {
	var a struct {
		DY int `json:"dy"`
	}
	if err := unmarshalArgs(raw, &a); err != nil {
		return nil, err
	}
	_, page, err := s.EnsurePage()
	if err != nil {
		return nil, err
	}
	y, err := engine.ScrollBy(page, a.DY)
	if err != nil {
		return nil, err
	}
	if s.browser != nil {
		_ = s.browser.InvalidateCachedExtract(page)
	}
	return map[string]int{"y": y}, nil
}

func (s *Session) opScrollTo(raw json.RawMessage) (any, error) {
	var a struct {
		Y      int  `json:"y"`
		Bottom bool `json:"bottom"`
	}
	if err := unmarshalArgs(raw, &a); err != nil {
		return nil, err
	}
	_, page, err := s.EnsurePage()
	if err != nil {
		return nil, err
	}
	y, err := engine.ScrollToY(page, a.Y, a.Bottom)
	if err != nil {
		return nil, err
	}
	if s.browser != nil {
		_ = s.browser.InvalidateCachedExtract(page)
	}
	return map[string]int{"y": y}, nil
}

func (s *Session) opEval(raw json.RawMessage) (any, error) {
	var a struct {
		Expr string `json:"expr"`
		Ref  string `json:"ref"`
	}
	if err := unmarshalArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Expr == "" {
		return nil, errors.New("eval: expr required")
	}
	out, err := s.EvalTimeout(a.Expr, a.Ref, 0)
	if err != nil {
		return nil, err
	}
	return map[string]string{"value": out}, nil
}

// EvalTimeout evaluates an expression in the page, resolving an optional @ref
// through the session ref table with the usual recovery. d <= 0 means no
// per-call deadline, which is what the JSONL "eval" op uses (the loop's
// --timeout governs it). MCP's eval tool has its own timeout_ms argument and
// passes it here rather than resolving the ref itself.
func (s *Session) EvalTimeout(expr, ref string, d time.Duration) (string, error) {
	b, page, err := s.EnsurePage()
	if err != nil {
		return "", err
	}
	var out string
	// Op name "ref" is what the JSONL eval path has always reported in a
	// stale-ref recovery error; keep it so the message does not change.
	err = s.withRecovery(b, page, "ref", func(snap *engine.PageSnapshot) error {
		v, evalErr := engine.EvalJSTimeout(page, expr, ref, snap, d)
		out = v
		return evalErr
	})
	if err != nil {
		return "", err
	}
	return out, nil
}

func (s *Session) opScreenshot(raw json.RawMessage) (any, error) {
	var a struct {
		FullPage bool    `json:"full_page"`
		Ref      string  `json:"ref"`
		Quality  int     `json:"quality"`
		Scale    float64 `json:"scale"`
	}
	if err := unmarshalArgs(raw, &a); err != nil {
		return nil, err
	}
	data, err := s.CaptureScreenshot(ScreenshotSpec{
		FullPage: a.FullPage,
		Ref:      a.Ref,
		Quality:  a.Quality,
		Scale:    a.Scale,
	})
	if err != nil {
		return nil, err
	}
	mime := "image/png"
	if a.Quality > 0 {
		mime = "image/jpeg"
	}
	return map[string]string{
		"mime":   mime,
		"base64": base64.StdEncoding.EncodeToString(data),
	}, nil
}

// ScreenshotSpec describes one capture.
//
// Format and Scale come from two different engine entry points and are
// mutually exclusive: Format is the MCP surface's explicit image format
// ("webp" | "jpeg" | "png"), Scale is the JSONL surface's device scale factor.
// Format wins when both are set.
type ScreenshotSpec struct {
	FullPage bool
	Ref      string
	Format   string
	Quality  int
	Scale    float64
}

// CaptureScreenshot takes a screenshot, resolving an optional @ref through the
// session ref table with the usual recovery. Both the JSONL "screenshot" op and
// MCP's screenshot tool go through it, so neither resolves refs on its own.
func (s *Session) CaptureScreenshot(spec ScreenshotSpec) ([]byte, error) {
	b, page, err := s.EnsurePage()
	if err != nil {
		return nil, err
	}
	var data []byte
	err = s.withRefRetry(b, page, func(snap *engine.PageSnapshot) error {
		var shotErr error
		if spec.Format != "" {
			data, shotErr = engine.TakeScreenshotFormat(page, spec.FullPage, spec.Ref, spec.Format, spec.Quality, snap)
		} else {
			data, shotErr = engine.TakeScreenshotScaled(page, spec.FullPage, spec.Ref, spec.Quality, spec.Scale, snap)
		}
		return shotErr
	})
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (s *Session) opWait(raw json.RawMessage) (any, error) {
	var a struct {
		Selector string `json:"selector"`
		Ref      string `json:"ref"`
		Text     string `json:"text"`
		URL      string `json:"url"`
		Load     string `json:"load"`
		State    string `json:"state"`
		MS       int    `json:"ms"`
		Timeout  int    `json:"timeout_ms"`
	}
	if err := unmarshalArgs(raw, &a); err != nil {
		return nil, err
	}
	b, page, err := s.EnsurePage()
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(s.cfg.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if a.Timeout > 0 {
		timeout = time.Duration(a.Timeout) * time.Millisecond
	}
	return engine.WaitForAgent(page, b, s.currentSnapshotIfAvailable(), engine.WaitSpec{
		Selector: a.Selector,
		Ref:      a.Ref,
		Text:     a.Text,
		URL:      a.URL,
		Load:     a.Load,
		State:    a.State,
		MS:       a.MS,
	}, timeout)
}

func (s *Session) opErrors() (any, error) {
	_, _, err := s.EnsurePage()
	if err != nil {
		return nil, err
	}
	if s.observer == nil {
		return nil, fmt.Errorf("errors: no session observer; enable --observe to collect errors in stealth mode")
	}
	return engine.ErrorsFromEvents(s.observer.Drain(0)), nil
}

func (s *Session) opDialog(raw json.RawMessage) (any, error) {
	var a struct {
		Action string `json:"action"`
		Text   string `json:"text"`
	}
	if err := unmarshalArgs(raw, &a); err != nil {
		return nil, err
	}
	action := strings.ToLower(strings.TrimSpace(a.Action))
	if action == "" {
		action = "accept"
	}
	switch action {
	case "accept":
		if s.dialogPolicy == nil {
			s.dialogPolicy = &engine.DialogAutoPolicy{Accept: true, Prompt: a.Text}
		} else {
			s.dialogPolicy.Set(true, a.Text)
		}
		return map[string]any{"action": "accept", "text": a.Text}, nil
	case "dismiss":
		if s.dialogPolicy == nil {
			s.dialogPolicy = &engine.DialogAutoPolicy{Accept: false}
		} else {
			s.dialogPolicy.Set(false, a.Text)
		}
		return map[string]any{"action": "dismiss"}, nil
	default:
		return nil, fmt.Errorf("dialog: unknown action %q (accept|dismiss)", a.Action)
	}
}

func (s *Session) opTabs(raw json.RawMessage) (any, error) {
	var a struct {
		Action string `json:"action"`
		Index  *int   `json:"index"`
		URL    string `json:"url"`
	}
	if err := unmarshalArgs(raw, &a); err != nil {
		return nil, err
	}
	action := strings.ToLower(strings.TrimSpace(a.Action))
	if action == "" {
		action = "list"
	}
	b, page, err := s.EnsurePage()
	if err != nil {
		return nil, err
	}
	currentID := ""
	if page != nil {
		currentID = string(page.TargetID)
	}
	switch action {
	case "list":
		return engine.ListTabs(b.RodBrowser(), currentID)
	case "switch":
		if a.Index == nil {
			return nil, fmt.Errorf("tabs: index required for switch")
		}
		newPage, err := engine.SwitchTab(b.RodBrowser(), *a.Index)
		if err != nil {
			return nil, err
		}
		if err := s.adoptPage(b, newPage); err != nil {
			return nil, err
		}
		info, _ := newPage.Info()
		out := map[string]any{"action": "switch", "index": *a.Index}
		if info != nil {
			out["url"] = info.URL
			out["title"] = info.Title
		}
		return out, nil
	case "close":
		if a.Index == nil {
			return nil, fmt.Errorf("tabs: index required for close")
		}
		closedID, err := engine.CloseTab(b.RodBrowser(), *a.Index)
		if err != nil {
			return nil, err
		}
		_ = b.DeleteSnapshot(closedID)
		if s.page != nil && s.page.TargetID == closedID {
			pages, perr := b.RodBrowser().Pages()
			if perr == nil && len(pages) > 0 {
				_ = s.adoptPage(b, pages[0])
			} else {
				s.page = nil
				s.snapshot = nil
			}
		}
		return map[string]any{"action": "close", "index": *a.Index}, nil
	case "new":
		newPage, err := engine.NewTab(b.RodBrowser(), a.URL)
		if err != nil {
			return nil, err
		}
		if err := s.adoptPage(b, newPage); err != nil {
			return nil, err
		}
		info, _ := newPage.Info()
		out := map[string]any{"action": "new"}
		if info != nil {
			out["url"] = info.URL
			out["title"] = info.Title
		}
		return out, nil
	default:
		return nil, fmt.Errorf("tabs: unknown action %q (list|switch|close|new)", a.Action)
	}
}
