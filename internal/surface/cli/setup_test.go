package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func setupTestEnvironment(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}
	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "ghostchrome-source"+binarySuffix())
	if err := os.WriteFile(source, []byte("#!/bin/sh\necho ghostchrome-test\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GHOSTCHROME_SETUP_SOURCE", source)
	oldSkill, hadSkill := embeddedSkills[setupSkillName]
	oldBundle := embeddedSkillFiles
	embeddedSkillFiles = map[string]string{
		"SKILL.md":             "---\nname: ghostchrome\n---\n# test skill\n",
		"references/cli.md":    "# CLI\n",
		"references/mcp.md":    "# MCP\n",
		"examples/cli-flow.sh": "#!/bin/sh\necho cli\n",
		"scripts/validate.sh":  "#!/bin/sh\nexit 0\n",
	}
	embeddedSkills[setupSkillName] = embeddedSkillFiles["SKILL.md"]
	t.Cleanup(func() {
		if hadSkill {
			embeddedSkills[setupSkillName] = oldSkill
		} else {
			delete(embeddedSkills, setupSkillName)
		}
		embeddedSkillFiles = oldBundle
	})
	return home, source
}

func TestParseSetupModeAndClients(t *testing.T) {
	for _, test := range []struct {
		input string
		want  setupMode
	}{
		{input: "cli", want: setupModeCLI},
		{input: " MCP ", want: setupModeMCP},
	} {
		got, err := parseSetupMode(test.input)
		if err != nil || got != test.want {
			t.Fatalf("parseSetupMode(%q) = %q, %v; want %q", test.input, got, err, test.want)
		}
	}
	if _, err := parseSetupMode("both"); err == nil {
		t.Fatal("expected invalid mode to fail")
	}
	clients, err := parseSetupClients("grok,claude,grok")
	if err != nil || strings.Join(clients, ",") != "claude,grok" {
		t.Fatalf("parseSetupClients dedupe/sort = %v, %v", clients, err)
	}
	if _, err := parseSetupClients("claude,unknown"); err == nil {
		t.Fatal("expected unknown client to fail")
	}
	if _, err := parseSetupClients(",,"); err == nil {
		t.Fatal("expected empty client list to fail")
	}
}

func TestSetupCLIIsExclusiveAndIdempotent(t *testing.T) {
	home, _ := setupTestEnvironment(t)
	clients := []string{"claude", "codex", "grok"}
	first, err := setupInstall(setupModeCLI, clients, false)
	if err != nil {
		t.Fatalf("install CLI: %v", err)
	}
	if first.Mode != setupModeCLI || first.Binary != filepath.Join(home, ".ghostchrome", "bin", setupCLIName+binarySuffix()) {
		t.Fatalf("unexpected CLI manifest: %+v", first)
	}
	if _, err := os.Stat(filepath.Join(home, ".ghostchrome", "bin", setupMCPName+binarySuffix())); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("MCP artifact should not be installed, err=%v", err)
	}
	for _, client := range clients {
		for _, relative := range []string{"SKILL.md", "references/cli.md", "references/mcp.md", "examples/cli-flow.sh", "scripts/validate.sh"} {
			path := setupSkillFilePath(home, client, relative)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("skill file missing for %s: %s: %v", client, relative, err)
			}
			if runtime.GOOS != "windows" && strings.HasSuffix(relative, ".sh") && info.Mode().Perm()&0o111 == 0 {
				t.Fatalf("skill script is not executable for %s: %s", client, relative)
			}
		}
	}
	second, err := setupInstall(setupModeCLI, clients, false)
	if err != nil {
		t.Fatalf("idempotent CLI install: %v", err)
	}
	if first.InstalledAt != second.InstalledAt {
		t.Fatalf("idempotent install changed timestamp: %q -> %q", first.InstalledAt, second.InstalledAt)
	}
	if _, err := setupInstall(setupModeMCP, clients, false); err == nil || !strings.Contains(err.Error(), "already installed in cli mode") {
		t.Fatalf("expected mode refusal, got %v", err)
	}
	manifest, err := readSetupManifest(pathsForHome(home))
	if err != nil || manifest.Mode != setupModeCLI {
		t.Fatalf("mode refusal changed manifest: manifest=%+v err=%v", manifest, err)
	}
}

