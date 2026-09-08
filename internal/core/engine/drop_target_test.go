package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePlaywrightLocator(t *testing.T) {
	tests := []struct {
		target string
		want   Locator
	}{
		{`getByRole("button", { name: "Save" })`, Locator{Role: "button", Name: "Save"}},
		{`page.getByText("Learn more")`, Locator{Text: "Learn more"}},
		{`getByLabel("Email")`, Locator{Label: "Email"}},
	}
	for _, tt := range tests {
		got, ok, err := parsePlaywrightLocator(tt.target)
		if err != nil || !ok || got != tt.want {
			t.Fatalf("parsePlaywrightLocator(%q) = %#v, %t, %v; want %#v, true, nil", tt.target, got, ok, err, tt.want)
		}
	}
	if _, ok, err := parsePlaywrightLocator(`getByTestId("save")`); ok || err != nil {
		t.Fatalf("unsupported locator parsed: ok=%t err=%v", ok, err)
	}
}

func TestResolveTargetCSSAndPlaywrightRef(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)
	if _, err := Navigate(page, dataURL(`<!doctype html><button id="save">Save</button><div class="many"></div><div class="many"></div>`), "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	el, err := ResolveTarget(page, "#save", nil)
	if err != nil {
		t.Fatalf("resolve CSS target: %v", err)
	}
	name, err := el.Eval(`() => this.textContent`)
	if err != nil || name.Value.Str() != "Save" {
		t.Fatalf("CSS target did not resolve Save: result=%v err=%v", name, err)
	}

	if _, err := ResolveTarget(page, ".many", nil); err == nil || !strings.Contains(err.Error(), "matched 2") {
		t.Fatalf("ambiguous CSS target error = %v", err)
	}
	if _, err := ResolveTarget(page, "#missing", nil); err == nil || !strings.Contains(err.Error(), "matched 0") {
		t.Fatalf("missing CSS target error = %v", err)
	}

	result, err := Extract(page, LevelSkeleton, "", false)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	snapshot, err := BuildSnapshot(page, result)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	refEl, err := ResolveTarget(page, "e1", snapshot)
	if err != nil {
		t.Fatalf("resolve playwright ref: %v", err)
	}
	refName, err := refEl.Eval(`() => this.textContent`)
	if err != nil || refName.Value.Str() != "Save" {
		t.Fatalf("playwright ref did not resolve Save: result=%v err=%v", refName, err)
	}

	locatorEl, err := ResolveTarget(page, `page.getByRole("button", { name: "Save" })`, nil)
	if err != nil {
		t.Fatalf("resolve Playwright locator: %v", err)
	}
	locatorName, err := locatorEl.Eval(`() => this.textContent`)
	if err != nil || locatorName.Value.Str() != "Save" {
		t.Fatalf("Playwright locator did not resolve Save: result=%v err=%v", locatorName, err)
	}
}

func TestResolveTargetPlaywrightLocatorsAreStrictAndVisible(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)
	if _, err := Navigate(page, dataURL(`<!doctype html>
		<button id="role">Unique role</button>
		<span id="text">Unique text</span>
		<label>Email<input id="label" aria-label="Email"></label>`), "load"); err != nil {
		t.Fatalf("navigate unique locators: %v", err)
	}
	for _, target := range []string{
		`getByRole("button", { name: "Unique role" })`,
		`getByText("Unique text")`,
		`getByLabel("Email")`,
	} {
		if _, err := ResolveTarget(page, target, nil); err != nil {
			t.Fatalf("ResolveTarget(%q) = %v", target, err)
		}
	}

	html := `<!doctype html>
		<button>Duplicate role</button><button>Duplicate role</button>
		<span>Duplicate text</span><span>Duplicate text</span>
		<label>Email<input aria-label="Email"></label><label>Email<input aria-label="Email"></label>
		<button style="display:none">Hidden only</button>`
	if _, err := Navigate(page, dataURL(html), "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	tests := []struct {
		target string
		match  string
	}{
		{`getByRole("button", { name: "Duplicate role" })`, "matched 2 visible elements"},
		{`getByText("Duplicate text")`, "matched 2 visible elements"},
		{`getByLabel("Email")`, "matched 2 visible elements"},
		{`getByRole("button", { name: "Hidden only" })`, "matched 0 visible elements"},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			if _, err := ResolveTarget(page, tt.target, nil); err == nil || !strings.Contains(err.Error(), tt.match) {
				t.Fatalf("ResolveTarget(%q) error = %v; want %q", tt.target, err, tt.match)
			}
		})
	}
}

