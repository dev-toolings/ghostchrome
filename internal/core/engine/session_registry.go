package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// SessionEntry is one named, auto-managed Chrome in the session registry.
// A session is a long-lived `serve` process bound to a disk profile of the
// same name (so cookies persist), reused across CLI invocations.
type SessionEntry struct {
	Name       string `json:"name"`
	Port       int    `json:"port"`
	PID        int    `json:"pid"`
	WSURL      string `json:"ws_url"`
	Profile    string `json:"profile"`
	LaunchedAt string `json:"launched_at"`
}

// sessionRegistry is the on-disk map of name → SessionEntry.
type sessionRegistry struct {
	Sessions map[string]SessionEntry `json:"sessions"`
}

// SessionSpawnOpts carries the global flags propagated to a spawned serve.
type SessionSpawnOpts struct {
	Headless       bool
	Stealth        bool
	Proxy          string
	ProxyBypass    string
	ExecutablePath string
	ConfigPath     string
}

const DefaultSessionName = "default"

var ErrSessionNotFound = errors.New("session not found in registry")

var sessionNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func validateSessionName(name string) error {
	if name == "" {
		return fmt.Errorf("session name is empty")
	}
	if !sessionNameRe.MatchString(name) {
		return fmt.Errorf("invalid session name %q (allowed: letters, digits, '-', '_')", name)
	}
	return nil
}

func sessionRegistryPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	dir := filepath.Join(home, ".ghostchrome")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create .ghostchrome dir: %w", err)
	}
	return filepath.Join(dir, "sessions.json"), nil
}

func sessionDir(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".ghostchrome", "sessions", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func sessionLogPath(name string) (string, error) {
	dir, err := sessionDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "serve.log"), nil
}

func sessionLeasePath(name string) (string, error) {
	dir, err := sessionDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "lease"), nil
}

// TouchSessionLease records that a client is using the named session so the
// serve idle reaper will not kill an attached JSONL/MCP/CLI loop.
func TouchSessionLease(name string) {
	if err := validateSessionName(name); err != nil {
		return
	}
	path, err := sessionLeasePath(name)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o600)
}

func SessionLeaseFresh(name string, window time.Duration) bool {
	if window <= 0 || validateSessionName(name) != nil {
		return false
	}
	path, err := sessionLeasePath(name)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < window
}
func sessionSocketPath(name string) (string, error) {
	dir, err := sessionDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cdp.sock"), nil
}

func loadSessionRegistry() (*sessionRegistry, string, error) {
	path, err := sessionRegistryPath()
	if err != nil {
		return nil, "", err
	}
	reg := &sessionRegistry{Sessions: map[string]SessionEntry{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return reg, path, nil
		}
		return nil, path, fmt.Errorf("read sessions registry: %w", err)
	}
	if err := json.Unmarshal(data, reg); err != nil {
		return nil, path, fmt.Errorf("parse sessions registry: %w", err)
	}
	if reg.Sessions == nil {
		reg.Sessions = map[string]SessionEntry{}
	}
	return reg, path, nil
}

func saveSessionRegistry(path string, reg *sessionRegistry) error {
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".sessions-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func withSessionRegistry(fn func(reg *sessionRegistry, path string) error) error {
	path, err := sessionRegistryPath()
	if err != nil {
		return err
	}
	return lockContinuousLog(path, func() error {
		reg, _, err := loadSessionRegistry()
		if err != nil {
			return err
		}
		return fn(reg, path)
	})
}