func TestSetupMCPWritesAllClientRegistrationsAndSwitches(t *testing.T) {
	home, _ := setupTestEnvironment(t)
	clients := []string{"claude", "codex", "grok"}
	manifest, err := setupInstall(setupModeMCP, clients, false)
	if err != nil {
		t.Fatalf("install MCP: %v", err)
	}
	paths := pathsForHome(home)
	if manifest.Mode != setupModeMCP || manifest.Binary != paths.MCP {
		t.Fatalf("unexpected MCP manifest: %+v", manifest)
	}
	_, state, err := jsonMCPState(paths.ClaudeConfig)
	if err != nil || !state.Present || state.Command != paths.MCP {
		t.Fatalf("Claude MCP registration: %+v, err=%v", state, err)
	}
	for _, client := range []string{"codex", "grok"} {
		state, err := tomlMCPState(configPathForClient(paths, client))
		if err != nil || !state.Present || state.Command != paths.MCP {
			t.Fatalf("%s MCP registration: %+v, err=%v", client, state, err)
		}
	}
	if _, err := setupInstall(setupModeCLI, clients, false); err == nil || !strings.Contains(err.Error(), "already installed in mcp mode") {
		t.Fatalf("expected switch refusal without explicit force, got %v", err)
	}
	cli, err := setupInstall(setupModeCLI, clients, true)
	if err != nil {
		t.Fatalf("switch to CLI: %v", err)
	}
	if cli.Mode != setupModeCLI || cli.Binary != paths.CLI {
		t.Fatalf("unexpected switched manifest: %+v", cli)
	}
	if _, err := os.Stat(paths.MCP); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("MCP artifact remains after switch: %v", err)
	}
	_, state, err = jsonMCPState(paths.ClaudeConfig)
	if err != nil || state.Present {
		t.Fatalf("Claude MCP registration remains after switch: %+v, err=%v", state, err)
	}
	for _, client := range []string{"codex", "grok"} {
		state, err := tomlMCPState(configPathForClient(paths, client))
		if err != nil || state.Present {
			t.Fatalf("%s MCP registration remains after switch: %+v, err=%v", client, state, err)
		}
	}
}

