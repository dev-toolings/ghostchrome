//go:build !windows

package engine

import "os"

func replaceContinuousLog(source, target string) error {
	return os.Rename(source, target)
}