func waitSessionDead(e SessionEntry, timeout time.Duration) {
	if e.PID <= 0 && e.Port == 0 {
		return
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, alive := sessionAlive(e); !alive {
			if e.PID <= 0 {
				return
			}
			if _, still := processCmdline(e.PID); !still {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// sessionAlive probes the session's CDP port. It returns the (fresh) browser
// WebSocket URL and true when Chrome answers, false otherwise. Liveness is
// authoritative via CDP rather than the stored PID: if our serve is gone the
// port stops answering.
func sessionAlive(e SessionEntry) (string, bool) {
	if e.WSURL != "" && e.Port == 0 {
		ws, err := ProbeCDPEndpoint(e.WSURL, 600*time.Millisecond)
		if err != nil || ws == "" {
			return "", false
		}
		return e.WSURL, true
	}
	ws, err := DiscoverCDP([]int{e.Port}, 600*time.Millisecond)
	if err != nil || ws == "" {
		return "", false
	}
	if e.WSURL != "" && ws != e.WSURL {
		return "", false
	}
	return ws, true
}

// cmdlineIsSessionServe reports whether a process command line is the serve
// that backs this session. Identity is the spawn fingerprint matched as EXACT
// tokens: the `serve` subcommand AND `--port <port>` AND `--user-profile
// <name>`. The exact (random) port + the session name make this unique to our
// process without depending on the binary's name (it may be renamed/symlinked).
func cmdlineIsSessionServe(cmd string, e SessionEntry) bool {
	fields := strings.Fields(cmd)
	serve := false
	port := strconv.Itoa(e.Port)
	portOK := false
	nameOK := false
	for i, f := range fields {
		if f == "serve" {
			serve = true
		}
		switch {
		case f == "--port" && i+1 < len(fields) && fields[i+1] == port:
			portOK = true
		case f == "--port="+port:
			portOK = true
		case f == "--user-profile" && i+1 < len(fields) && fields[i+1] == e.Name:
			nameOK = true
		case f == "--user-profile="+e.Name:
			nameOK = true
		}
	}
	return serve && portOK && nameOK
}

// killSessionProcess signals the session's serve process ONLY if the PID still
// belongs to THIS session's ghostchrome serve. Guards against the OS having
// recycled a dead serve's PID for an unrelated process — we must never signal
// an innocent process during respawn/prune/stop. processCmdline is provided
// per-platform (session_spawn_{unix,windows}.go).
func killSessionProcess(e SessionEntry) {
	if e.PID <= 0 {
		return
	}
	cmd, ok := processCmdline(e.PID)
	if !ok {
		return // gone or unverifiable — do not signal anything
	}
	if !cmdlineIsSessionServe(cmd, e) {
		return // PID reused by an unrelated process — leave it alone
	}
	_ = killPID(e.PID)
}

// suppressDaemonEnv ensures the spawned serve subprocess does NOT trigger the
// implicit-session daemon again (which would fork-bomb). It strips any legacy
// GHOSTCHROME_DAEMON var, strips the session-identity vars so the child serve
// cannot re-derive a session name from inherited env and acquire yet another
// serve, and injects GHOSTCHROME_NO_DAEMON=1.
func suppressDaemonEnv(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if strings.HasPrefix(e, "GHOSTCHROME_DAEMON=") ||
			strings.HasPrefix(e, "GHOSTCHROME_NO_DAEMON=") ||
			strings.HasPrefix(e, "GHOSTCHROME_SESSION=") ||
			strings.HasPrefix(e, "PLAYWRIGHT_CLI_SESSION=") {
			continue
		}
		out = append(out, e)
	}
	return append(out, "GHOSTCHROME_NO_DAEMON=1")
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// AcquireSession resolves a named session to a browser WebSocket URL. If the
// session is already running it is reused; otherwise a detached `serve`
// process is spawned, bound to the disk profile of the same name, and probed
// until its CDP endpoint is ready.
func AcquireSession(name string, opts SessionSpawnOpts) (string, error) {
	if err := validateSessionName(name); err != nil {
		return "", err
	}
	var ws string
	err := withSessionRegistry(func(reg *sessionRegistry, path string) error {
		if entry, ok := reg.Sessions[name]; ok {
			if existing, alive := sessionAlive(entry); alive {
				ws = existing
				return nil
			}
			killSessionProcess(entry)
			waitSessionDead(entry, 3*time.Second)
			delete(reg.Sessions, name)
		}

		if profileDir, err := ResolveProfileDir(name); err == nil {
			clearStaleProfileLock(profileDir)
		}

		port, err := freePort()
		if err != nil {
			return fmt.Errorf("allocate port: %w", err)
		}
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("locate ghostchrome binary: %w", err)
		}

		args := []string{
			"serve",
			"--port", strconv.Itoa(port),
			"--user-profile", name,
			fmt.Sprintf("--headless=%t", opts.Headless),
		}
		if opts.Stealth {
			args = append(args, "--stealth")
		}
		if opts.Proxy != "" {
			args = append(args, "--proxy", opts.Proxy)
		}
		if opts.ProxyBypass != "" {
			args = append(args, "--proxy-bypass", opts.ProxyBypass)
		}
		if opts.ConfigPath != "" {
			args = append(args, "--config", opts.ConfigPath)
		}

		logPath, err := sessionLogPath(name)
		if err != nil {
			return err
		}
		logFile, err := os.Create(logPath)
		if err != nil {
			return fmt.Errorf("open session log: %w", err)
		}

		cmd := exec.Command(exe, args...)
		env := suppressDaemonEnv(os.Environ())
		if opts.ExecutablePath != "" {
			env = append(env, "PLAYWRIGHT_MCP_EXECUTABLE_PATH="+opts.ExecutablePath)
		}
		cmd.Env = env
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		cmd.SysProcAttr = detachSysProcAttr()
		if err := cmd.Start(); err != nil {
			logFile.Close()
			return fmt.Errorf("spawn session serve: %w", err)
		}
		pid := cmd.Process.Pid
		_ = cmd.Process.Release()
		logFile.Close()

		ready, err := waitForCDP(port, 15*time.Second)
		if err != nil {
			_ = killPID(pid)
			return fmt.Errorf("session %q: chrome did not come up (see %s): %w", name, logPath, err)
		}
		if sock, serr := sessionSocketPath(name); serr == nil {
			_ = os.WriteFile(sock, []byte(ready+"\n"), 0o600)
		}

		reg.Sessions[name] = SessionEntry{
			Name:       name,
			Port:       port,
			PID:        pid,
			WSURL:      ready,
			Profile:    name,
			LaunchedAt: time.Now().UTC().Format(time.RFC3339),
		}
		if err := saveSessionRegistry(path, reg); err != nil {
			_ = killPID(pid)
			return fmt.Errorf("save sessions registry: %w", err)
		}
		ws = ready
		return nil
	})
	if err != nil {
		return "", err
	}
	return ws, nil
}

// DefaultSession returns the default attached/session WS URL when it exists
// and is alive. It does not spawn a new browser.
func DefaultSession() (string, bool) {
	reg, _, err := loadSessionRegistry()
	if err != nil {
		return "", false
	}
	entry, ok := reg.Sessions[DefaultSessionName]
	if !ok {
		return "", false
	}
	return sessionAlive(entry)
}

// ResolveSession returns the live browser WebSocket URL for an existing
// registered session. It never spawns a new browser.
func ResolveSession(name string) (SessionRegistryEntry, error) {
	if err := validateSessionName(name); err != nil {
		return SessionRegistryEntry{}, err
	}
	reg, _, err := loadSessionRegistry()
	if err != nil {
		return SessionRegistryEntry{}, err
	}
	entry, ok := reg.Sessions[name]
	if !ok {
		return SessionRegistryEntry{}, fmt.Errorf("session %q: %w", name, ErrSessionNotFound)
	}
	ws, alive := sessionAlive(entry)
	if !alive {
		return SessionRegistryEntry{}, fmt.Errorf("session %q is not alive", name)
	}
	return SessionRegistryEntry{
		Name:       name,
		Port:       entry.Port,
		PID:        entry.PID,
		WSURL:      ws,
		Profile:    entry.Profile,
		LaunchedAt: entry.LaunchedAt,
		Alive:      true,
	}, nil
}

// AttachSession registers an existing CDP endpoint as a named session. It does
// not spawn or own the browser process; StopSession removes the registry entry
// but will not terminate an external browser because PID/Port are zero.
func AttachSession(name string, wsURL string) (SessionEntry, error) {
	if err := validateSessionName(name); err != nil {
		return SessionEntry{}, err
	}
	if strings.TrimSpace(wsURL) == "" {
		return SessionEntry{}, fmt.Errorf("cdp endpoint is empty")
	}
	if _, err := ProbeCDPEndpoint(wsURL, time.Second); err != nil {
		return SessionEntry{}, fmt.Errorf("connect cdp endpoint: %w", err)
	}

	var entry SessionEntry
	err := withSessionRegistry(func(reg *sessionRegistry, path string) error {
		entry = SessionEntry{
			Name:       name,
			Port:       0,
			PID:        0,
			WSURL:      wsURL,
			Profile:    "",
			LaunchedAt: time.Now().UTC().Format(time.RFC3339),
		}
		reg.Sessions[name] = entry
		return saveSessionRegistry(path, reg)
	})
	if err != nil {
		return SessionEntry{}, err
	}
	return entry, nil
}

// waitForCDP polls the port until Chrome's /json/version answers or timeout.
func waitForCDP(port int, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ws, err := DiscoverCDP([]int{port}, 500*time.Millisecond); err == nil && ws != "" {
			return ws, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "", fmt.Errorf("timeout after %s", timeout)
}

// SessionRegistryEntry is the public-facing representation used by the CLI.
type SessionRegistryEntry struct {
	Name       string `json:"name"`
	Port       int    `json:"port"`
	PID        int    `json:"pid"`
	WSURL      string `json:"ws_url"`
	Profile    string `json:"profile"`
	LaunchedAt string `json:"launched_at,omitempty"`
	Alive      bool   `json:"alive"`
}

// AliveStr returns "yes" or "no".
func (e SessionRegistryEntry) AliveStr() string {
	if e.Alive {
		return "yes"
	}
	return "no"
}

// ListSessions returns every registry entry annotated with current liveness.
func ListSessions() ([]SessionRegistryEntry, error) {
	reg, _, err := loadSessionRegistry()
	if err != nil {
		return nil, err
	}
	out := make([]SessionRegistryEntry, 0, len(reg.Sessions))
	for name, e := range reg.Sessions {
		_, alive := sessionAlive(e)
		out = append(out, SessionRegistryEntry{
			Name:       name,
			Port:       e.Port,
			PID:        e.PID,
			WSURL:      e.WSURL,
			Profile:    e.Profile,
			LaunchedAt: e.LaunchedAt,
			Alive:      alive,
		})
	}
	return out, nil
}

// StopSession terminates the named session's process and removes it from the
// registry (best-effort: the process may already be gone).
func StopSession(name string) error {
	return withSessionRegistry(func(reg *sessionRegistry, path string) error {
		entry, ok := reg.Sessions[name]
		if !ok {
			return fmt.Errorf("session %q: %w", name, ErrSessionNotFound)
		}
		killSessionProcess(entry)
		waitSessionDead(entry, 5*time.Second)
		delete(reg.Sessions, name)
		if dir, err := sessionDir(name); err == nil {
			_ = os.RemoveAll(dir)
		}
		return saveSessionRegistry(path, reg)
	})
}

// PruneSessions removes registry entries whose Chrome is no longer reachable,
// killing any lingering serve process and its log. Returns the count pruned.
func PruneSessions() (int, error) {
	n := 0
	err := withSessionRegistry(func(reg *sessionRegistry, path string) error {
		for name, entry := range reg.Sessions {
			if _, alive := sessionAlive(entry); alive {
				continue
			}
			killSessionProcess(entry)
			waitSessionDead(entry, 2*time.Second)
			if dir, lerr := sessionDir(name); lerr == nil {
				_ = os.RemoveAll(dir)
			}
			delete(reg.Sessions, name)
			n++
		}
		if n > 0 {
			return saveSessionRegistry(path, reg)
		}
		return nil
	})
	return n, err
}

// KillAllSessions stops every registered session.
func KillAllSessions() (int, error) {
	n := 0
	err := withSessionRegistry(func(reg *sessionRegistry, path string) error {
		for name, entry := range reg.Sessions {
			killSessionProcess(entry)
			waitSessionDead(entry, 2*time.Second)
			if dir, lerr := sessionDir(name); lerr == nil {
				_ = os.RemoveAll(dir)
			}
			n++
		}
		reg.Sessions = map[string]SessionEntry{}
		return saveSessionRegistry(path, reg)
	})
	return n, err
}
