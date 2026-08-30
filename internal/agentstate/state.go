// Package agentstate persists the non-secret pairing state for an agent.
package agentstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"durpdeploy/internal/agenttls"
)

const FileName = "state.json"

var ErrRePairRequired = errors.New("agent state: re-pair required")

// RePairRequiredError reports state that cannot safely establish prior trust.
type RePairRequiredError struct{ cause error }

func (err *RePairRequiredError) Error() string {
	return ErrRePairRequired.Error() + ": " + err.cause.Error()
}

func (err *RePairRequiredError) Unwrap() error { return err.cause }

func (err *RePairRequiredError) Is(target error) bool {
	return target == ErrRePairRequired
}

// State contains the durable pairing values. Agent identity is persisted only
// by agenttls as a certificate and private key, never duplicated here.
type State struct {
	ServerURL    string
	ServerPins   []agenttls.Fingerprint
	AgentID      string
	AgentVersion string
}

// New parses the durable pairing values before they enter application state.
func New(
	serverURL string,
	serverPins []agenttls.Fingerprint,
	agentID string,
) (State, error) {
	if err := validateServerURL(serverURL); err != nil {
		return State{}, err
	}
	if agentID == "" {
		return State{}, errors.New("agent state: agent ID is required")
	}
	if len(serverPins) == 0 || len(serverPins) > 2 {
		return State{}, errors.New(
			"agent state: require one or two server pins",
		)
	}
	pins := append([]agenttls.Fingerprint(nil), serverPins...)
	for index, pin := range pins {
		for _, prior := range pins[:index] {
			if pin == prior {
				return State{}, errors.New(
					"agent state: server pins must be unique",
				)
			}
		}
	}
	return State{ServerURL: serverURL, ServerPins: pins, AgentID: agentID}, nil
}

// Store owns an agent's private directory and its pairing state file.
type Store struct{ directory string }

// NewStore creates a state store rooted at directory.
func NewStore(directory string) Store { return Store{directory: directory} }

// Save atomically replaces the pairing state with restrictive filesystem modes.
func (store Store) Save(state State) error {
	validated, err := New(state.ServerURL, state.ServerPins, state.AgentID)
	if err != nil {
		return err
	}
	validated.AgentVersion = state.AgentVersion
	if err := os.MkdirAll(store.directory, 0o700); err != nil {
		return fmt.Errorf("create agent state directory: %w", err)
	}
	if err := os.Chmod(store.directory, 0o700); err != nil {
		return fmt.Errorf("protect agent state directory: %w", err)
	}
	contents, err := json.Marshal(diskState{
		ServerURL:    validated.ServerURL,
		ServerPins:   fingerprints(validated.ServerPins),
		AgentID:      validated.AgentID,
		AgentVersion: validated.AgentVersion,
	})
	if err != nil {
		return fmt.Errorf("encode agent state: %w", err)
	}
	if err := writeAtomically(
		filepath.Join(store.directory, FileName),
		contents,
	); err != nil {
		return err
	}
	return syncDirectory(store.directory)
}

// Load returns existing pairing state or a typed re-pair requirement.
func (store Store) Load() (State, error) {
	directory, err := os.Stat(store.directory)
	if err != nil {
		return State{}, rePairError("stat pairing state directory", err)
	}
	if directory.Mode().Perm()&0o077 != 0 {
		return State{}, rePairError(
			"pairing state directory permissions",
			errors.New("not private"),
		)
	}
	contents, err := os.ReadFile(filepath.Join(store.directory, FileName))
	if err != nil {
		return State{}, rePairError("read pairing state", err)
	}
	var stored diskState
	if err := json.Unmarshal(contents, &stored); err != nil {
		return State{}, rePairError("decode pairing state", err)
	}
	pins := make([]agenttls.Fingerprint, 0, len(stored.ServerPins))
	for _, raw := range stored.ServerPins {
		pin, err := agenttls.ParseFingerprint(raw)
		if err != nil {
			return State{}, rePairError("parse server pin", err)
		}
		pins = append(pins, pin)
	}
	state, err := New(stored.ServerURL, pins, stored.AgentID)
	if err != nil {
		return State{}, rePairError("validate pairing state", err)
	}
	state.AgentVersion = stored.AgentVersion
	info, err := os.Stat(filepath.Join(store.directory, FileName))
	if err != nil {
		return State{}, rePairError("stat pairing state", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return State{}, rePairError(
			"pairing state permissions",
			errors.New("not private"),
		)
	}
	return state, nil
}

type diskState struct {
	ServerURL    string   `json:"server_url"`
	ServerPins   []string `json:"server_pins"`
	AgentID      string   `json:"agent_id"`
	AgentVersion string   `json:"agent_version,omitempty"`
}

func fingerprints(pins []agenttls.Fingerprint) []string {
	result := make([]string, 0, len(pins))
	for _, pin := range pins {
		result = append(result, pin.String())
	}
	return result
}

func rePairError(operation string, cause error) error {
	return &RePairRequiredError{cause: fmt.Errorf("%s: %w", operation, cause)}
}

func validateServerURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery ||
		parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") ||
		parsed.Hostname() == "" {
		return errors.New("agent state: server URL must be an HTTPS origin")
	}
	return nil
}

func writeAtomically(path string, contents []byte) (err error) {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create temporary pairing state: %w", err)
	}
	temporaryPath := file.Name()
	defer func() {
		if file != nil {
			if closeErr := file.Close(); closeErr != nil && err == nil {
				err = fmt.Errorf("close temporary pairing state: %w", closeErr)
			}
		}
		if err != nil {
			if removeErr := os.Remove(temporaryPath); removeErr != nil &&
				!os.IsNotExist(removeErr) {
				err = errors.Join(
					err,
					fmt.Errorf("remove temporary pairing state: %w", removeErr),
				)
			}
		}
	}()
	if err = file.Chmod(0o600); err != nil {
		return fmt.Errorf("protect temporary pairing state: %w", err)
	}
	if _, err = file.Write(contents); err != nil {
		return fmt.Errorf("write temporary pairing state: %w", err)
	}
	if err = file.Sync(); err != nil {
		return fmt.Errorf("sync temporary pairing state: %w", err)
	}
	if err = file.Close(); err != nil {
		return fmt.Errorf("close temporary pairing state: %w", err)
	}
	file = nil
	if err = os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace pairing state: %w", err)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open agent state directory: %w", err)
	}
	if err := directory.Sync(); err != nil {
		closeErr := directory.Close()
		if closeErr != nil {
			return errors.Join(
				fmt.Errorf("sync agent state directory: %w", err),
				fmt.Errorf("close agent state directory: %w", closeErr),
			)
		}
		return fmt.Errorf("sync agent state directory: %w", err)
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("close agent state directory: %w", err)
	}
	return nil
}
