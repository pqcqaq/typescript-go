package main

import (
	"errors"
	"fmt"
	"os"
)

func rollbackApplicationOutput(outputPath string, cause error) error {
	if err := os.Remove(outputPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Join(cause, fmt.Errorf("roll back executable %s: %w", outputPath, err))
	}
	return cause
}
