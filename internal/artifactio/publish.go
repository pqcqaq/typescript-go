// Package artifactio owns filesystem publication of verified compiler artifacts.
package artifactio

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// PublishNewFile publishes data at path without replacing an existing file.
//
// Data is written and synced through a staging file in the destination
// directory. A hard link then makes the completed bytes visible atomically;
// unlike rename, the link fails if another process created path after the
// caller's preflight check.
func PublishNewFile(path string, data []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".ts2bin-output-*")
	if err != nil {
		return fmt.Errorf("create artifact staging file: %w", err)
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		_ = os.Remove(temporaryPath)
	}()

	if err := temporary.Chmod(mode); err != nil {
		return fmt.Errorf("chmod staged artifact: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write staged artifact: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync staged artifact: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close staged artifact: %w", err)
	}
	closed = true

	if err := os.Link(temporaryPath, path); err != nil {
		return fmt.Errorf("publish staged artifact: %w", err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		rollbackErr := os.Remove(path)
		if rollbackErr != nil && !errors.Is(rollbackErr, os.ErrNotExist) {
			return errors.Join(
				fmt.Errorf("remove artifact staging link: %w", err),
				fmt.Errorf("roll back published artifact %s: %w", path, rollbackErr),
			)
		}
		return fmt.Errorf("remove artifact staging link: %w", err)
	}
	return nil
}
