package agentstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"durpdeploy/internal/agenttls"
)

func TestStore_SaveLoadPersistsOnlyPairingState(t *testing.T) {
	// Given
	directory := filepath.Join(t.TempDir(), "agent")
	store := NewStore(directory)
	pin := agenttls.FingerprintOf([]byte("server-certificate"))
	want, err := New(
		"https://agent.example.test:9443",
		[]agenttls.Fingerprint{pin},
		"agent-1",
	)
	if err != nil {
		t.Fatalf("new state: %v", err)
	}
	want.AgentVersion = "v1.2.3"

	// When
	if err := store.Save(want); err != nil {
		t.Fatalf("save state: %v", err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}

	// Then
	if got.ServerURL != want.ServerURL || got.AgentID != want.AgentID ||
		got.AgentVersion != want.AgentVersion || len(got.ServerPins) != 1 ||
		got.ServerPins[0] != pin {
		t.Fatalf("loaded state = %#v, want %#v", got, want)
	}
	assertMode(t, directory, 0o700)
	assertMode(t, filepath.Join(directory, FileName), 0o600)
	contents, err := os.ReadFile(filepath.Join(directory, FileName))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	for _, forbidden := range []string{
		"payload", "variable", "pair-code", "server-key", "private-key",
	} {
		if strings.Contains(string(contents), forbidden) {
			t.Fatalf("state contains forbidden %q field", forbidden)
		}
	}
}

func TestStore_SaveLoadPersistsStagedServerPins(t *testing.T) {
	// Given
	store := NewStore(t.TempDir())
	current := agenttls.FingerprintOf([]byte("current-server-certificate"))
	pending := agenttls.FingerprintOf([]byte("pending-server-certificate"))
	state, err := New(
		"https://agent.example.test",
		[]agenttls.Fingerprint{current, pending},
		"agent-1",
	)
	if err != nil {
		t.Fatalf("new state: %v", err)
	}

	// When
	if err := store.Save(state); err != nil {
		t.Fatalf("save state: %v", err)
	}
	got, err := store.Load()

	// Then
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if len(got.ServerPins) != 2 || got.ServerPins[0] != current ||
		got.ServerPins[1] != pending {
		t.Fatalf("loaded staged pins = %v", got.ServerPins)
	}
}

func TestStore_LoadReadsLegacyStateWithoutAgentVersion(t *testing.T) {
	// Given
	directory := t.TempDir()
	store := NewStore(directory)
	pin := agenttls.FingerprintOf([]byte("server-certificate"))
	contents, err := json.Marshal(diskState{
		ServerURL:  "https://agent.example.test:9443",
		ServerPins: []string{pin.String()},
		AgentID:    "agent-1",
	})
	if err != nil {
		t.Fatalf("marshal legacy state: %v", err)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("create state directory: %v", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("secure state directory: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, FileName),
		contents,
		0o600,
	); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	// When
	got, err := store.Load()

	// Then
	if err != nil {
		t.Fatalf("load legacy state: %v", err)
	}
	if got.ServerURL == "" || got.AgentID == "" || len(got.ServerPins) != 1 {
		t.Fatalf("loaded legacy state = %#v", got)
	}
}

func TestStore_LoadRequiresRePairWhenStateIsMissing(t *testing.T) {
	// Given
	store := NewStore(t.TempDir())

	// When
	_, err := store.Load()

	// Then
	if !errors.Is(err, ErrRePairRequired) {
		t.Fatalf("load error = %v, want re-pair requirement", err)
	}
}

func TestStore_LoadRequiresRePairWhenDirectoryIsAccessible(t *testing.T) {
	// Given
	directory := t.TempDir()
	store := NewStore(directory)
	state, err := New(
		"https://agent.example.test",
		[]agenttls.Fingerprint{agenttls.FingerprintOf([]byte("certificate"))},
		"agent-1",
	)
	if err != nil {
		t.Fatalf("new state: %v", err)
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("save state: %v", err)
	}
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatalf("relax state directory: %v", err)
	}

	// When
	_, err = store.Load()

	// Then
	if !errors.Is(err, ErrRePairRequired) {
		t.Fatalf("load error = %v, want re-pair requirement", err)
	}
}

func TestStore_LoadIgnoresInterruptedTemporaryWrite(t *testing.T) {
	// Given
	directory := t.TempDir()
	store := NewStore(directory)
	state, err := New(
		"https://agent.example.test",
		[]agenttls.Fingerprint{agenttls.FingerprintOf([]byte("certificate"))},
		"agent-1",
	)
	if err != nil {
		t.Fatalf("new state: %v", err)
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("save state: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, "."+FileName+".interrupted"),
		[]byte("{"),
		0o600,
	); err != nil {
		t.Fatalf("write interrupted temporary state: %v", err)
	}

	// When
	got, err := store.Load()

	// Then
	if err != nil {
		t.Fatalf("load after interrupted write: %v", err)
	}
	if got.AgentID != state.AgentID {
		t.Fatalf("agent ID = %q, want %q", got.AgentID, state.AgentID)
	}
}

func TestNew_RejectsUnpairedOrUnsafeState(t *testing.T) {
	// Given
	pin := agenttls.FingerprintOf([]byte("certificate"))
	cases := []struct {
		name      string
		serverURL string
		pins      []agenttls.Fingerprint
		agentID   string
	}{
		{"missing URL", "", []agenttls.Fingerprint{pin}, "agent-1"},
		{
			"HTTP URL",
			"http://agent.example.test",
			[]agenttls.Fingerprint{pin},
			"agent-1",
		},
		{"missing pins", "https://agent.example.test", nil, "agent-1"},
		{
			"missing agent ID",
			"https://agent.example.test",
			[]agenttls.Fingerprint{pin},
			"",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// When
			_, err := New(test.serverURL, test.pins, test.agentID)

			// Then
			if err == nil {
				t.Fatal("unsafe state was accepted")
			}
		})
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode %s = %04o, want %04o", path, got, want)
	}
}
