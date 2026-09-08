package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/dev-toolings/ghostchrome/internal/core/engine"
	"github.com/spf13/cobra"
)

var (
	flagRouteStatus       int
	flagRouteBody         string
	flagRouteContentType  string
	flagRouteHeaders      []string
	flagRouteRemoveHeader string
	flagRouteWait         int
	flagRouteID           string
)

var routeCompatCmd = &cobra.Command{
	Use:   "route <pattern>",
	Short: "Mock or block requests matching a URL pattern",
	Long: `Install a persistent route in the active ghostchrome session.
By default this spawns a small background worker and returns, matching the
Playwright CLI workflow where route is followed by reload/snapshot/unroute.
Use --wait for a bounded foreground route.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if flagRouteWait > 0 {
			runRouteActive(args[0])
			return
		}
		startPersistentRoute(args[0])
	},
}

var routeWorkerCmd = &cobra.Command{
	Use:    "route-worker <pattern>",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		runRouteActive(args[0])
	},
}

var routeListCmd = &cobra.Command{
	Use:   "route-list",
	Short: "List persistent routes",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		reg, err := loadRouteRegistry()
		if err != nil {
			exitErr("route-list", err)
		}
		type routeListResult struct {
			Routes []routeEntry `json:"routes"`
			Count  int          `json:"count"`
		}
		var lines []string
		for _, route := range reg.Routes {
			lines = append(lines, fmt.Sprintf("%s pid=%d pattern=%q session=%q", route.ID, route.PID, route.Pattern, route.Session))
		}
		if len(lines) == 0 {
			lines = append(lines, "No routes")
		}
		output(routeListResult{Routes: reg.Routes, Count: len(reg.Routes)}, strings.Join(lines, "\n"))
	},
}

var unrouteCmd = &cobra.Command{
	Use:   "unroute [pattern|id]",
	Short: "Remove persistent routes",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		filter := ""
		if len(args) > 0 {
			filter = args[0]
		}
		removed, err := removeRoutes(filter)
		if err != nil {
			exitErr("unroute", err)
		}
		type unrouteResult struct {
			Removed []routeEntry `json:"removed"`
			Count   int          `json:"count"`
		}
		output(unrouteResult{Removed: removed, Count: len(removed)}, fmt.Sprintf("removed %d route(s)", len(removed)))
	},
}

func runRouteActive(pattern string) {
	spec := buildRouteSpec(pattern)
	b, _ := openPage()
	defer b.Close()

	session, err := engine.StartIntercept(b.RodBrowser(), spec)
	if err != nil {
		exitErr("route", err)
	}

	fmt.Fprintf(os.Stderr, "[route] active pattern=%q status=%d. Ctrl-C to stop.\n", pattern, flagRouteStatus)
	waitForRouteStop(session)

	blocked, fulfilled, passed := session.Stats().Snapshot()
	type routeResult struct {
		Action    string `json:"action"`
		ID        string `json:"id,omitempty"`
		Pattern   string `json:"pattern"`
		Blocked   int    `json:"blocked"`
		Fulfilled int    `json:"fulfilled"`
		Passed    int    `json:"passed"`
	}
	text := fmt.Sprintf("[route] stopped — fulfilled=%d blocked=%d passed=%d", fulfilled, blocked, passed)
	output(routeResult{Action: "route", ID: flagRouteID, Pattern: pattern, Blocked: blocked, Fulfilled: fulfilled, Passed: passed}, text)
}

func buildRouteSpec(pattern string) engine.InterceptSpec {
	headers, err := parseRouteHeaders(flagRouteHeaders)
	if err != nil {
		exitErr("route", err)
	}
	body, err := engine.LoadFulfillBody(flagRouteBody)
	if err != nil {
		exitErr("route", err)
	}
	return engine.InterceptSpec{
		FulfillPattern:       pattern,
		FulfillBody:          body,
		FulfillStatus:        flagRouteStatus,
		FulfillContentType:   flagRouteContentType,
		FulfillHeaders:       headers,
		RemoveRequestHeaders: parseRouteRemoveHeaders(flagRouteRemoveHeader),
	}
}

func waitForRouteStop(session *engine.InterceptSession) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sig)

	if flagRouteWait > 0 {
		select {
		case <-sig:
		case <-time.After(time.Duration(flagRouteWait) * time.Second):
		}
	} else {
		<-sig
	}
	if err := session.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "[route] stop: %v\n", err)
	}
}

type routeEntry struct {
	ID            string            `json:"id"`
	Pattern       string            `json:"pattern"`
	PID           int               `json:"pid"`
	Session       string            `json:"session,omitempty"`
	ConnectURL    string            `json:"connect_url,omitempty"`
	Status        int               `json:"status"`
	ContentType   string            `json:"content_type,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
	RemoveHeaders []string          `json:"remove_headers,omitempty"`
	CreatedAt     string            `json:"created_at"`
	LogPath       string            `json:"log_path,omitempty"`
}

type routeRegistry struct {
	Routes []routeEntry `json:"routes"`
}

