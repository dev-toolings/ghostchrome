package engine

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCmdlineIsSessionServe(t *testing.T) {
	e := SessionEntry{Name: "work", Port: 9222, Profile: "work"}
	cases := []struct {
		name string
		cmd  string
		want bool
	}{
		{"exact match (space form)", "/usr/local/bin/ghostchrome serve --port 9222 --user-profile work --headless=true", true},
		{"renamed binary still matches", "/home/kev/.local/bin/gc serve --port 9222 --user-profile work", true},
		{"exact match (equals form)", "ghostchrome serve --port=9222 --user-profile=work", true},
		{"substring port must NOT match", "ghostchrome serve --port 92223 --user-profile work", false},
		{"different profile must NOT match", "ghostchrome serve --port 9222 --user-profile other", false},
		{"missing profile must NOT match", "ghostchrome serve --port 9222 --headless=true", false},
		{"not a serve must NOT match", "ghostchrome agent --port 9222 --user-profile work", false},
		{"unrelated process must NOT match", "/usr/bin/node /home/kev/server.js --port 9222", false},
		{"port as bare substring elsewhere must NOT match", "ghostchrome serve --port 7000 --user-profile work9222", false},
	}
	for _, c := range cases {
		if got := cmdlineIsSessionServe(c.cmd, e); got != c.want {
			t.Errorf("%s: cmdlineIsSessionServe(%q) = %v, want %v", c.name, c.cmd, got, c.want)
		}
	}
}

func TestSuppressDaemonEnvStripsSessionIdentity(t *testing.T) {
	// A spawned serve must not inherit any signal that would make it re-derive
	// a session name and acquire yet another serve (the recursive fork bomb).
	in := []string{
		"PATH=/usr/bin",
		"GHOSTCHROME_DAEMON=1",
		"GHOSTCHROME_NO_DAEMON=0",
		"GHOSTCHROME_SESSION=iautos-1783290111247382717",
		"PLAYWRIGHT_CLI_SESSION=iautos-1783290111247382717",
		"HOME=/home/kev",
	}
	out := suppressDaemonEnv(in)

	var noDaemon int
	for _, e := range out {
		switch {
		case strings.HasPrefix(e, "GHOSTCHROME_DAEMON="):
			t.Errorf("GHOSTCHROME_DAEMON leaked into child env: %q", e)
		case strings.HasPrefix(e, "GHOSTCHROME_SESSION="):
			t.Errorf("GHOSTCHROME_SESSION leaked into child env: %q", e)
		case strings.HasPrefix(e, "PLAYWRIGHT_CLI_SESSION="):
			t.Errorf("PLAYWRIGHT_CLI_SESSION leaked into child env: %q", e)
		case e == "GHOSTCHROME_NO_DAEMON=1":
			noDaemon++
		case strings.HasPrefix(e, "GHOSTCHROME_NO_DAEMON="):
			t.Errorf("stale GHOSTCHROME_NO_DAEMON value survived: %q", e)
		}
	}
	if noDaemon != 1 {
		t.Errorf("expected exactly one GHOSTCHROME_NO_DAEMON=1, got %d", noDaemon)
	}
	// Unrelated env must be preserved.
	var hasPath, hasHome bool
	for _, e := range out {
		if e == "PATH=/usr/bin" {
			hasPath = true
		}
		if e == "HOME=/home/kev" {
			hasHome = true
		}
	}
	if !hasPath || !hasHome {
		t.Errorf("unrelated env not preserved: PATH=%v HOME=%v", hasPath, hasHome)
	}
}

func TestSessionAliveHonorsStoredWebSocketEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"webSocketDebuggerUrl": "ws://127.0.0.1:9222/devtools/browser/live",
		})
	}))
	defer srv.Close()
	port := parseTestPort(t, srv.URL)

	ws, alive := sessionAlive(SessionEntry{
		Name:  "default",
		Port:  port,
		WSURL: "ws://127.0.0.1:9222/devtools/browser/live",
	})
	if !alive {
		t.Fatal("expected session to be alive")
	}
	if ws != "ws://127.0.0.1:9222/devtools/browser/live" {
		t.Fatalf("unexpected websocket %q", ws)
	}
}

func TestSessionAliveRejectsMismatchedWebSocketEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"webSocketDebuggerUrl": "ws://127.0.0.1:9222/devtools/browser/foreign",
		})
	}))
	defer srv.Close()
	port := parseTestPort(t, srv.URL)

	_, alive := sessionAlive(SessionEntry{
		Name:  "default",
		Port:  port,
		WSURL: "ws://127.0.0.1:9222/devtools/browser/live",
	})
	if alive {
		t.Fatal("expected session to be considered dead when ws endpoint mismatches")
	}
}

func TestSessionAliveWSOnlyDoesNotRequirePort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"webSocketDebuggerUrl": "ws://127.0.0.1:9222/devtools/browser/attached",
		})
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	ws := "ws://" + u.Host + "/devtools/browser/attached"
	got, alive := sessionAlive(SessionEntry{Name: "ext", Port: 0, WSURL: ws})
	if !alive {
		t.Fatal("expected attached session to be alive via /json/version")
	}
	if got != ws {
		t.Fatalf("unexpected websocket %q", got)
	}
}

func TestSaveSessionRegistryAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")
	reg := &sessionRegistry{Sessions: map[string]SessionEntry{"work": {Name: "work", Port: 1}}}
	if err := saveSessionRegistry(path, reg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "work") {
		t.Fatalf("saved registry missing session: %s", data)
	}
}

func parseTestPort(t *testing.T, rawURL string) int {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(u.Host, ":")
	port, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		t.Fatal(err)
	}
	return port
}

func TestSessionLeaseFresh(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if SessionLeaseFresh("work", time.Hour) {
		t.Fatal("missing lease should be stale")
	}
	TouchSessionLease("work")
	if !SessionLeaseFresh("work", time.Hour) {
		t.Fatal("fresh lease should be live")
	}
}
