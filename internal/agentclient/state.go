package agentclient

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"durpdeploy/internal/agenttls"
)

const (
	pinsFileName       = "server-pins.json"
	enrollmentFileName = "enrollment-complete"
)

type pinState struct {
	Pins []string `json:"pins"`
}

func loadPins(
	directory string,
	initial agenttls.Fingerprint,
) ([]agenttls.Fingerprint, error) {
	path := filepath.Join(directory, pinsFileName)
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		pins := []agenttls.Fingerprint{initial}
		return pins, writePins(path, pins)
	}
	if err != nil {
		return nil, fmt.Errorf("read server pins: %w", err)
	}
	var stored pinState
	if err := json.Unmarshal(contents, &stored); err != nil {
		return nil, fmt.Errorf("decode server pins: %w", err)
	}
	return parsePins(stored.Pins)
}

func parsePins(raw []string) ([]agenttls.Fingerprint, error) {
	if len(raw) == 0 || len(raw) > 2 {
		return nil, fmt.Errorf(
			"server pins must contain one or two fingerprints",
		)
	}
	pins := make([]agenttls.Fingerprint, 0, len(raw))
	for _, value := range raw {
		pin, err := agenttls.ParseFingerprint(value)
		if err != nil {
			return nil, fmt.Errorf("parse server fingerprint: %w", err)
		}
		for _, existing := range pins {
			if pin == existing {
				return nil, fmt.Errorf("server pins must be unique")
			}
		}
		pins = append(pins, pin)
	}
	return pins, nil
}

func writePins(path string, pins []agenttls.Fingerprint) (err error) {
	raw := make([]string, 0, len(pins))
	for _, pin := range pins {
		raw = append(raw, pin.String())
	}
	contents, err := json.Marshal(pinState{Pins: raw})
	if err != nil {
		return fmt.Errorf("encode server pins: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create temporary server pins: %w", err)
	}
	temporaryPath := file.Name()
	defer func() {
		if removeErr := os.Remove(
			temporaryPath,
		); removeErr != nil && !os.IsNotExist(removeErr) &&
			err == nil {
			err = fmt.Errorf("remove temporary server pins: %w", removeErr)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("set temporary server pins mode: %w", err)
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return fmt.Errorf("write temporary server pins: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync temporary server pins: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary server pins: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace server pins: %w", err)
	}
	return nil
}

func isEnrolled(directory string) (bool, error) {
	_, err := os.Stat(filepath.Join(directory, enrollmentFileName))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat enrollment state: %w", err)
	}
	return true, nil
}

func markEnrolled(directory string) error {
	path := filepath.Join(directory, enrollmentFileName)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create enrollment state: %w", err)
	}
	return file.Close()
}
