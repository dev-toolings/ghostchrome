package setup

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	setupInstructionsBegin = "<!-- ghostchrome:begin -->"
	setupInstructionsEnd   = "<!-- ghostchrome:end -->"
)

const setupInstructionsBlock = `Ghostchrome browser automation:
- Use the globally installed Ghostchrome skill.
- Read "ghostchrome setup status" before choosing a transport.
- Use CLI in CLI mode and MCP tools in MCP mode.
- Never invoke both transports in the same workflow.
- Run "ghostchrome setup doctor --strict" when the runtime is unavailable.`

func InstructionPath(home, client string) string {
	switch client {
	case "claude":
		return filepath.Join(home, ".claude", "CLAUDE.md")
	case "codex":
		return filepath.Join(home, ".codex", "AGENTS.md")
	case "grok":
		return filepath.Join(home, ".grok", "AGENTS.md")
	default:
		return ""
	}
}

func updateInstructionsFile(path string) error {
	if path == "" {
		return errors.New("empty instructions path")
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to modify symlinked instructions file %s", path)
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		data = nil
	} else if err != nil {
		return err
	}
	text := string(data)
	block := setupInstructionsBegin + "\n" + setupInstructionsBlock + "\n" + setupInstructionsEnd
	start := strings.Index(text, setupInstructionsBegin)
	end := strings.Index(text, setupInstructionsEnd)
	switch {
	case start >= 0 && end < start:
		return fmt.Errorf("ambiguous Ghostchrome instruction markers in %s", path)
	case start >= 0 && end >= 0:
		end += len(setupInstructionsEnd)
		text = text[:start] + block + text[end:]
	case start >= 0 || end >= 0:
		return fmt.Errorf("incomplete Ghostchrome instruction markers in %s", path)
	default:
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		if text != "" {
			text += "\n"
		}
		text += block + "\n"
	}
	return writeSetupAtomic(path, []byte(text), 0o600)
}

func backupInstructionsFile(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	backup := fmt.Sprintf("%s.bak-%s", path, time.Now().UTC().Format("20060102T150405.000000000Z"))
	return writeSetupAtomic(backup, data, 0o600)
}

// RunInstructions rewrites the managed policy block in every selected
// client instruction file. write must be the confirmation the caller
// obtained; clientsFlag is the raw comma-separated client list.
func RunInstructions(out io.Writer, write bool, clientsFlag string) error {
	if !write {
		return errors.New("refusing to modify global instructions without --write")
	}
	home, err := Home()
	if err != nil {
		return err
	}
	clients, err := ParseClients(clientsFlag)
	if err != nil {
		return err
	}
	for _, client := range clients {
		path := InstructionPath(home, client)
		block := setupInstructionsBegin + "\n" + setupInstructionsBlock + "\n" + setupInstructionsEnd
		if existing, readErr := os.ReadFile(path); readErr == nil {
			start := strings.Index(string(existing), setupInstructionsBegin)
			end := strings.Index(string(existing), setupInstructionsEnd)
			if start >= 0 && end >= start && strings.TrimSpace(string(existing[start:end+len(setupInstructionsEnd)])) == block {
				fmt.Fprintf(out, "instructions unchanged → %s\n", path)
				continue
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return fmt.Errorf("read %s: %w", path, readErr)
		}
		if err := backupInstructionsFile(path); err != nil {
			return fmt.Errorf("backup %s: %w", path, err)
		}
		if err := updateInstructionsFile(path); err != nil {
			return fmt.Errorf("update %s: %w", path, err)
		}
		fmt.Fprintf(out, "updated instructions → %s\n", path)
	}
	return nil
}
