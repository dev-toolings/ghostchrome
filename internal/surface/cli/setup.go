package cli

// The setup command owns the user-facing installation state.  It deliberately
// lives in cmd (rather than the browser engine) because installation is a
// local-filesystem concern and must not create a Chrome process.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/spf13/cobra"
)

const (
	setupManifestSchema = 1
	setupDefaultClients = "claude,codex,grok"
	setupCLIName        = "ghostchrome"
	setupMCPName        = "ghostchrome-mcp"
	setupSkillName      = "ghostchrome"
)

type setupMode string

const (
	setupModeCLI setupMode = "cli"
	setupModeMCP setupMode = "mcp"
)

var (
	setupModeFlag     string
	setupClientsFlag  string
	setupSwitchTo     string
	setupSwitchYes    bool
	setupUninstallYes bool
	setupPurgeData    bool
	setupDoctorStrict bool
)

// setupManifest is intentionally small and forward-compatible.  Paths are
// absolute so a client can consume the file without having to expand `~`.
// managed_files contains files Ghostchrome may update/remove; it never grants
// permission to remove arbitrary user files.
type setupManifest struct {
	SchemaVersion int       `json:"schema_version"`
	Mode          setupMode `json:"mode"`
	Version       string    `json:"version"`
	InstallRoot   string    `json:"install_root"`
	Binary        string    `json:"binary"`
	Clients       []string  `json:"clients"`
	SkillSHA256   string    `json:"skill_sha256,omitempty"`
	ManagedFiles  []string  `json:"managed_files"`
	InstalledAt   string    `json:"installed_at"`
}

type setupPaths struct {
	Home         string
	Root         string
	BinDir       string
	Manifest     string
	Lock         string
	CLI          string
	MCP          string
	ClaudeConfig string
	CodexConfig  string
	GrokConfig   string
}

func pathsForHome(home string) setupPaths {
	root := filepath.Join(home, ".ghostchrome")
	return setupPaths{
		Home:         home,
		Root:         root,
		BinDir:       filepath.Join(root, "bin"),
		Manifest:     filepath.Join(root, "install.json"),
		Lock:         filepath.Join(root, "setup.lock"),
		CLI:          filepath.Join(root, "bin", setupCLIName+binarySuffix()),
		MCP:          filepath.Join(root, "bin", setupMCPName+binarySuffix()),
		ClaudeConfig: filepath.Join(home, ".claude.json"),
		CodexConfig:  filepath.Join(home, ".codex", "config.toml"),
		GrokConfig:   filepath.Join(home, ".grok", "config.toml"),
	}
}

func setupHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	if home == "" || !filepath.IsAbs(home) {
		return "", fmt.Errorf("resolve home directory: invalid path %q", home)
	}
	return filepath.Clean(home), nil
}

func parseSetupMode(value string) (setupMode, error) {
	switch setupMode(strings.ToLower(strings.TrimSpace(value))) {
	case setupModeCLI:
		return setupModeCLI, nil
	case setupModeMCP:
		return setupModeMCP, nil
	default:
		return "", fmt.Errorf("invalid setup mode %q: use cli or mcp", value)
	}
}

func parseSetupClients(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		value = setupDefaultClients
	}
	allowed := map[string]bool{"claude": true, "codex": true, "grok": true}
	seen := make(map[string]bool)
	for _, raw := range strings.Split(value, ",") {
		client := strings.ToLower(strings.TrimSpace(raw))
		if client == "" {
			continue
		}
		if !allowed[client] {
			return nil, fmt.Errorf("unsupported client %q: use claude, codex, or grok", client)
		}
		seen[client] = true
	}
	if len(seen) == 0 {
		return nil, errors.New("at least one client is required")
	}
	clients := make([]string, 0, len(seen))
	for client := range seen {
		clients = append(clients, client)
	}
	sort.Strings(clients)
	return clients, nil
}

func readSetupManifest(paths setupPaths) (*setupManifest, error) {
	data, err := os.ReadFile(paths.Manifest)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read setup manifest: %w", err)
	}
	var manifest setupManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse setup manifest %s: %w", paths.Manifest, err)
	}
	if manifest.SchemaVersion != setupManifestSchema {
		return nil, fmt.Errorf("unsupported setup manifest schema %d (expected %d)", manifest.SchemaVersion, setupManifestSchema)
	}
	if _, err := parseSetupMode(string(manifest.Mode)); err != nil {
		return nil, fmt.Errorf("invalid setup manifest: %w", err)
	}
	if manifest.InstallRoot == "" || manifest.Binary == "" {
		return nil, errors.New("invalid setup manifest: install_root and binary are required")
	}
	manifest.InstallRoot = expandSetupPath(paths.Home, manifest.InstallRoot)
	manifest.Binary = expandSetupPath(paths.Home, manifest.Binary)
	for index, managedPath := range manifest.ManagedFiles {
		manifest.ManagedFiles[index] = expandSetupPath(paths.Home, managedPath)
	}
	if filepath.Clean(manifest.InstallRoot) != filepath.Clean(paths.Root) {
		return nil, fmt.Errorf("invalid setup manifest: install_root %q is outside the active home", manifest.InstallRoot)
	}
	if filepath.Clean(manifest.Binary) != filepath.Clean(paths.CLI) && filepath.Clean(manifest.Binary) != filepath.Clean(paths.MCP) {
		// A system install may deliberately keep the artifact in /usr/local/bin;
		// only the two canonical Ghostchrome basenames are accepted outside the
		// state root, never an arbitrary path supplied by a manifest.
		base := filepath.Base(manifest.Binary)
		if base != setupCLIName && base != setupMCPName && base != setupCLIName+binarySuffix() && base != setupMCPName+binarySuffix() {
			return nil, fmt.Errorf("invalid setup manifest: unsupported binary path %q", manifest.Binary)
		}
	}
	for _, managedPath := range manifest.ManagedFiles {
		if !setupManifestPathAllowed(paths, &manifest, managedPath) {
			return nil, fmt.Errorf("invalid setup manifest: managed path %q is outside Ghostchrome locations", managedPath)
		}
	}
	return &manifest, nil
}

