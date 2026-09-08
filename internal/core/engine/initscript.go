package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

type installedScript struct {
	source string
	id     proto.PageScriptIdentifier
}

type installedScriptSet struct {
	mu      sync.Mutex
	scripts map[string]installedScript
}

var installedPageScripts sync.Map // pageSessionKey -> *installedScriptSet

// InitScriptsDir returns the directory for user init scripts.
func InitScriptsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".ghostchrome", "init-scripts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// ListInitScripts returns the names of installed init scripts.
func ListInitScripts() ([]string, error) {
	dir, err := InitScriptsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".js") {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// AddInitScript copies a JS file into the init-scripts directory.
func AddInitScript(srcPath string) (string, error) {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return "", fmt.Errorf("read script: %w", err)
	}
	dir, err := InitScriptsDir()
	if err != nil {
		return "", err
	}
	name := filepath.Base(srcPath)
	if !strings.HasSuffix(name, ".js") {
		name += ".js"
	}
	dst := filepath.Join(dir, name)
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return "", err
	}
	return name, nil
}

// RemoveInitScript removes a script by name from the init-scripts directory.
func RemoveInitScript(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("init script name must be a filename without directory separators")
	}
	dir, err := InitScriptsDir()
	if err != nil {
		return err
	}
	if !strings.HasSuffix(name, ".js") {
		name += ".js"
	}
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("init script %q not found", name)
	}
	return os.Remove(path)
}

// ApplyInitScripts registers installed scripts before scripts on future documents.
func ApplyInitScripts(page *rod.Page) error {
	if page == nil {
		return fmt.Errorf("page is nil")
	}
	dir, err := InitScriptsDir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	sources := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".js") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return fmt.Errorf("read %s: %w", e.Name(), err)
		}
		sources[e.Name()] = string(data)
	}
	key := pageSessionKey{browser: page.Browser(), session: page.SessionID}
	if len(sources) == 0 {
		if _, ok := installedPageScripts.Load(key); !ok {
			return nil
		}
	} else if err := ActivePolicy.AllowAction("eval"); err != nil {
		return err
	}
	value, _ := installedPageScripts.LoadOrStore(key, &installedScriptSet{scripts: map[string]installedScript{}})
	set := value.(*installedScriptSet)
	set.mu.Lock()
	defer set.mu.Unlock()
	for name, script := range set.scripts {
		if source, ok := sources[name]; ok && source == script.source {
			continue
		}
		if err := (proto.PageRemoveScriptToEvaluateOnNewDocument{Identifier: script.id}).Call(page); err != nil {
			return err
		}
		delete(set.scripts, name)
	}
	// ReadDir is sorted; preserve installation order for dependent scripts.
	for _, entry := range entries {
		name := entry.Name()
		source, ok := sources[name]
		if !ok {
			continue
		}
		if _, ok := set.scripts[name]; ok {
			continue
		}
		result, err := (proto.PageAddScriptToEvaluateOnNewDocument{Source: source}).Call(page)
		if err != nil {
			return fmt.Errorf("register %s: %w", name, err)
		}
		set.scripts[name] = installedScript{source: source, id: result.Identifier}
	}
	return nil
}

// ApplyInitScriptFiles registers JavaScript files to run before future page
// scripts. This mirrors Playwright's addInitScript-style behavior for config
// supplied init scripts.
func ApplyInitScriptFiles(page *rod.Page, paths []string) error {
	if page == nil {
		return fmt.Errorf("page is nil")
	}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read init script %s: %w", path, err)
		}
		if _, err := page.EvalOnNewDocument(string(data)); err != nil {
			return fmt.Errorf("register init script %s: %w", path, err)
		}
	}
	return nil
}
