package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/cdp"
)

// A page script must not make passive challenge inspection depend on the
// JavaScript selector helpers used by Rod's Page.HTML implementation.
func TestChallengeInspectionSurvivesBrokenPageSelectors(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)
	if _, err := Navigate(page, dataURL(`<html><body>window._cf_chl_opt</body></html>`), "domcontentloaded"); err != nil {
		t.Fatal(err)
	}
	if _, err := page.Eval(`() => { document.querySelector = () => null; }`); err != nil {
		t.Fatal(err)
	}
	if !isBotChallenge(page.Timeout(500 * time.Millisecond)) {
		t.Fatal("challenge markup must remain readable when page selector helpers cannot resolve html")
	}
}

type stalledInspectionClient struct {
	events      chan *cdp.Event
	stallPrefix string
}

func (c stalledInspectionClient) Event() <-chan *cdp.Event { return c.events }

func (c stalledInspectionClient) Call(ctx context.Context, _, method string, _ interface{}) ([]byte, error) {
	if strings.HasPrefix(method, c.stallPrefix) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if method == "Target.attachToTarget" {
		return []byte(`{"sessionId":"inspection-test"}`), nil
	}
	return []byte(`{}`), nil
}

// A missing CDP reply must respect both the passive budget and an earlier
// caller deadline, rather than leaving the JSONL response blocked forever.
func TestPassiveInspectionBoundsUnresponsiveCDP(t *testing.T) {
	inspections := []struct {
		name   string
		prefix string
		read   func(*rod.Page) error
	}{
		{"markup", "DOM.", func(page *rod.Page) error { _, err := InspectionHTML(page); return err }},
		{"metadata", "Target.getTargetInfo", func(page *rod.Page) error { _, err := InspectionInfo(page); return err }},
	}
	for _, inspection := range inspections {
		for _, callerTimeout := range []time.Duration{10 * time.Second, 30 * time.Millisecond} {
			t.Run(inspection.name+"/"+callerTimeout.String(), func(t *testing.T) {
				client := stalledInspectionClient{events: make(chan *cdp.Event), stallPrefix: inspection.prefix}
				t.Cleanup(func() { close(client.events) })
				browser, cancel := rod.New().NoDefaultDevice().Client(client).WithCancel()
				t.Cleanup(cancel)
				if err := browser.Connect(); err != nil {
					t.Fatal(err)
				}
				page, err := browser.PageFromTarget("inspection-test")
				if err != nil {
					t.Fatal(err)
				}
				page = page.Timeout(callerTimeout)
				defer page.CancelTimeout()
				start := time.Now()
				err = inspection.read(page)
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("expected bounded inspection failure, got %v", err)
				}
				budget := min(callerTimeout, 2*time.Second)
				if elapsed := time.Since(start); elapsed > budget+time.Second {
					t.Fatalf("inspection exceeded its budget: %v", elapsed)
				}
			})
		}
	}
}