func expandSetupPath(home, path string) string {
	path = strings.TrimSpace(path)
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		return filepath.Join(home, path[2:])
	}
	return filepath.Clean(path)
}

func setupManifestPathAllowed(paths setupPaths, manifest *setupManifest, candidate string) bool {
	candidate = filepath.Clean(candidate)
	if candidate == filepath.Clean(manifest.Binary) || candidate == filepath.Clean(paths.Manifest) || candidate == filepath.Clean(paths.Lock) {
		return true
	}
	for _, known := range []string{paths.ClaudeConfig, paths.CodexConfig, paths.GrokConfig} {
		if candidate == filepath.Clean(known) {
			return true
		}
	}
	for _, client := range []string{"claude", "codex", "grok"} {
		root := filepath.Dir(setupSkillPath(paths.Home, client))
		if isSetupPathWithin(root, candidate) {
			return true
		}
	}
	return false
}

func isSetupPathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil || rel == "." {
		return err == nil && rel == "."
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func writeSetupManifest(paths setupPaths, manifest *setupManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode setup manifest: %w", err)
	}
	return writeSetupAtomic(paths.Manifest, append(data, '\n'), 0o600)
}

func writeSetupAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to write through symlink %s", path)
	}
	tmp, err := os.CreateTemp(dir, ".ghostchrome-atomic-")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temporary file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	removeTemp = false
	return nil
}

func setupFileHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func setupSkillContent() []byte {
	return []byte(embeddedSkills[setupSkillName])
}

// embeddedSkillFiles is an optional complete skill bundle.  The legacy
// SetEmbeddedSkill API still registers SKILL.md; release entrypoints can call
// SetEmbeddedSkillFile for references, examples, and validation scripts.
var embeddedSkillFiles = map[string]string{}

// SetEmbeddedSkillFile registers one path from the complete embedded skill
// tree. Paths are relative to the skill directory and must not escape it.
func SetEmbeddedSkillFile(path, content string) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return
	}
	embeddedSkillFiles[path] = content
}

func setupSkillBundle() map[string][]byte {
	bundle := make(map[string][]byte)
	for path, content := range embeddedSkillFiles {
		bundle[path] = []byte(content)
	}
	if _, ok := bundle["SKILL.md"]; !ok {
		if content := setupSkillContent(); len(content) > 0 {
			bundle["SKILL.md"] = content
		}
	}
	return bundle
}

func setupSkillPath(home, client string) string {
	return setupSkillFilePath(home, client, "SKILL.md")
}

func setupSkillFilePath(home, client, relative string) string {
	var base string
	switch client {
	case "claude":
		base = filepath.Join(home, ".claude", "skills")
	case "codex":
		base = filepath.Join(home, ".codex", "skills")
	case "grok":
		base = filepath.Join(home, ".grok", "skills")
	}
	return filepath.Join(base, setupSkillName, relative)
}

func setupSkillFiles(home string, clients []string) []string {
	bundle := setupSkillBundle()
	paths := make([]string, 0, len(clients)*len(bundle))
	for _, client := range clients {
		for relative := range bundle {
			paths = append(paths, setupSkillFilePath(home, client, relative))
		}
	}
	sort.Strings(paths)
	return paths
}

func setupCheckUnmanagedFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to manage symlink %s", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to manage non-regular file %s", path)
	}
	return nil
}

func setupSkillConflict(path string, content []byte, managed bool, force bool) error {
	if err := setupCheckUnmanagedFile(path); err != nil {
		return err
	}
	old, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if string(old) == string(content) {
		return nil
	}
	if managed || force {
		return nil
	}
	return fmt.Errorf("skill conflict at %s; refusing to overwrite an unmanaged skill", path)
}

func installSetupSkills(home string, clients []string, managed map[string]bool, force bool) (string, []string, error) {
	bundle := setupSkillBundle()
	content := bundle["SKILL.md"]
	if len(content) == 0 {
		return "", nil, errors.New("this build does not contain the Ghostchrome skill")
	}
	hash := setupFileHash(content)
	paths := setupSkillFiles(home, clients)
	for _, client := range clients {
		for relative, fileContent := range bundle {
			path := setupSkillFilePath(home, client, relative)
			if err := setupSkillConflict(path, fileContent, managed[path], force); err != nil {
				return "", nil, err
			}
		}
	}
	for _, client := range clients {
		for relative, fileContent := range bundle {
			path := setupSkillFilePath(home, client, relative)
			mode := os.FileMode(0o644)
			// The bundled validator executes shell examples directly. Preserve
			// execution for every authored shell asset, not only scripts/:
			// examples/cli-flow.sh is part of the installed skill contract too.
			if strings.HasSuffix(strings.ToLower(relative), ".sh") {
				mode = 0o755
			}
			if err := writeSetupAtomic(path, fileContent, mode); err != nil {
				return "", nil, fmt.Errorf("install skill %s: %w", path, err)
			}
		}
	}
	return hash, paths, nil
}

