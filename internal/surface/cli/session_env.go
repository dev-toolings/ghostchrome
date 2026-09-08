package cli

import (
	"os"
	"strings"
)

func sessionNameFromEnv() string {
	if env := strings.TrimSpace(os.Getenv("PLAYWRIGHT_CLI_SESSION")); env != "" {
		return env
	}
	return strings.TrimSpace(os.Getenv("GHOSTCHROME_SESSION"))
}

func implicitSessionEnabled() bool {
	v := strings.TrimSpace(os.Getenv("GHOSTCHROME_NO_DAEMON"))
	if v == "1" || strings.EqualFold(v, "true") {
		return false
	}
	return true
}