func startPersistentRoute(pattern string) {
	headers, err := parseRouteHeaders(flagRouteHeaders)
	if err != nil {
		exitErr("route", err)
	}
	removeHeaders := parseRouteRemoveHeaders(flagRouteRemoveHeader)
	sessionName, connectArg, err := resolveRouteTarget()
	if err != nil {
		exitErr("route", err)
	}
	exe, err := os.Executable()
	if err != nil {
		exitErr("route", err)
	}
	id := fmt.Sprintf("r%d", time.Now().UnixNano())
	logPath, err := routeLogPath(id)
	if err != nil {
		exitErr("route", err)
	}
	logFile, err := os.Create(logPath)
	if err != nil {
		exitErr("route", err)
	}

	childArgs := []string{"route-worker", pattern, "--route-id", id, "--status", fmt.Sprintf("%d", flagRouteStatus)}
	if flagRouteBody != "" {
		childArgs = append(childArgs, "--body", flagRouteBody)
	}
	if flagRouteContentType != "" {
		childArgs = append(childArgs, "--content-type", flagRouteContentType)
	}
	for _, header := range flagRouteHeaders {
		childArgs = append(childArgs, "--header", header)
	}
	if flagRouteRemoveHeader != "" {
		childArgs = append(childArgs, "--remove-header", flagRouteRemoveHeader)
	}
	if sessionName != "" {
		childArgs = append(childArgs, "-s", sessionName)
	} else {
		childArgs = append(childArgs, "--connect", connectArg)
	}

	child := exec.Command(exe, childArgs...)
	child.Stdout = logFile
	child.Stderr = logFile
	child.SysProcAttr = engine.DetachSysProcAttr()
	if err := child.Start(); err != nil {
		logFile.Close()
		exitErr("route", err)
	}
	pid := child.Process.Pid
	_ = child.Process.Release()
	logFile.Close()

	entry := routeEntry{
		ID:            id,
		Pattern:       pattern,
		PID:           pid,
		Session:       sessionName,
		ConnectURL:    connectArg,
		Status:        flagRouteStatus,
		ContentType:   flagRouteContentType,
		Headers:       headers,
		RemoveHeaders: removeHeaders,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		LogPath:       logPath,
	}
	if err := registerRoute(entry); err != nil {
		_ = engine.KillProcess(pid)
		exitErr("route", err)
	}
	output(entry, fmt.Sprintf("route %s active for %q", id, pattern))
}

func resolveRouteTarget() (sessionName string, connectURL string, err error) {
	if flagSession != "" {
		return flagSession, "", nil
	}
	if env := sessionNameFromEnv(); env != "" {
		return env, "", nil
	}
	if flagConnect != "" {
		return "", flagConnect, nil
	}
	if _, ok := engine.DefaultSession(); ok {
		return engine.DefaultSessionName, "", nil
	}
	return "", "", fmt.Errorf("persistent route requires -s/--session, --connect, or an attached default session")
}

func routeRegistryPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".ghostchrome")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "routes.json"), nil
}

func routeLogPath(id string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".ghostchrome", "routes")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, id+".log"), nil
}

func loadRouteRegistry() (*routeRegistry, error) {
	path, err := routeRegistryPath()
	if err != nil {
		return nil, err
	}
	reg := &routeRegistry{}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return reg, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, reg); err != nil {
		return nil, err
	}
	return reg, nil
}

func saveRouteRegistry(reg *routeRegistry) error {
	path, err := routeRegistryPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func registerRoute(entry routeEntry) error {
	reg, err := loadRouteRegistry()
	if err != nil {
		return err
	}
	reg.Routes = append(reg.Routes, entry)
	return saveRouteRegistry(reg)
}

func removeRoutes(filter string) ([]routeEntry, error) {
	reg, err := loadRouteRegistry()
	if err != nil {
		return nil, err
	}
	var kept []routeEntry
	var removed []routeEntry
	for _, route := range reg.Routes {
		match := filter == "" || route.ID == filter || route.Pattern == filter
		if match {
			_ = engine.KillProcess(route.PID)
			if route.LogPath != "" {
				_ = os.Remove(route.LogPath)
			}
			removed = append(removed, route)
			continue
		}
		kept = append(kept, route)
	}
	reg.Routes = kept
	if err := saveRouteRegistry(reg); err != nil {
		return removed, err
	}
	return removed, nil
}

func parseRouteHeaders(raw []string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	headers := make(map[string]string, len(raw))
	for _, pair := range raw {
		name, value, ok := strings.Cut(pair, ":")
		if !ok {
			name, value, ok = strings.Cut(pair, "=")
		}
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if !ok || name == "" {
			return nil, fmt.Errorf("--header expects name:value or name=value, got %q", pair)
		}
		headers[name] = value
	}
	return headers, nil
}

func parseRouteRemoveHeaders(raw string) []string {
	return engine.ParseBlockList(raw)
}

func init() {
	registerRouteFlags(routeCompatCmd, true)
	registerRouteFlags(routeWorkerCmd, false)
	routeWorkerCmd.Flags().StringVar(&flagRouteID, "route-id", "", "Route registry ID")
	rootCmd.AddCommand(routeCompatCmd, routeWorkerCmd, routeListCmd, unrouteCmd)
}

func registerRouteFlags(cmd *cobra.Command, includeWait bool) {
	cmd.Flags().IntVar(&flagRouteStatus, "status", 200, "HTTP status code")
	cmd.Flags().StringVar(&flagRouteBody, "body", "", "Response body (inline string or @path)")
	cmd.Flags().StringVar(&flagRouteContentType, "content-type", "", "Content-Type response header")
	cmd.Flags().StringArrayVar(&flagRouteHeaders, "header", nil, "Additional response header name:value (repeatable)")
	cmd.Flags().StringVar(&flagRouteRemoveHeader, "remove-header", "", "Request headers to strip before continuing the request (comma-separated)")
	if includeWait {
		cmd.Flags().IntVar(&flagRouteWait, "wait", 0, "Seconds to keep route active in the foreground (0 = persist in background)")
	}
}