func setupSourceBinary(mode setupMode) (string, error) {
	if override := strings.TrimSpace(os.Getenv("GHOSTCHROME_SETUP_SOURCE")); override != "" {
		if err := setupCheckExecutable(override); err != nil {
			return "", fmt.Errorf("GHOSTCHROME_SETUP_SOURCE: %w", err)
		}
		return override, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate current executable: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("resolve current executable: %w", err)
	}
	if mode == setupModeCLI {
		if err := setupCheckExecutable(exe); err != nil {
			return "", err
		}
		return exe, nil
	}
	// A release contains both artifacts beside one another.  A setup command
	// invoked from the CLI therefore finds the standalone MCP artifact without
	// ever installing the CLI binary as a disguised MCP server.
	candidates := []string{
		filepath.Join(filepath.Dir(exe), setupMCPName),
		filepath.Join(filepath.Dir(exe), setupMCPName+binarySuffix()),
	}
	if path, lookErr := exec.LookPath(setupMCPName); lookErr == nil {
		candidates = append(candidates, path)
	}
	if path, lookErr := exec.LookPath(setupMCPName + binarySuffix()); lookErr == nil {
		candidates = append(candidates, path)
	}
	for _, candidate := range candidates {
		if setupCheckExecutable(candidate) == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("standalone %s binary not found beside %s; download the matching release artifact or set GHOSTCHROME_SETUP_SOURCE", setupMCPName, exe)
}

func binarySuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func setupCheckExecutable(path string) error {
	if err := setupCheckUnmanagedFile(path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("binary %s: %w", path, err)
	}
	// Windows does not expose Unix execute bits through os.FileMode. A regular
	// file with the platform executable suffix is the meaningful preflight.
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return fmt.Errorf("binary %s is not executable", path)
	}
	if runtime.GOOS == "windows" && !info.Mode().IsRegular() {
		return fmt.Errorf("binary %s is not a regular file", path)
	}
	return nil
}

func setupCopyBinary(source, destination string, managed bool) error {
	if filepath.Clean(source) == filepath.Clean(destination) {
		return setupCheckExecutable(source)
	}
	if err := setupCheckUnmanagedFile(destination); err != nil {
		return err
	}
	if _, err := os.Stat(destination); err == nil && !managed {
		return fmt.Errorf("binary conflict at %s; refusing to overwrite an unmanaged file", destination)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read source binary %s: %w", source, err)
	}
	if err := writeSetupAtomic(destination, data, 0o755); err != nil {
		return fmt.Errorf("install binary %s: %w", destination, err)
	}
	return nil
}

func configPathForClient(paths setupPaths, client string) string {
	switch client {
	case "claude":
		return paths.ClaudeConfig
	case "codex":
		return paths.CodexConfig
	case "grok":
		return paths.GrokConfig
	default:
		return ""
	}
}

type mcpConfigState struct {
	Present bool
	Command string
	Legacy  bool
	Managed bool
}

func knownGhostchromeCommand(command string) bool {
	command = strings.ToLower(strings.TrimSpace(command))
	base := strings.ToLower(filepath.Base(command))
	return strings.Contains(base, "ghostchrome")
}

func legacyGhostchromeCommand(command string, args []string) bool {
	if !knownGhostchromeCommand(command) {
		return false
	}
	for _, arg := range args {
		if strings.EqualFold(strings.TrimSpace(arg), "mcp") {
			return true
		}
	}
	return strings.HasSuffix(strings.ToLower(filepath.Base(command)), setupMCPName)
}

func jsonMCPState(path string) (map[string]any, mcpConfigState, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, mcpConfigState{}, nil
	}
	if err != nil {
		return nil, mcpConfigState{}, err
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, mcpConfigState{}, fmt.Errorf("parse %s: %w", path, err)
	}
	state := mcpConfigState{}
	servers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		return root, state, nil
	}
	entry, ok := servers[setupSkillName].(map[string]any)
	if !ok {
		if _, exists := servers[setupSkillName]; exists {
			// A key with an unexpected shape is still occupied. Treat it as a
			// foreign entry rather than silently replacing user configuration.
			state.Present = true
		}
		return root, state, nil
	}
	state.Present = true
	if command, ok := entry["command"].(string); ok {
		state.Command = command
	}
	var args []string
	if rawArgs, ok := entry["args"].([]any); ok {
		for _, raw := range rawArgs {
			if arg, ok := raw.(string); ok {
				args = append(args, arg)
			}
		}
	}
	state.Legacy = legacyGhostchromeCommand(state.Command, args)
	return root, state, nil
}

func setupMCPJSON(path, command string, force, remove bool) (mcpConfigState, error) {
	if err := setupCheckUnmanagedFile(path); err != nil {
		return mcpConfigState{}, err
	}
	root, state, err := jsonMCPState(path)
	if err != nil {
		return state, err
	}
	if state.Present && !state.Legacy && !state.Managed && !force {
		return state, fmt.Errorf("MCP config conflict at %s; refusing to overwrite a non-Ghostchrome server", path)
	}
	if state.Present && !remove && !force && !state.Legacy && state.Command != "" && filepath.Clean(state.Command) != filepath.Clean(command) {
		return state, fmt.Errorf("MCP config conflict at %s; refusing to overwrite an unmanaged Ghostchrome registration", path)
	}
	if root == nil {
		root = map[string]any{}
	}
	servers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		if existing, exists := root["mcpServers"]; exists && existing != nil {
			return state, fmt.Errorf("MCP config %s has an invalid mcpServers object", path)
		}
		servers = map[string]any{}
		root["mcpServers"] = servers
	}
	if remove {
		delete(servers, setupSkillName)
	} else {
		servers[setupSkillName] = map[string]any{
			"type":    "stdio",
			"command": command,
			"args":    []string{"--stealth"},
			"env":     map[string]string{},
		}
	}
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return state, err
	}
	if err := writeSetupAtomic(path, append(data, '\n'), 0o600); err != nil {
		return state, err
	}
	return state, nil
}