func TestSetupSwitchPreservesChangedMCPRegistration(t *testing.T) {
	home, _ := setupTestEnvironment(t)
	clients := []string{"claude"}
	if _, err := setupInstall(setupModeMCP, clients, false); err != nil {
		t.Fatalf("install MCP: %v", err)
	}
	paths := pathsForHome(home)
	if err := os.WriteFile(paths.ClaudeConfig, []byte(`{"mcpServers":{"ghostchrome":{"type":"stdio","command":"/custom/ghostchrome-mcp","args":[]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := setupInstall(setupModeCLI, clients, true); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("expected changed MCP registration refusal, got %v", err)
	}
	if _, err := os.Stat(paths.CLI); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed switch left CLI artifact: %v", err)
	}
}

func TestSetupRefusesForeignArtifactsAndConfigs(t *testing.T) {
	home, _ := setupTestEnvironment(t)
	paths := pathsForHome(home)
	if err := os.MkdirAll(filepath.Dir(paths.MCP), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.MCP, []byte("foreign binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := setupInstall(setupModeCLI, []string{"claude"}, false); err == nil || !strings.Contains(err.Error(), "opposite Ghostchrome artifact") {
		t.Fatalf("expected foreign artifact refusal, got %v", err)
	}
	if err := os.Remove(paths.MCP); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ClaudeConfig, []byte(`{"mcpServers":{"ghostchrome":{"command":"/usr/local/bin/other"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := setupInstall(setupModeMCP, []string{"claude"}, false); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("expected foreign config refusal, got %v", err)
	}
	if _, err := os.Stat(paths.MCP); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("foreign config refusal left MCP artifact: %v", err)
	}
	if err := os.WriteFile(paths.ClaudeConfig, []byte(`{"mcpServers":{"ghostchrome":"foreign-shape"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := setupInstall(setupModeMCP, []string{"claude"}, false); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("expected malformed occupied config refusal, got %v", err)
	}
}

func TestSetupUninstallPreservesProfilesAndModifiedSkills(t *testing.T) {
	home, _ := setupTestEnvironment(t)
	clients := []string{"claude", "codex", "grok"}
	manifest, err := setupInstall(setupModeMCP, clients, false)
	if err != nil {
		t.Fatalf("install MCP: %v", err)
	}
	modified := setupSkillPath(home, "claude")
	if err := os.WriteFile(modified, []byte("custom user skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(home, ".ghostchrome", "profiles", "keep-me")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err := setupUninstall(false, false, &out, &errOut); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected uninstall confirmation refusal, got %v", err)
	}
	if err := setupUninstall(true, false, &out, &errOut); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(pathsForHome(home).Manifest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("manifest remains after uninstall: %v", err)
	}
	if _, err := os.Stat(manifest.Binary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("binary remains after uninstall: %v", err)
	}
	if data, err := os.ReadFile(modified); err != nil || string(data) != "custom user skill\n" {
		t.Fatalf("modified skill was not preserved: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(profile); err != nil {
		t.Fatalf("profile was removed without --purge-data: %v", err)
	}
	_, state, err := jsonMCPState(pathsForHome(home).ClaudeConfig)
	if err != nil || state.Present {
		t.Fatalf("Claude MCP registration remains: %+v, err=%v", state, err)
	}
}

func TestSetupManifestIsStrictAndAtomic(t *testing.T) {
	home, _ := setupTestEnvironment(t)
	paths := pathsForHome(home)
	manifest := &setupManifest{
		SchemaVersion: setupManifestSchema,
		Mode:          setupModeCLI,
		Version:       "test",
		InstallRoot:   paths.Root,
		Binary:        paths.CLI,
		Clients:       []string{"claude"},
		ManagedFiles:  []string{paths.CLI},
		InstalledAt:   "now",
	}
	if err := writeSetupManifest(paths, manifest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(paths.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	var decoded setupManifest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Mode != setupModeCLI || decoded.SchemaVersion != setupManifestSchema {
		t.Fatalf("decoded manifest mismatch: %+v", decoded)
	}
	if info, err := os.Stat(paths.Manifest); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("manifest permissions = %o, err=%v", info.Mode().Perm(), err)
	}
	if err := os.WriteFile(paths.Manifest, []byte(`{"schema_version":999}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSetupManifest(paths); err == nil || !strings.Contains(err.Error(), "unsupported setup manifest schema") {
		t.Fatalf("expected schema refusal, got %v", err)
	}
	manifest.SchemaVersion = setupManifestSchema
	manifest.ManagedFiles = []string{filepath.Join(home, "unrelated.txt")}
	if err := writeSetupManifest(paths, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := readSetupManifest(paths); err == nil || !strings.Contains(err.Error(), "outside Ghostchrome locations") {
		t.Fatalf("expected unsafe managed path refusal, got %v", err)
	}
	manifest.ManagedFiles = []string{"~/.ghostchrome/bin/" + setupCLIName + binarySuffix()}
	manifest.InstallRoot = "~/.ghostchrome"
	manifest.Binary = "~/.ghostchrome/bin/" + setupCLIName + binarySuffix()
	if err := writeSetupManifest(paths, manifest); err != nil {
		t.Fatal(err)
	}
	normalized, readErr := readSetupManifest(paths)
	if readErr != nil || normalized.Binary != paths.CLI || normalized.InstallRoot != paths.Root {
		t.Fatalf("expected tilde paths to normalize, manifest=%+v err=%v", normalized, readErr)
	}
}

func TestSetupInstructionsWritesManagedBlockAndBackup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}
	oldClients := setupClientsFlag
	oldWrite := setupInstructionsWrite
	setupClientsFlag = "codex"
	setupInstructionsWrite = true
	t.Cleanup(func() {
		setupClientsFlag = oldClients
		setupInstructionsWrite = oldWrite
	})
	path := setupInstructionPath(home, "codex")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runSetupInstructions(&out); err != nil {
		t.Fatalf("write instructions: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, setupInstructionsBegin) || !strings.Contains(text, "ghostchrome setup status") {
		t.Fatalf("managed block missing: %s", text)
	}
	backups, err := filepath.Glob(path + ".bak-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("expected one backup, got %v (err=%v)", backups, err)
	}
	if err := runSetupInstructions(&out); err != nil {
		t.Fatalf("idempotent instructions write: %v", err)
	}
}
