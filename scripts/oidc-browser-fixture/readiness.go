//go:build oidctest

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func writeReadiness(path string, state readinessState) error {
	body, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode readiness: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".oidc-browser-ready-")
	if err != nil {
		return fmt.Errorf("create temporary readiness file: %w", err)
	}
	temporaryPath := file.Name()
	defer os.Remove(temporaryPath)
	if _, err := file.Write(body); err != nil {
		file.Close()
		return fmt.Errorf("write readiness: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync readiness: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close readiness: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish readiness: %w", err)
	}
	return nil
}

func removeReadiness(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove readiness: %w", err)
	}
	return nil
}
