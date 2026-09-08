package cli

import (
	"fmt"
	"path/filepath"
)

func errInvalidArg(name, got, allowed string) error {
	return fmt.Errorf("invalid %s %q: use %s", name, got, allowed)
}

func errNeedRefOrLocator() error {
	return fmt.Errorf("need a @ref or one of --by-role / --by-name / --by-label / --by-text")
}

func validateOutputPath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("output path cannot be empty")
	}
	cleaned := filepath.Clean(p)
	if filepath.IsAbs(cleaned) {
		return cleaned, nil
	}
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("cannot resolve path %q: %w", p, err)
	}
	return abs, nil
}