func tomlMCPState(path string) (mcpConfigState, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return mcpConfigState{}, nil
	}
	if err != nil {
		return mcpConfigState{}, err
	}
	state := mcpConfigState{}
	inSection := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section := strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]")
			inSection = section == "mcp_servers."+setupSkillName || strings.HasPrefix(section, "mcp_servers."+setupSkillName+".")
			if inSection {
				state.Present = true
			}
			continue
		}
		if !inSection || !strings.Contains(trimmed, "=") {
			continue
		}
		key, value, _ := strings.Cut(trimmed, "=")
		if strings.TrimSpace(key) != "command" {
			continue
		}
		parsed, err := strconv.Unquote(strings.TrimSpace(value))
		if err == nil {
			state.Command = parsed
		} else {
			state.Command = strings.Trim(strings.TrimSpace(value), "\"")
		}
	}
	state.Legacy = legacyGhostchromeCommand(state.Command, []string{"mcp"})
	return state, nil
}

func tomlMCPBlock(command string) string {
	return "[mcp_servers." + setupSkillName + "]\ncommand = " + strconv.Quote(command) + "\nargs = [\"--stealth\"]\nenabled = true\n"
}

func setupMCPTOML(path, command string, force, remove bool) (mcpConfigState, error) {
	if err := setupCheckUnmanagedFile(path); err != nil {
		return mcpConfigState{}, err
	}
	state, err := tomlMCPState(path)
	if err != nil {
		return state, fmt.Errorf("parse %s: %w", path, err)
	}
	if state.Present && !state.Legacy && !state.Managed && !force {
		return state, fmt.Errorf("MCP config conflict at %s; refusing to overwrite a non-Ghostchrome server", path)
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		data = nil
	} else if err != nil {
		return state, err
	}
	content := string(data)
	if state.Present {
		lines := strings.Split(content, "\n")
		out := make([]string, 0, len(lines))
		inSection := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
				section := strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]")
				inSection = section == "mcp_servers."+setupSkillName || strings.HasPrefix(section, "mcp_servers."+setupSkillName+".")
			}
			if !inSection {
				out = append(out, line)
			}
		}
		content = strings.Join(out, "\n")
	}
	if !remove {
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += tomlMCPBlock(command)
	}
	if err := writeSetupAtomic(path, []byte(content), 0o600); err != nil {
		return state, err
	}
	return state, nil
}

func setupConfigForClient(paths setupPaths, client string, command string, force, remove bool) (mcpConfigState, error) {
	path := configPathForClient(paths, client)
	if client == "claude" {
		return setupMCPJSON(path, command, force, remove)
	}
	return setupMCPTOML(path, command, force, remove)
}

func setupManagedMap(manifest *setupManifest) map[string]bool {
	managed := make(map[string]bool)
	if manifest == nil {
		return managed
	}
	for _, path := range manifest.ManagedFiles {
		managed[filepath.Clean(path)] = true
	}
	return managed
}

func setupExistingOpposite(paths setupPaths, manifest *setupManifest, target setupMode) error {
	managed := setupManagedMap(manifest)
	for _, candidate := range []string{paths.CLI, paths.MCP} {
		if (target == setupModeCLI && candidate == paths.CLI) || (target == setupModeMCP && candidate == paths.MCP) {
			continue
		}
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		if !managed[filepath.Clean(candidate)] {
			return fmt.Errorf("opposite Ghostchrome artifact already exists at %s; refusing to install both modes", candidate)
		}
	}
	return nil
}

func setupLegacyMCPPresent(paths setupPaths, clients []string) (bool, error) {
	for _, client := range clients {
		path := configPathForClient(paths, client)
		var state mcpConfigState
		var err error
		if client == "claude" {
			_, state, err = jsonMCPState(path)
		} else {
			state, err = tomlMCPState(path)
		}
		if err != nil {
			return false, err
		}
		if state.Present && state.Legacy {
			return true, nil
		}
	}
	return false, nil
}

func setupVersion() string {
	if rootCmd.Version != "" {
		return rootCmd.Version
	}
	return "dev"
}