func TestDropTargetDispatchesFilesAndData(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)
	path := t.TempDir() + "/note.txt"
	if err := os.WriteFile(path, []byte("hello drop"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	html := `<!doctype html><div id="drop-zone"></div><script>
	window.events = [];
	document.getElementById('drop-zone').addEventListener('dragenter', event => window.events.push(event.type));
	document.getElementById('drop-zone').addEventListener('dragover', event => window.events.push(event.type));
	document.getElementById('drop-zone').addEventListener('drop', event => {
	  window.events.push(event.type);
	  const file = event.dataTransfer.files[0];
	  window.file = file ? file.name + ':' + file.size + ':' + file.type : '';
	  window.text = event.dataTransfer.getData('text/plain');
	});
	</script>`
	if _, err := Navigate(page, dataURL(html), "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}

	if err := DropTarget(page, "#drop-zone", []string{path}, []DropData{{MIME: "text/plain", Value: "metadata"}}, nil); err != nil {
		t.Fatalf("drop target: %v", err)
	}
	gotEvents, err := EvalJS(page, `window.events.join(",")`, "", nil)
	if err != nil || gotEvents != "dragenter,dragover,drop" {
		t.Fatalf("drop events = %q, %v", gotEvents, err)
	}
	gotFile, err := EvalJS(page, `window.file`, "", nil)
	if err != nil || !strings.HasPrefix(gotFile, "note.txt:10:text/plain") {
		t.Fatalf("drop file = %q, %v", gotFile, err)
	}
	gotText, err := EvalJS(page, `window.text`, "", nil)
	if err != nil || gotText != "metadata" {
		t.Fatalf("drop data = %q, %v", gotText, err)
	}
}

func TestDropTargetDispatchesDataWithoutFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)
	html := `<!doctype html><div id="drop-zone"></div><script>
	document.getElementById('drop-zone').addEventListener('drop', event => {
	  window.fileCount = event.dataTransfer.files.length;
	  window.text = event.dataTransfer.getData('text/plain');
	});
	</script>`
	if _, err := Navigate(page, dataURL(html), "load"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	if err := DropTarget(page, "#drop-zone", nil, []DropData{{MIME: "text/plain", Value: "data only"}}, nil); err != nil {
		t.Fatalf("drop target: %v", err)
	}
	got, err := EvalJS(page, `window.fileCount + ":" + window.text`, "", nil)
	if err != nil || got != "0:data only" {
		t.Fatalf("drop data-only result = %q, %v", got, err)
	}
}

func TestDropTargetRejectsOversizedFileBeforeReading(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)
	if _, err := Navigate(page, dataURL(`<!doctype html><div id="drop-zone"></div>`), "load"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "large.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxDropFileBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := DropTarget(page, "#drop-zone", []string{path}, nil, nil); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("oversized drop error = %v", err)
	}
}

func TestDropTargetValidatesTargetBeforeFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Chrome")
	}
	_, page := newIsolatedPage(t)
	if _, err := Navigate(page, dataURL(`<!doctype html><div></div>`), "load"); err != nil {
		t.Fatal(err)
	}
	err := DropTarget(page, "#missing", []string{"/definitely/missing/file"}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), `matched 0`) {
		t.Fatalf("target should fail before file stat: %v", err)
	}
}
