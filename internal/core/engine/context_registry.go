package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// ContextEntry maps a user-supplied name to a CDP BrowserContextID.
type ContextEntry struct {
	ID string `json:"id"`
}

// contextRegistry is the on-disk map of name → ContextEntry.
type contextRegistry struct {
	Contexts map[string]ContextEntry `json:"contexts"`
}

// contextRegistryPath returns ~/.ghostchrome/contexts.json, creating the
// parent directory with 0700 perms if needed.
func contextRegistryPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	dir := filepath.Join(home, ".ghostchrome")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create .ghostchrome dir: %w", err)
	}
	return filepath.Join(dir, "contexts.json"), nil
}

func loadContextRegistry() (*contextRegistry, string, error) {
	path, err := contextRegistryPath()
	if err != nil {
		return nil, "", err
	}
	reg := &contextRegistry{Contexts: map[string]ContextEntry{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return reg, path, nil
		}
		return nil, path, fmt.Errorf("read contexts registry: %w", err)
	}
	if err := json.Unmarshal(data, reg); err != nil {
		return nil, path, fmt.Errorf("parse contexts registry: %w", err)
	}
	if reg.Contexts == nil {
		reg.Contexts = map[string]ContextEntry{}
	}
	return reg, path, nil
}

func saveContextRegistry(path string, reg *contextRegistry) error {
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// liveContextIDs queries Chrome for all active browser context IDs.
func liveContextIDs(b *rod.Browser) (map[string]struct{}, error) {
	res, err := proto.TargetGetBrowserContexts{}.Call(b)
	if err != nil {
		return nil, err
	}
	live := make(map[string]struct{}, len(res.BrowserContextIDs))
	for _, id := range res.BrowserContextIDs {
		live[string(id)] = struct{}{}
	}
	return live, nil
}

// AcquireContext returns a *rod.Browser view scoped to the named isolated
// context. It reuses an existing context if the registry entry is still
// alive in Chrome, otherwise creates a new one and persists it.
//
// The caller must have already connected b to Chrome before calling this.
func AcquireContext(b *rod.Browser, name string) (*rod.Browser, error) {
	reg, path, err := loadContextRegistry()
	if err != nil {
		return nil, err
	}

	live, err := liveContextIDs(b)
	if err != nil {
		return nil, fmt.Errorf("list browser contexts: %w", err)
	}

	if entry, ok := reg.Contexts[name]; ok {
		if _, alive := live[entry.ID]; alive {
			// Reuse existing context — copy the browser view and set its ID.
			view := *b
			view.BrowserContextID = proto.BrowserBrowserContextID(entry.ID)
			return &view, nil
		}
		// Stale entry — remove and recreate below.
		delete(reg.Contexts, name)
	}

	// Create a new incognito context.
	incognito, err := b.Incognito()
	if err != nil {
		return nil, fmt.Errorf("create incognito context: %w", err)
	}

	reg.Contexts[name] = ContextEntry{ID: string(incognito.BrowserContextID)}
	if err := saveContextRegistry(path, reg); err != nil {
		return nil, fmt.Errorf("save contexts registry: %w", err)
	}

	return incognito, nil
}

// ContextRegistryEntry is the public-facing representation used by the CLI.
type ContextRegistryEntry struct {
	Name    string `json:"name"`
	ID      string `json:"id"`
	Alive   bool   `json:"alive"`
	Unknown bool   `json:"unknown,omitempty"` // true when Chrome is unreachable
}

// AliveStr returns "yes", "no", or "?" depending on liveness state.
func (e ContextRegistryEntry) AliveStr() string {
	if e.Unknown {
		return "?"
	}
	if e.Alive {
		return "yes"
	}
	return "no"
}

// ListContexts returns all registry entries annotated with liveness.
// b may be nil — in that case all entries are reported as unknown/false.
func ListContexts(b *rod.Browser) ([]ContextRegistryEntry, error) {
	reg, _, err := loadContextRegistry()
	if err != nil {
		return nil, err
	}

	var live map[string]struct{}
	if b != nil {
		live, _ = liveContextIDs(b)
	}

	out := make([]ContextRegistryEntry, 0, len(reg.Contexts))
	for name, entry := range reg.Contexts {
		_, alive := live[entry.ID]
		out = append(out, ContextRegistryEntry{
			Name:    name,
			ID:      entry.ID,
			Alive:   alive,
			Unknown: live == nil,
		})
	}
	return out, nil
}

// CloseContext disposes the named context in Chrome (if alive) and removes it
// from the registry.
func CloseContext(b *rod.Browser, name string) error {
	reg, path, err := loadContextRegistry()
	if err != nil {
		return err
	}

	entry, ok := reg.Contexts[name]
	if !ok {
		return fmt.Errorf("context %q not found in registry", name)
	}

	// Best-effort dispose — Chrome may have already cleaned it up.
	if b != nil {
		_ = proto.TargetDisposeBrowserContext{
			BrowserContextID: proto.BrowserBrowserContextID(entry.ID),
		}.Call(b)
	}

	delete(reg.Contexts, name)
	return saveContextRegistry(path, reg)
}
