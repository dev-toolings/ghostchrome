package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/joho/godotenv"
)

// redactJSONOutput masks string values without changing JSON types or keys.
func redactJSONOutput(data []byte, secrets []string) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	var redact func(any) any
	redact = func(v any) any {
		switch v := v.(type) {
		case string:
			return string(redactOutput([]byte(v), secrets))
		case []any:
			for i := range v {
				v[i] = redact(v[i])
			}
		case map[string]any:
			for key := range v {
				v[key] = redact(v[key])
			}
		}
		return v
	}
	return json.Marshal(redact(value))
}

// actionResult is a common result struct for interaction commands (click, hover, type).
type actionResult struct {
	Action  string                   `json:"action"`
	Ref     string                   `json:"ref,omitempty"`
	Locator string                   `json:"locator,omitempty"`
	Result  *engine.ExtractionResult `json:"result"`
}

// renderProfile resolves the current render profile once per invocation.
func renderProfile() engine.RenderProfile {
	return engine.ResolveProfile(flagProfile, flagFormat)
}

// output picks the right format based on --format / --profile.
// In agent-JSON mode, compact marshaling drops whitespace.
func output(jsonVal any, textVal string) {
	p := renderProfile()
	data, err := renderOutputBytes(jsonVal, textVal, p)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: output render: %v\n", err)
		exitNow(1)
	}
	data = redactOutput(data, flagOutputSecrets)
	data, err = limitOutput(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: output artifact: %v\n", err)
		exitNow(1)
	}
	_, _ = os.Stdout.Write(append(data, '\n'))
}

func renderOutputBytes(jsonVal any, textVal string, p engine.RenderProfile) ([]byte, error) {
	if flagRaw {
		switch value := jsonVal.(type) {
		case string:
			return []byte(value), nil
		case []byte:
			return value, nil
		default:
			data, err := json.Marshal(jsonVal)
			if err != nil {
				return nil, err
			}
			var wrapper map[string]json.RawMessage
			if json.Unmarshal(data, &wrapper) == nil {
				if result, ok := wrapper["result"]; ok {
					var text string
					if json.Unmarshal(result, &text) == nil {
						return []byte(text), nil
					}
					return result, nil
				}
			}
			return data, nil
		}
	}
	switch flagFormat {
	case "json":
		if p.Agent {
			return json.Marshal(jsonVal)
		}
		return json.MarshalIndent(jsonVal, "", "  ")
	default:
		return []byte(textVal), nil
	}
}

func loadConfiguredOutputSecrets() error {
	values := make([]string, 0)
	if loadedPlaywrightConfig != nil {
		for _, value := range loadedPlaywrightConfig.Config.Secrets {
			if value != "" {
				values = append(values, value)
			}
		}
	}
	path := strings.TrimSpace(flagSecretsFile)
	if path == "" {
		path = strings.TrimSpace(os.Getenv("PLAYWRIGHT_MCP_SECRETS_FILE"))
	}
	if path != "" {
		secrets, err := godotenv.Read(path)
		if err != nil {
			return fmt.Errorf("load secrets file %s: %w", path, err)
		}
		for _, value := range secrets {
			if value != "" {
				values = append(values, value)
			}
		}
	}
	flagOutputSecrets = uniqueSecrets(values)
	return nil
}

func uniqueSecrets(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	// Replace longer values first so an overlapping shorter value cannot leave
	// a recognizable suffix behind.
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

func redactOutput(data []byte, secrets []string) []byte {
	if len(data) == 0 || len(secrets) == 0 {
		return data
	}
	redacted := string(data)
	for _, value := range redactionValues(secrets) {
		redacted = strings.ReplaceAll(redacted, value, "<redacted>")
	}
	return []byte(redacted)
}

// redactionValues includes both the literal secret and the representation that
// encoding/json writes inside a JSON string. The latter escapes HTML-sensitive
// characters and line breaks, so redaction must happen before output reaches
// stdout or an artifact.
func redactionValues(secrets []string) []string {
	seen := make(map[string]struct{}, len(secrets)*2)
	values := make([]string, 0, len(secrets)*2)
	for _, secret := range secrets {
		for _, value := range []string{secret, jsonEscapedString(secret)} {
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	return values
}

func jsonEscapedString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	return string(encoded[1 : len(encoded)-1])
}

func limitOutput(data []byte) ([]byte, error) {
	if flagOutputMaxSize == 0 || len(data) <= flagOutputMaxSize {
		return data, nil
	}
	dir := playwrightArtifactDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, fmt.Sprintf("output-%d.txt", time.Now().UTC().UnixNano()))
	if err := os.WriteFile(path, append(append([]byte(nil), data...), '\n'), 0o600); err != nil {
		return nil, err
	}
	if flagRaw {
		return []byte(path), nil
	}
	if flagFormat == "json" {
		return json.Marshal(map[string]any{
			"artifact":   path,
			"size_bytes": len(data),
			"truncated":  true,
		})
	}
	return []byte(fmt.Sprintf("Output exceeded %d bytes (%d bytes total).\n\n[Output](%s)", flagOutputMaxSize, len(data), path)), nil
}