// installSetupMode performs the complete installation after all arguments
// have been validated. force is true only for an explicit `switch --yes` and
// permits migration of Ghostchrome's legacy MCP entries.
func installSetupMode(mode setupMode, clients []string, force bool) (*setupManifest, error) {
	home, err := setupHome()
	if err != nil {
		return nil, err
	}
	paths := pathsForHome(home)
	if err := os.MkdirAll(paths.Root, 0o700); err != nil {
		return nil, fmt.Errorf("create setup root: %w", err)
	}
	manifest, err := readSetupManifest(paths)
	if err != nil {
		return nil, err
	}
	if manifest != nil && manifest.Mode != mode && !force {
		return nil, fmt.Errorf("ghostchrome is already installed in %s mode; use `ghostchrome setup switch --to %s --yes`", manifest.Mode, mode)
	}
	if err := setupExistingOpposite(paths, manifest, mode); err != nil {
		return nil, err
	}
	if manifest == nil && !force {
		if legacy, err := setupLegacyMCPPresent(paths, clients); err != nil {
			return nil, err
		} else if legacy {
			return nil, errors.New("legacy Ghostchrome MCP registration detected; use `ghostchrome setup switch --to cli --yes` or `--to mcp --yes` to migrate it")
		}
	}
	managed := setupManagedMap(manifest)
	destination := paths.CLI
	if mode == setupModeMCP {
		destination = paths.MCP
	}

	// Preflight skill conflicts before touching client configuration.
	bundle := setupSkillBundle()
	content := bundle["SKILL.md"]
	if len(content) == 0 {
		return nil, errors.New("this build does not contain the Ghostchrome skill")
	}
	for _, client := range clients {
		for relative, fileContent := range bundle {
			skillPath := setupSkillFilePath(home, client, relative)
			if err := setupSkillConflict(skillPath, fileContent, managed[filepath.Clean(skillPath)], force); err != nil {
				return nil, err
			}
		}
	}

	// Preflight the one selected transport. In CLI mode, remove only entries
	// previously managed by this manifest or legacy entries during an explicit
	// switch. A foreign server named ghostchrome remains a hard conflict.
	clientStates := make(map[string]mcpConfigState, len(clients))
	for _, client := range clients {
		configPath := configPathForClient(paths, client)
		configManaged := managed[filepath.Clean(configPath)]
		state, err := setupReadClientState(configPath, client)
		if err != nil {
			return nil, err
		}
		clientStates[client] = state
		configOwned := configManaged && manifest != nil && manifest.Mode == setupModeMCP && state.Present && filepath.Clean(state.Command) == filepath.Clean(manifest.Binary)
		legacyUsable := state.Legacy
		if configManaged && manifest != nil && manifest.Mode == setupModeMCP && state.Present && filepath.Clean(state.Command) != filepath.Clean(manifest.Binary) {
			// A managed config whose entry was changed is user-owned now, even
			// when the replacement still happens to be named ghostchrome-mcp.
			legacyUsable = false
		}
		if mode == setupModeMCP {
			if state.Present && !legacyUsable && !configOwned {
				return nil, fmt.Errorf("MCP config conflict at %s; refusing to overwrite an unmanaged entry", configPath)
			}
		} else {
			if state.Present && !legacyUsable && !configOwned {
				return nil, fmt.Errorf("MCP config conflict at %s; refusing to remove a non-Ghostchrome server", configPath)
			}
		}
	}

	// Resolve the source only after all conflict checks have passed. In
	// particular, a foreign client config must not leave a freshly copied
	// binary behind when setup aborts.
	source, err := setupSourceBinary(mode)
	if err != nil {
		return nil, err
	}
	if err := setupCopyBinary(source, destination, managed[filepath.Clean(destination)]); err != nil {
		return nil, err
	}
	for _, client := range clients {
		state := clientStates[client]
		configPath := configPathForClient(paths, client)
		configManaged := managed[filepath.Clean(configPath)]
		if mode == setupModeMCP || (state.Present && (configManaged || state.Legacy)) {
			remove := mode == setupModeCLI
			if _, err := setupConfigForClient(paths, client, destination, force || configManaged, remove); err != nil {
				return nil, err
			}
		}
	}

	skillHash, skillPaths, err := installSetupSkills(home, clients, managed, force)
	if err != nil {
		return nil, err
	}
	managedFiles := []string{destination}
	managedFiles = append(managedFiles, skillPaths...)
	if mode == setupModeMCP {
		for _, client := range clients {
			managedFiles = append(managedFiles, configPathForClient(paths, client))
		}
	}
	if manifest != nil && manifest.Mode == setupModeMCP && mode == setupModeCLI {
		// Keep the previous config files in the manifest so uninstall/switch can
		// safely identify them even if a selected-client list changed.
		managedFiles = append(managedFiles, setupManifestConfigFiles(manifest)...)
	}
	managedFiles = uniqueSetupPaths(managedFiles)

	newManifest := &setupManifest{
		SchemaVersion: setupManifestSchema,
		Mode:          mode,
		Version:       setupVersion(),
		InstallRoot:   paths.Root,
		Binary:        destination,
		Clients:       append([]string(nil), clients...),
		SkillSHA256:   skillHash,
		ManagedFiles:  managedFiles,
		InstalledAt:   time.Now().UTC().Format(time.RFC3339Nano),
	}
	if manifest != nil && manifest.InstalledAt != "" && manifest.Mode == mode {
		newManifest.InstalledAt = manifest.InstalledAt
	}
	if err := writeSetupManifest(paths, newManifest); err != nil {
		return nil, err
	}
	// Remove the old managed artifact only after the new artifact and manifest
	// are in place. This ordering keeps a failed switch recoverable.
	if manifest != nil && manifest.Binary != "" && filepath.Clean(manifest.Binary) != filepath.Clean(destination) {
		if managed[filepath.Clean(manifest.Binary)] {
			if err := os.Remove(manifest.Binary); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("remove previous Ghostchrome binary %s: %w", manifest.Binary, err)
			}
		}
	}
	return newManifest, nil
}

func setupManifestConfigFiles(manifest *setupManifest) []string {
	var files []string
	for _, path := range manifest.ManagedFiles {
		base := filepath.Base(path)
		if base == ".claude.json" || base == "config.toml" {
			files = append(files, path)
		}
	}
	return files
}

func uniqueSetupPaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.Clean(path)
		if path == "." || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func setupReadClientState(path, client string) (mcpConfigState, error) {
	if client == "claude" {
		_, state, err := jsonMCPState(path)
		return state, err
	}
	return tomlMCPState(path)
}

func withSetupLock(paths setupPaths, fn func() error) error {
	if err := os.MkdirAll(paths.Root, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(paths.Lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("another Ghostchrome setup is in progress (%s)", paths.Lock)
		}
		return fmt.Errorf("create setup lock: %w", err)
	}
	_, _ = io.WriteString(lock, fmt.Sprintf("pid=%d\n", os.Getpid()))
	_ = lock.Close()
	defer os.Remove(paths.Lock)
	return fn()
}

func setupInstall(mode setupMode, clients []string, force bool) (*setupManifest, error) {
	home, err := setupHome()
	if err != nil {
		return nil, err
	}
	paths := pathsForHome(home)
	var manifest *setupManifest
	err = withSetupLock(paths, func() error {
		var err error
		manifest, err = installSetupMode(mode, clients, force)
		return err
	})
	return manifest, err
}

type setupClientStatus struct {
	Client string `json:"client"`
	Skill  string `json:"skill"`
	MCP    string `json:"mcp"`
}

type setupStatus struct {
	Configured bool                `json:"configured"`
	Manifest   *setupManifest      `json:"manifest,omitempty"`
	Artifacts  []string            `json:"artifacts"`
	Clients    []setupClientStatus `json:"clients"`
}

func gatherSetupStatus() (setupStatus, error) {
	home, err := setupHome()
	if err != nil {
		return setupStatus{}, err
	}
	paths := pathsForHome(home)
	manifest, err := readSetupManifest(paths)
	if err != nil {
		return setupStatus{}, err
	}
	status := setupStatus{Manifest: manifest, Configured: manifest != nil}
	for _, artifact := range []string{paths.CLI, paths.MCP} {
		if _, err := os.Stat(artifact); err == nil {
			status.Artifacts = append(status.Artifacts, artifact)
		}
	}
	clients := []string{"claude", "codex", "grok"}
	if manifest != nil && len(manifest.Clients) > 0 {
		clients = manifest.Clients
	}
	for _, client := range clients {
		s := setupClientStatus{Client: client}
		skill := setupSkillPath(home, client)
		if _, err := os.Stat(skill); err == nil {
			s.Skill = skill
		} else {
			s.Skill = "not installed"
		}
		config := configPathForClient(paths, client)
		state, err := setupReadClientState(config, client)
		if err != nil {
			return status, err
		}
		if state.Present {
			s.MCP = state.Command
		} else {
			s.MCP = "not configured"
		}
		status.Clients = append(status.Clients, s)
	}
	return status, nil
}

func printSetupStatus(out io.Writer, status setupStatus) error {
	if flagFormat == "json" || flagJSON {
		data, err := json.MarshalIndent(status, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(data))
		return err
	}
	if !status.Configured {
		_, err := fmt.Fprintln(out, "ghostchrome setup: not configured")
		return err
	}
	m := status.Manifest
	fmt.Fprintf(out, "ghostchrome setup: %s mode\n", m.Mode)
	fmt.Fprintf(out, "  binary: %s\n", m.Binary)
	fmt.Fprintf(out, "  version: %s\n", m.Version)
	fmt.Fprintf(out, "  clients: %s\n", strings.Join(m.Clients, ", "))
	for _, artifact := range status.Artifacts {
		fmt.Fprintf(out, "  artifact: %s\n", artifact)
	}
	for _, client := range status.Clients {
		fmt.Fprintf(out, "  %-6s skill=%s", client.Client, client.Skill)
		if m.Mode == setupModeMCP {
			fmt.Fprintf(out, " mcp=%s", client.MCP)
		}
		fmt.Fprintln(out)
	}
	return nil
}

type setupDoctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func runSetupDoctor(strict bool) ([]setupDoctorCheck, error) {
	status, err := gatherSetupStatus()
	if err != nil {
		return nil, err
	}
	checks := []setupDoctorCheck{}
	add := func(name, state, detail string) {
		checks = append(checks, setupDoctorCheck{Name: name, Status: state, Detail: detail})
	}
	if !status.Configured {
		add("manifest", "fail", "not configured; run ghostchrome setup --mode cli|mcp")
		return checks, nil
	}
	manifest := status.Manifest
	add("manifest", "ok", fmt.Sprintf("%s mode, schema %d", manifest.Mode, manifest.SchemaVersion))
	if info, err := os.Stat(manifest.Binary); err != nil {
		add("binary", "fail", err.Error())
	} else if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		add("binary", "fail", "installed binary is not executable")
	} else {
		add("binary", "ok", manifest.Binary)
	}
	opposite := pathsForHome(mustSetupHome())
	other := opposite.MCP
	if manifest.Mode == setupModeMCP {
		other = opposite.CLI
	}
	if _, err := os.Stat(other); err == nil {
		add("exclusive-mode", "fail", "opposite artifact exists: "+other)
	} else {
		add("exclusive-mode", "ok", "only selected transport is installed")
	}
	if manifest.SkillSHA256 == "" {
		add("skill", "warn", "manifest has no skill hash")
	} else {
		allSkillsOK := true
		for _, client := range manifest.Clients {
			path := setupSkillPath(opposite.Home, client)
			data, err := os.ReadFile(path)
			if err != nil || setupFileHash(data) != manifest.SkillSHA256 {
				allSkillsOK = false
				add("skill-"+client, "fail", "missing or hash mismatch: "+path)
			}
		}
		if allSkillsOK {
			add("skill", "ok", fmt.Sprintf("sha256:%s", manifest.SkillSHA256))
		}
	}
	if manifest.Mode == setupModeMCP {
		for _, client := range manifest.Clients {
			path := configPathForClient(opposite, client)
			state, err := setupReadClientState(path, client)
			if err != nil {
				add("mcp-"+client, "fail", err.Error())
				continue
			}
			if !state.Present || filepath.Clean(state.Command) != filepath.Clean(manifest.Binary) {
				add("mcp-"+client, "fail", "registration does not point to "+manifest.Binary)
			} else {
				add("mcp-"+client, "ok", path)
			}
		}
	}
	if chrome := engine.FindSystemChromeBinary(); chrome == "" {
		add("chrome", "warn", "no system Chrome found; the runtime may download Chromium on first use")
	} else {
		add("chrome", "ok", chrome)
	}
	if ws, err := engine.DiscoverCDP(nil, 250*time.Millisecond); err == nil {
		add("cdp", "ok", ws)
	} else {
		add("cdp", "info", "no running Chrome with remote debugging; it will start on first browser operation")
	}
	if strict {
		// The caller decides the exit code from fail checks. Keeping warnings and
		// informational checks visible makes `--strict` useful in CI without
		// requiring a running browser daemon.
		_ = strict
	}
	return checks, nil
}

func mustSetupHome() string {
	home, _ := setupHome()
	return home
}

func printSetupDoctor(out io.Writer, checks []setupDoctorCheck, strict bool) error {
	failed := false
	if flagFormat == "json" || flagJSON {
		data, err := json.MarshalIndent(checks, "", "  ")
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(out, string(data)); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(out, "ghostchrome setup doctor")
		for _, check := range checks {
			icon := "✓"
			switch check.Status {
			case "fail":
				icon = "✗"
				failed = true
			case "warn":
				icon = "⚠"
			case "info":
				icon = "ℹ"
			}
			fmt.Fprintf(out, "  %s %-18s %s\n", icon, check.Name, check.Detail)
		}
	}
	for _, check := range checks {
		if check.Status == "fail" {
			failed = true
		}
	}
	if strict && failed {
		return errors.New("setup doctor found failing checks")
	}
	return nil
}

func setupUninstall(yes, purge bool, out, errOut io.Writer) error {
	if !yes {
		return errors.New("refusing to uninstall without --yes")
	}
	home, err := setupHome()
	if err != nil {
		return err
	}
	paths := pathsForHome(home)
	return withSetupLock(paths, func() error {
		manifest, err := readSetupManifest(paths)
		if err != nil {
			return err
		}
		if manifest == nil {
			return errors.New("ghostchrome setup is not configured")
		}
		if n, killErr := engine.KillAllSessions(); killErr != nil {
			fmt.Fprintf(errOut, "warning: could not stop all sessions: %v\n", killErr)
		} else if n > 0 {
			fmt.Fprintf(out, "stopped %d session(s)\n", n)
		}
		managed := setupManagedMap(manifest)
		// Remove MCP registrations by editing their containing config, not by
		// deleting the user's entire config file.
		if manifest.Mode == setupModeMCP {
			for _, client := range manifest.Clients {
				path := configPathForClient(paths, client)
				state, stateErr := setupReadClientState(path, client)
				if stateErr != nil {
					return stateErr
				}
				// Older installer manifests did not list client config paths. An
				// exact command match is sufficient ownership evidence; a changed
				// entry remains untouched.
				if !managed[filepath.Clean(path)] && (!state.Present || filepath.Clean(state.Command) != filepath.Clean(manifest.Binary)) {
					continue
				}
				if state.Present && filepath.Clean(state.Command) != filepath.Clean(manifest.Binary) {
					fmt.Fprintf(errOut, "warning: preserving modified MCP registration %s\n", path)
					continue
				}
				if _, err := setupConfigForClient(paths, client, manifest.Binary, true, true); err != nil {
					return err
				}
			}
		}
		for _, path := range manifest.ManagedFiles {
			if filepath.Clean(path) == filepath.Clean(paths.Manifest) || filepath.Clean(path) == filepath.Clean(paths.Lock) {
				continue
			}
			if filepath.Clean(path) == filepath.Clean(paths.ClaudeConfig) || filepath.Clean(path) == filepath.Clean(paths.CodexConfig) || filepath.Clean(path) == filepath.Clean(paths.GrokConfig) {
				continue
			}
			if strings.HasSuffix(path, filepath.Join(setupSkillName, "SKILL.md")) {
				data, readErr := os.ReadFile(path)
				if readErr == nil && manifest.SkillSHA256 != "" && setupFileHash(data) != manifest.SkillSHA256 {
					fmt.Fprintf(errOut, "warning: preserving modified skill %s\n", path)
					continue
				}
			}
			if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
				if err := removeSetupSkillDir(path, manifest.SkillSHA256, errOut); err != nil {
					return err
				}
				continue
			}
			if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return fmt.Errorf("remove managed file %s: %w", path, removeErr)
			}
		}
		if purge {
			for _, dir := range engine.GhostchromeDataDirs() {
				if removeErr := os.RemoveAll(dir); removeErr != nil {
					return fmt.Errorf("remove data directory %s: %w", dir, removeErr)
				}
			}
		}
		if err := os.Remove(paths.Manifest); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		// Remove empty setup directories only; profiles and other user data stay.
		_ = os.Remove(paths.BinDir)
		_ = os.Remove(paths.Root)
		fmt.Fprintln(out, "ghostchrome setup uninstalled (browser profiles preserved)")
		return nil
	})
}

// removeSetupSkillDir supports manifests produced by the shell installer,
// which historically tracked a skill directory rather than every file. The
// directory is removed only when its entrypoint still has the recorded hash;
// custom or modified skills are preserved as user-owned content.
func removeSetupSkillDir(path, skillHash string, errOut io.Writer) error {
	entry := filepath.Join(path, "SKILL.md")
	data, err := os.ReadFile(entry)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(errOut, "warning: preserving incomplete skill directory %s\n", path)
			return nil
		}
		return err
	}
	if skillHash == "" || setupFileHash(data) != skillHash {
		fmt.Fprintf(errOut, "warning: preserving modified skill directory %s\n", path)
		return nil
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove managed skill directory %s: %w", path, err)
	}
	return nil
}

func setupCommandError(action string, err error) error {
	return fmt.Errorf("setup %s: %w", action, err)
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Install exactly one Ghostchrome transport and its global skill",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if strings.TrimSpace(setupModeFlag) == "" {
			return errors.New("--mode is required on first setup; choose cli or mcp")
		}
		mode, err := parseSetupMode(setupModeFlag)
		if err != nil {
			return setupCommandError("install", err)
		}
		clients, err := parseSetupClients(setupClientsFlag)
		if err != nil {
			return setupCommandError("install", err)
		}
		manifest, err := setupInstall(mode, clients, false)
		if err != nil {
			return setupCommandError("install", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "ghostchrome setup installed in %s mode\n  binary: %s\n", manifest.Mode, manifest.Binary)
		return nil
	},
}

var setupStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the active Ghostchrome setup mode",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		status, err := gatherSetupStatus()
		if err != nil {
			return setupCommandError("status", err)
		}
		return printSetupStatus(cmd.OutOrStdout(), status)
	},
}

var setupDoctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Validate the selected artifact, skill, clients, Chrome and CDP",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		checks, err := runSetupDoctor(setupDoctorStrict)
		if err != nil {
			return setupCommandError("doctor", err)
		}
		return printSetupDoctor(cmd.OutOrStdout(), checks, setupDoctorStrict)
	},
}

var setupSwitchCmd = &cobra.Command{
	Use:   "switch",
	Short: "Explicitly switch between CLI and MCP mode",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !setupSwitchYes {
			return errors.New("refusing to switch setup mode without --yes")
		}
		mode, err := parseSetupMode(setupSwitchTo)
		if err != nil {
			return setupCommandError("switch", err)
		}
		home, err := setupHome()
		if err != nil {
			return setupCommandError("switch", err)
		}
		paths := pathsForHome(home)
		manifest, err := readSetupManifest(paths)
		if err != nil {
			return setupCommandError("switch", err)
		}
		clients := []string{"claude", "codex", "grok"}
		if manifest != nil && len(manifest.Clients) > 0 {
			clients = manifest.Clients
		} else if strings.TrimSpace(setupClientsFlag) != "" && setupClientsFlag != setupDefaultClients {
			clients, err = parseSetupClients(setupClientsFlag)
			if err != nil {
				return setupCommandError("switch", err)
			}
		}
		if manifest == nil {
			legacy, legacyErr := setupLegacyMCPPresent(paths, clients)
			if legacyErr != nil {
				return setupCommandError("switch", legacyErr)
			}
			if !legacy {
				return errors.New("no existing Ghostchrome setup to switch; use `ghostchrome setup --mode cli|mcp`")
			}
		}
		newManifest, err := setupInstall(mode, clients, true)
		if err != nil {
			return setupCommandError("switch", err)
		}
		checks, doctorErr := runSetupDoctor(true)
		if doctorErr != nil {
			return setupCommandError("switch", fmt.Errorf("post-switch doctor: %w", doctorErr))
		}
		for _, check := range checks {
			if check.Status == "fail" {
				return setupCommandError("switch", fmt.Errorf("post-switch doctor failed: %s", check.Detail))
			}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "ghostchrome setup switched to %s mode\n  binary: %s\n", newManifest.Mode, newManifest.Binary)
		return nil
	},
}

var setupUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the managed transport, skill and registrations",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if !setupUninstallYes {
			return errors.New("refusing to uninstall without --yes")
		}
		return setupUninstall(true, setupPurgeData, cmd.OutOrStdout(), cmd.ErrOrStderr())
	},
}

func init() {
	setupCmd.Flags().StringVar(&setupModeFlag, "mode", "", "Installation mode: cli or mcp")
	setupCmd.Flags().StringVar(&setupClientsFlag, "clients", setupDefaultClients, "Comma-separated global clients: claude,codex,grok")
	setupDoctorCmd.Flags().BoolVar(&setupDoctorStrict, "strict", false, "Exit non-zero when a hard check fails")
	setupSwitchCmd.Flags().StringVar(&setupSwitchTo, "to", "", "Target installation mode: cli or mcp")
	setupSwitchCmd.Flags().BoolVar(&setupSwitchYes, "yes", false, "Confirm the explicit transport switch")
	setupUninstallCmd.Flags().BoolVar(&setupUninstallYes, "yes", false, "Confirm removal of managed setup files")
	setupUninstallCmd.Flags().BoolVar(&setupPurgeData, "purge-data", false, "Also remove browser profiles and Ghostchrome data")
	setupCmd.AddCommand(setupStatusCmd, setupDoctorCmd, setupSwitchCmd, setupUninstallCmd)
	rootCmd.AddCommand(setupCmd)
	commandGroups["setup"] = "util"
}
